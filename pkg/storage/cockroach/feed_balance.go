package cockroach

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"math"
	"math/rand/v2"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/time/rate"
	"k8s.io/apimachinery/pkg/util/wait"
)

const (
	// feedApplicationName names the watch feeds' sessions, so the balancer can
	// count them on each node.
	feedApplicationName = "milo-watch-feed"

	// A balanced feed checks again after balanceMinInterval, doubling up to
	// balanceMaxInterval while nothing changes.
	balanceMinInterval = time.Minute
	balanceMaxInterval = 10 * time.Minute

	// balanceLag is the feed lag that triggers a check right away. Feeds
	// crowded onto one node fall behind together.
	balanceLag = 10 * time.Second
)

var errFeedRebalanced = errors.New("the changefeed is moving to a node running fewer watch feeds")

// feedNode is a node that can take a watch feed, and how many it runs.
type feedNode struct {
	ID    int64
	Host  string
	Feeds int
}

// A feedBalancer connects this server's watch feed to the node running the
// fewest of them, and moves it when the feeds get uneven. CockroachDB runs a
// changefeed without a sink entirely on the node its connection reaches.
type feedBalancer struct {
	config *pgx.ConnConfig
	nodes  func(context.Context) ([]feedNode, error)
	poked  chan struct{}
	pokes  rate.Sometimes
}

func newFeedBalancer(pool *pgxpool.Pool, config *pgx.ConnConfig) *feedBalancer {
	return &feedBalancer{
		config: config,
		nodes:  func(ctx context.Context) ([]feedNode, error) { return listFeedNodes(ctx, pool) },
		poked:  make(chan struct{}, 1),
		pokes:  rate.Sometimes{Interval: balanceMinInterval},
	}
}

// listFeedNodes lists the live nodes that aren't draining, with the watch
// feeds on each. Listing nodes takes CockroachDB 26.4 or later and the
// VIEWCLUSTERMETADATA privilege. The sessions cover every node, for the same
// database user.
func listFeedNodes(ctx context.Context, pool *pgxpool.Pool) ([]feedNode, error) {
	rows, _ := pool.Query(ctx, `
		SELECT n.node_id, n.host, count(s.node_id)
		FROM information_schema.crdb_nodes AS n
		LEFT JOIN [SHOW CLUSTER SESSIONS] AS s ON s.node_id = n.node_id AND s.application_name = $1
		WHERE n.is_live = 'YES' AND n.draining = 'NO' AND n.membership = 'active'
		GROUP BY n.node_id, n.host`, feedApplicationName)
	nodes, err := pgx.CollectRows(rows, pgx.RowToStructByPos[feedNode])
	if err != nil {
		return nil, fmt.Errorf("list the CockroachDB nodes and their watch feeds: %w", err)
	}
	return nodes, nil
}

// dial connects to the node running the fewest feeds, and calls move once the
// feed should go elsewhere, until ctx ends.
func (b *feedBalancer) dial(ctx context.Context, move func()) (*pgx.Conn, error) {
	nodes, err := b.nodes(ctx)
	if err != nil {
		return nil, err
	}
	if len(nodes) == 0 {
		return nil, errors.New("no CockroachDB node is live and taking connections")
	}
	target := leastLoaded(nodes)
	// The connection keeps the DSN's port and TLS server name.
	cfg := b.config.Copy()
	cfg.Host = target.Host
	for _, fb := range cfg.Fallbacks {
		fb.Host = target.Host
	}
	if cfg.RuntimeParams == nil {
		cfg.RuntimeParams = map[string]string{}
	}
	cfg.RuntimeParams["application_name"] = feedApplicationName
	conn, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("connect to CockroachDB node %d: %w", target.ID, err)
	}
	go b.watch(ctx, target.ID, move)
	return conn, nil
}

// lagging asks for a check now, at most once per balanceMinInterval.
func (b *feedBalancer) lagging() {
	b.pokes.Do(func() {
		select {
		case b.poked <- struct{}{}:
		default:
		}
	})
}

// watch checks the feed on node with a backoff, or right away when lagging,
// and calls move once the feed should go elsewhere.
func (b *feedBalancer) watch(ctx context.Context, node int64, move func()) {
	backoff := balanceBackoff()
	timer := time.NewTimer(backoff.Step())
	defer timer.Stop()
	for {
		select {
		case <-timer.C:
		case <-b.poked:
			backoff = balanceBackoff()
		case <-ctx.Done():
			return
		}
		// A failed check leaves the feed where it is.
		if nodes, err := b.nodes(ctx); err == nil && crowded(nodes, node) {
			move()
			return
		}
		timer.Reset(backoff.Step())
	}
}

func balanceBackoff() *wait.Backoff {
	return &wait.Backoff{Duration: balanceMinInterval, Factor: 2, Jitter: 0.5, Steps: math.MaxInt32, Cap: balanceMaxInterval}
}

// leastLoaded picks a node running the fewest feeds, at random among equals,
// so servers starting together spread out. nodes must not be empty.
func leastLoaded(nodes []feedNode) feedNode {
	nodes = slices.Clone(nodes)
	rand.Shuffle(len(nodes), func(i, j int) { nodes[i], nodes[j] = nodes[j], nodes[i] })
	return slices.MinFunc(nodes, func(a, b feedNode) int { return cmp.Compare(a.Feeds, b.Feeds) })
}

// crowded reports whether the feed on node should move: node no longer takes
// feeds, or runs two more than another. Moving at a gap of one would only
// swap which node runs the extra feed.
func crowded(nodes []feedNode, node int64) bool {
	i := slices.IndexFunc(nodes, func(n feedNode) bool { return n.ID == node })
	if i < 0 {
		return len(nodes) > 0
	}
	return nodes[i].Feeds-leastLoaded(nodes).Feeds >= 2
}

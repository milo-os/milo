package etcdshared

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	grpcprom "github.com/grpc-ecosystem/go-grpc-middleware/providers/prometheus"
	"github.com/prometheus/client_golang/prometheus"
	"go.etcd.io/etcd/client/pkg/v3/transport"
	clientv3 "go.etcd.io/etcd/client/v3"
	"go.etcd.io/etcd/client/v3/kubernetes"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/connectivity"

	utilnet "k8s.io/apimachinery/pkg/util/net"
	"k8s.io/apiserver/pkg/server/egressselector"
	"k8s.io/apiserver/pkg/storage/etcd3"
	"k8s.io/apiserver/pkg/storage/storagebackend"
	"k8s.io/component-base/metrics/legacyregistry"
	tracing "k8s.io/component-base/tracing"
)

const (
	keepaliveTime    = 30 * time.Second
	keepaliveTimeout = 10 * time.Second
	dialTimeout      = 20 * time.Second
)

var etcd3ClientLogger = zap.NewNop().Named("etcd-client-shared")

var grpcClientMetrics = upstreamGRPCClientMetrics()

// upstreamGRPCClientMetrics returns the upstream factory's unexported collector,
// which etcd alerts read, from the error of registering an identical one.
func upstreamGRPCClientMetrics() *grpcprom.ClientMetrics {
	m := grpcprom.NewClientMetrics()
	err := legacyregistry.Registerer().Register(m)
	if err == nil {
		return m
	}
	var are prometheus.AlreadyRegisteredError
	if errors.As(err, &are) {
		if existing, ok := are.ExistingCollector.(*grpcprom.ClientMetrics); ok {
			return existing
		}
	}
	panic(fmt.Sprintf("registering etcd grpc client metrics: %v", err))
}

// newSharedETCDClient builds a *kubernetes.Client from a transport config. It is
// a package var so tests can substitute a dial-free fake. It is a faithful copy
// of the unexported newETCD3Client in
// k8s.io/apiserver/pkg/storage/storagebackend/factory: only the transport
// (endpoints + TLS) determines the connection, which is identical across every
// project control plane and resource type.
var newSharedETCDClient = func(c storagebackend.TransportConfig) (*kubernetes.Client, error) {
	tlsInfo := transport.TLSInfo{
		CertFile:      c.CertFile,
		KeyFile:       c.KeyFile,
		TrustedCAFile: c.TrustedCAFile,
	}
	tlsConfig, err := tlsInfo.ClientConfig()
	if err != nil {
		return nil, err
	}
	if len(c.CertFile) == 0 && len(c.KeyFile) == 0 && len(c.TrustedCAFile) == 0 {
		tlsConfig = nil
	}
	networkContext := egressselector.Etcd.AsNetworkContext()
	var egressDialer utilnet.DialFunc
	if c.EgressLookup != nil {
		egressDialer, err = c.EgressLookup(networkContext)
		if err != nil {
			return nil, err
		}
	}
	dialOptions := []grpc.DialOption{
		grpc.WithChainUnaryInterceptor(grpcClientMetrics.UnaryClientInterceptor()),
		grpc.WithChainStreamInterceptor(grpcClientMetrics.StreamClientInterceptor()),
	}
	tracingOpts := []otelgrpc.Option{
		otelgrpc.WithMessageEvents(otelgrpc.ReceivedEvents, otelgrpc.SentEvents),
		otelgrpc.WithPropagators(tracing.Propagators()),
		otelgrpc.WithTracerProvider(c.TracerProvider),
	}
	dialOptions = append(dialOptions,
		grpc.WithStatsHandler(otelgrpc.NewClientHandler(tracingOpts...)))
	if egressDialer != nil {
		dialer := func(ctx context.Context, addr string) (net.Conn, error) {
			if strings.Contains(addr, "//") {
				u, err := url.Parse(addr)
				if err != nil {
					return nil, err
				}
				addr = u.Host
			}
			return egressDialer(ctx, "tcp", addr)
		}
		dialOptions = append(dialOptions, grpc.WithContextDialer(dialer))
	}

	cfg := clientv3.Config{
		DialTimeout:          dialTimeout,
		DialKeepAliveTime:    keepaliveTime,
		DialKeepAliveTimeout: keepaliveTimeout,
		DialOptions:          dialOptions,
		Endpoints:            c.ServerList,
		TLS:                  tlsConfig,
		Logger:               etcd3ClientLogger,
	}

	kClient, err := kubernetes.New(cfg)
	if err != nil {
		return nil, err
	}

	if err := blockUntilReady(dialTimeout, kClient); err != nil {
		_ = kClient.Close()
		return nil, err
	}

	return kClient, nil
}

func blockUntilReady(timeout time.Duration, client *kubernetes.Client) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	conn := client.Client.ActiveConnection()
	if conn == nil {
		return fmt.Errorf("no grpc connection")
	}

	for {
		currentState := conn.GetState()
		if currentState == connectivity.Ready {
			return nil
		}
		if currentState == connectivity.Idle {
			conn.Connect()
		}
		if !conn.WaitForStateChange(ctx, currentState) {
			return fmt.Errorf("etcd grpc connection not ready: %w", ctx.Err())
		}
	}
}

// sharedClientPoolSize is the number of etcd connections opened per transport
// and round-robined across all project control planes and resource types. A
// single connection multiplexes every watch-cache watch over one gRPC watch
// stream; at our scale (~hundreds of projects x dozens of resources that is
// tens of thousands of watches) one stream becomes a head-of-line bottleneck
// and per-prefix caches fall progressively behind the global revision, which
// surfaces as "Too large resource version" / cache consistency failures and
// breaks streaming WatchList. Spreading watches across a small pool keeps the
// per-connection watch count in the range a normal apiserver handles while
// still collapsing the tens of thousands of per-(project x resource)
// connections this package replaced — the memory win is preserved (a few dozen
// connections, not one per resource). It is tunable via
// --shared-etcd-client-pool-size; SetSharedClientPoolSize must be called once at
// startup before any storage is built.
var sharedClientPoolSize = 32

// SetSharedClientPoolSize overrides the per-transport pool size. It is intended
// to be called exactly once during apiserver startup, before the first storage
// (and therefore the first acquireClient) is created, so no synchronization is
// needed against acquireClient. Values below 1 are clamped to 1.
func SetSharedClientPoolSize(n int) {
	if n < 1 {
		n = 1
	}
	sharedClientPoolSize = n
}

type runningClient struct {
	clients []*kubernetes.Client
	next    uint64
	refs    int
}

var (
	clientsMu sync.Mutex
	clients   = map[string]*runningClient{}
)

// transportKey derives the shared-client cache key. Only the transport
// (endpoints + TLS) determines the connection.
func transportKey(c storagebackend.TransportConfig) string {
	endpoints := strings.Join(slices.Sorted(slices.Values(c.ServerList)), ",")
	return fmt.Sprintf("%s|%s|%s|%s", endpoints, c.CertFile, c.KeyFile, c.TrustedCAFile)
}

// acquireClient returns one etcd client from a fixed-size pool shared per
// transport config. All project control planes and resource types that share
// the same transport reuse the same pool of underlying gRPC connections,
// assigned round-robin; per-project isolation is enforced by the etcd key
// prefix at the store layer, not by the connection. Each client's KV is wrapped
// once with the latency tracker (it is stateless and request-context scoped, so
// a single wrapper per client is safe). The returned release func closes the
// pool only when the last reference for the transport is released.
func acquireClient(c storagebackend.TransportConfig) (*kubernetes.Client, func(), error) {
	clientsMu.Lock()
	defer clientsMu.Unlock()

	key := transportKey(c)
	rc, found := clients[key]
	if !found {
		pool := make([]*kubernetes.Client, 0, sharedClientPoolSize)
		for i := 0; i < sharedClientPoolSize; i++ {
			client, err := newSharedETCDClient(c)
			if err != nil {
				for _, pc := range pool {
					_ = pc.Close()
				}
				return nil, nil, err
			}
			client.KV = etcd3.NewETCDLatencyTracker(client.KV)
			pool = append(pool, client)
		}

		rc = &runningClient{clients: pool}
		clients[key] = rc
	}

	rc.refs++
	client := rc.clients[rc.next%uint64(len(rc.clients))]
	rc.next++

	return client, func() {
		clientsMu.Lock()
		defer clientsMu.Unlock()

		rc := clients[key]
		rc.refs--
		if rc.refs == 0 {
			for _, pc := range rc.clients {
				_ = pc.Close()
			}
			delete(clients, key)
		}
	}, nil
}

type runningCompactor struct {
	interval  time.Duration
	client    *clientv3.Client
	compactor etcd3.Compactor
	cancel    func()
	refs      int
}

var (
	compactorsMu sync.Mutex
	compactors   = map[string]*runningCompactor{}
)

// startCompactorOnce starts one compactor per transport, mirroring the
// refcounting semantics of the unexported startCompactorOnce in the upstream
// factory package. The compactor uses its own dedicated client (it must outlive
// individual stores and is never KV-wrapped).
func startCompactorOnce(c storagebackend.TransportConfig, interval time.Duration) (etcd3.Compactor, func(), error) {
	compactorsMu.Lock()
	defer compactorsMu.Unlock()

	if interval == 0 {
		return nil, func() {}, nil
	}
	key := fmt.Sprintf("%v", c)
	if compactor, foundBefore := compactors[key]; !foundBefore || compactor.interval > interval {
		client, err := newSharedETCDClient(c)
		if err != nil {
			return nil, nil, err
		}
		compactorClient := client.Client

		if foundBefore {
			compactor.cancel()
		} else {
			compactor = &runningCompactor{}
			compactors[key] = compactor
		}

		compactor.interval = interval
		compactor.client = compactorClient
		cmp := etcd3.StartCompactorPerEndpoint(compactorClient, interval)
		compactor.compactor = cmp
		compactor.cancel = cmp.Stop
	}

	compactors[key].refs++

	return compactors[key].compactor, func() {
		compactorsMu.Lock()
		defer compactorsMu.Unlock()

		compactor := compactors[key]
		compactor.refs--
		if compactor.refs == 0 {
			compactor.cancel()
			compactor.client.Close()
			delete(compactors, key)
		}
	}, nil
}

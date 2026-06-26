package etcdshared

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	grpcprom "github.com/grpc-ecosystem/go-grpc-prometheus"
	"go.etcd.io/etcd/client/pkg/v3/transport"
	clientv3 "go.etcd.io/etcd/client/v3"
	"go.etcd.io/etcd/client/v3/kubernetes"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"k8s.io/klog/v2"

	utilnet "k8s.io/apimachinery/pkg/util/net"
	"k8s.io/apimachinery/pkg/util/wait"
	genericfeatures "k8s.io/apiserver/pkg/features"
	"k8s.io/apiserver/pkg/server/egressselector"
	"k8s.io/apiserver/pkg/storage/etcd3"
	"k8s.io/apiserver/pkg/storage/etcd3/metrics"
	"k8s.io/apiserver/pkg/storage/storagebackend"
	utilfeature "k8s.io/apiserver/pkg/util/feature"
	tracing "k8s.io/component-base/tracing"
)

const (
	keepaliveTime    = 30 * time.Second
	keepaliveTimeout = 10 * time.Second
	dialTimeout      = 20 * time.Second

	dbMetricsMonitorJitter = 0.5
)

var etcd3ClientLogger = zap.NewNop().Named("etcd-client-shared")

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
		grpc.WithBlock(),
		grpc.WithChainUnaryInterceptor(grpcprom.UnaryClientInterceptor),
		grpc.WithChainStreamInterceptor(grpcprom.StreamClientInterceptor),
	}
	if utilfeature.DefaultFeatureGate.Enabled(genericfeatures.APIServerTracing) {
		tracingOpts := []otelgrpc.Option{
			otelgrpc.WithMessageEvents(otelgrpc.ReceivedEvents, otelgrpc.SentEvents),
			otelgrpc.WithPropagators(tracing.Propagators()),
			otelgrpc.WithTracerProvider(c.TracerProvider),
		}
		dialOptions = append(dialOptions,
			grpc.WithStatsHandler(otelgrpc.NewClientHandler(tracingOpts...)))
	}
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

	return kubernetes.New(cfg)
}

type runningClient struct {
	client            *kubernetes.Client
	stopDBSizeMonitor func()
	refs              int
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

// acquireClient returns a single shared etcd client per transport config. All
// project control planes and resource types that share the same transport reuse
// the same underlying gRPC connection; per-project isolation is enforced by the
// etcd key prefix at the store layer, not by the connection. The client's KV is
// wrapped once with the latency tracker (it is stateless and request-context
// scoped, so a single wrapper is safe and avoids compounding wrappers across
// thousands of resources) and a single DB-size monitor is started for it. The
// returned release func closes the client only when the last reference for the
// transport is released.
func acquireClient(c storagebackend.TransportConfig, dbMetricPollInterval time.Duration) (*kubernetes.Client, func(), error) {
	clientsMu.Lock()
	defer clientsMu.Unlock()

	key := transportKey(c)
	rc, found := clients[key]
	if !found {
		client, err := newSharedETCDClient(c)
		if err != nil {
			return nil, nil, err
		}
		client.KV = etcd3.NewETCDLatencyTracker(client.KV)

		stopDBSizeMonitor, err := startDBSizeMonitorPerEndpoint(client.Client, dbMetricPollInterval)
		if err != nil {
			_ = client.Close()
			return nil, nil, err
		}

		rc = &runningClient{
			client:            client,
			stopDBSizeMonitor: stopDBSizeMonitor,
		}
		clients[key] = rc
	}

	rc.refs++

	return rc.client, func() {
		clientsMu.Lock()
		defer clientsMu.Unlock()

		rc := clients[key]
		rc.refs--
		if rc.refs == 0 {
			rc.stopDBSizeMonitor()
			_ = rc.client.Close()
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

var (
	dbMetricsMonitorsMu sync.Mutex
	dbMetricsMonitors   = map[string]struct{}{}
)

// startDBSizeMonitorPerEndpoint starts a loop to monitor etcd database size and
// update etcd_db_total_size_in_bytes per endpoint. Faithful copy of the
// upstream factory helper; deduped per endpoint within this package.
func startDBSizeMonitorPerEndpoint(client *clientv3.Client, interval time.Duration) (func(), error) {
	if interval == 0 {
		return func() {}, nil
	}
	dbMetricsMonitorsMu.Lock()
	defer dbMetricsMonitorsMu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	for _, ep := range client.Endpoints() {
		if _, found := dbMetricsMonitors[ep]; found {
			continue
		}
		dbMetricsMonitors[ep] = struct{}{}
		endpoint := ep
		klog.V(4).Infof("Start monitoring storage db size metric for endpoint %s with polling interval %v", endpoint, interval)
		go wait.JitterUntilWithContext(ctx, func(context.Context) {
			epStatus, err := client.Maintenance.Status(ctx, endpoint)
			if err != nil {
				klog.V(4).Infof("Failed to get storage db size for ep %s: %v", endpoint, err)
				metrics.UpdateEtcdDbSize(endpoint, -1)
			} else {
				metrics.UpdateEtcdDbSize(endpoint, epStatus.DbSize)
			}
		}, interval, dbMetricsMonitorJitter, true)
	}

	return func() {
		cancel()
	}, nil
}

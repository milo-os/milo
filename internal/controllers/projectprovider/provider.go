package projectprovider

import (
	"context"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/discovery/cached/memory"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/restmapper"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog/v2"
	"golang.org/x/time/rate"

	ppmetrics "go.miloapis.com/milo/internal/controllers/projectprovider/metrics"
)

// Config holds tunable parameters for the project provider. All fields have
// sensible defaults via DefaultConfig().
type Config struct {
	// Workers is the number of concurrent goroutines processing the add queue.
	Workers int
	// MaxRetries is the maximum number of times a failed AddProject is retried.
	MaxRetries int
	// RateLimit is the sustained per-second rate at which projects are added.
	RateLimit float64
	// RateBurst is the burst allowance for project additions.
	RateBurst int
	// BaseBackoff is the initial backoff duration after a failed AddProject.
	BaseBackoff time.Duration
	// MaxBackoff is the upper bound for exponential backoff between retries.
	MaxBackoff time.Duration
}

func DefaultConfig() Config {
	return Config{
		Workers:     5,
		MaxRetries:  10,
		RateLimit:   10,
		RateBurst:   15,
		BaseBackoff: 5 * time.Second,
		MaxBackoff:  60 * time.Second,
	}
}

type Sink interface {
	AddProject(ctx context.Context, id string, cfg *rest.Config) error
	RemoveProject(id string)
}

type Provider struct {
	root       *rest.Config
	dyn        dynamic.Interface
	sink       Sink
	projectGVR schema.GroupVersionResource
	cfg        Config
}

func New(root *rest.Config, sink Sink, cfg Config) (*Provider, error) {
	dyn, err := dynamic.NewForConfig(root)
	if err != nil {
		return nil, err
	}
	gvr, err := resolveProjectGVR(root, "resourcemanager.miloapis.com", "v1alpha1")
	if err != nil {
		return nil, err
	}

	ppmetrics.Register()

	return &Provider{root: root, dyn: dyn, sink: sink, projectGVR: gvr, cfg: cfg}, nil
}

func (p *Provider) cfgForProject(id string) *rest.Config {
	c := rest.CopyConfig(p.root)
	c.Host = strings.TrimSuffix(p.root.Host, "/") + "/projects/" + id + "/control-plane"
	return c
}

func (p *Provider) Run(ctx context.Context) error {
	queue := workqueue.NewTypedRateLimitingQueueWithConfig(
		workqueue.NewTypedMaxOfRateLimiter(
			workqueue.NewTypedItemExponentialFailureRateLimiter[string](p.cfg.BaseBackoff, p.cfg.MaxBackoff),
			&workqueue.TypedBucketRateLimiter[string]{Limiter: rate.NewLimiter(rate.Limit(p.cfg.RateLimit), p.cfg.RateBurst)},
		),
		workqueue.TypedRateLimitingQueueConfig[string]{Name: "project_provider"},
	)
	defer queue.ShutDown()

	lw := &cache.ListWatch{
		ListFunc: func(lo metav1.ListOptions) (runtime.Object, error) {
			return p.dyn.Resource(p.projectGVR).List(ctx, lo)
		},
		WatchFunc: func(lo metav1.ListOptions) (watch.Interface, error) {
			return p.dyn.Resource(p.projectGVR).Watch(ctx, lo)
		},
	}
	inf := cache.NewSharedIndexInformer(lw, &unstructured.Unstructured{}, 0, nil)

	inf.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(o interface{}) {
			id := o.(*unstructured.Unstructured).GetName()
			queue.Add(id)
		},
		DeleteFunc: func(o interface{}) {
			if d, ok := o.(cache.DeletedFinalStateUnknown); ok {
				o = d.Obj
			}
			id := o.(*unstructured.Unstructured).GetName()
			p.sink.RemoveProject(id)
		},
	})

	go inf.Run(ctx.Done())

	// Periodically report queue depth.
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				ppmetrics.QueueDepth.Set(float64(queue.Len()))
			}
		}
	}()

	for i := 0; i < p.cfg.Workers; i++ {
		go p.runWorker(ctx, queue)
	}

	<-ctx.Done()
	return nil
}

func (p *Provider) runWorker(ctx context.Context, queue workqueue.TypedRateLimitingInterface[string]) {
	for {
		id, quit := queue.Get()
		if quit {
			return
		}
		p.processProject(ctx, queue, id)
		queue.Done(id)
	}
}

func (p *Provider) processProject(ctx context.Context, queue workqueue.TypedRateLimitingInterface[string], id string) {
	start := time.Now()
	err := p.sink.AddProject(ctx, id, p.cfgForProject(id))
	ppmetrics.ProjectAddDurationSeconds.Observe(time.Since(start).Seconds())

	if err == nil {
		ppmetrics.ProjectAddTotal.WithLabelValues("success").Inc()
		queue.Forget(id)
		return
	}

	retries := queue.NumRequeues(id)
	if retries < p.cfg.MaxRetries {
		ppmetrics.ProjectAddTotal.WithLabelValues("error").Inc()
		ppmetrics.ProjectAddRetriesTotal.Inc()
		klog.V(2).Infof("Failed to add project %q (attempt %d/%d, will retry): %v",
			id, retries+1, p.cfg.MaxRetries, err)
		queue.AddRateLimited(id)
		return
	}

	ppmetrics.ProjectAddTotal.WithLabelValues("abandoned").Inc()
	klog.Errorf("Failed to add project %q after %d attempts, giving up: %v",
		id, p.cfg.MaxRetries, err)
	queue.Forget(id)
}

func resolveProjectGVR(cfg *rest.Config, group, preferredVersion string) (schema.GroupVersionResource, error) {
	disc, err := discovery.NewDiscoveryClientForConfig(cfg)
	if err != nil {
		return schema.GroupVersionResource{}, err
	}
	rm := restmapper.NewDeferredDiscoveryRESTMapper(memory.NewMemCacheClient(disc))

	mapping, err := rm.RESTMapping(schema.GroupKind{Group: group, Kind: "Project"}, preferredVersion)
	if err != nil {
		return schema.GroupVersionResource{}, err
	}
	return mapping.Resource, nil
}

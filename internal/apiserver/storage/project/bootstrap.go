package projectstorage

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/klog/v2"
)

const (
	miloSystemNamespace = "milo-system"

	DefaultNamespaceBootstrapTimeout = 5 * time.Second

	defaultBootstrapRetryAfter = 30 * time.Second
)

type NamespaceBootstrapper struct {
	timeout    time.Duration
	retryAfter time.Duration
	now        func() time.Time
	run        func(ctx context.Context, project string) error

	ready sync.Map

	mu       sync.Mutex
	attempts map[string]*bootstrapAttempt
}

type bootstrapAttempt struct {
	done     chan struct{}
	err      error
	finished time.Time
}

func NewNamespaceBootstrapper(loopbackConfig *rest.Config, timeout time.Duration) *NamespaceBootstrapper {
	if loopbackConfig == nil {
		return nil
	}
	return newNamespaceBootstrapper(timeout, func(ctx context.Context, project string) error {
		return createMiloSystemNamespace(ctx, loopbackConfig, project)
	})
}

func newNamespaceBootstrapper(timeout time.Duration, run func(ctx context.Context, project string) error) *NamespaceBootstrapper {
	if timeout <= 0 {
		timeout = DefaultNamespaceBootstrapTimeout
	}
	return &NamespaceBootstrapper{
		timeout:    timeout,
		retryAfter: defaultBootstrapRetryAfter,
		now:        time.Now,
		run:        run,
		attempts:   map[string]*bootstrapAttempt{},
	}
}

func (b *NamespaceBootstrapper) Ensure(ctx context.Context, project string) {
	if b == nil || project == "" {
		return
	}
	if _, ok := b.ready.Load(project); ok {
		return
	}

	b.mu.Lock()
	if a, ok := b.attempts[project]; ok {
		select {
		case <-a.done:
			if a.err == nil || b.now().Sub(a.finished) < b.retryAfter {
				b.mu.Unlock()
				return
			}
		default:
			b.mu.Unlock()
			select {
			case <-a.done:
			case <-ctx.Done():
			}
			return
		}
	}
	a := &bootstrapAttempt{done: make(chan struct{})}
	b.attempts[project] = a
	b.mu.Unlock()

	start := b.now()
	runCtx, cancel := context.WithTimeout(context.Background(), b.timeout)
	err := b.run(runCtx, project)
	cancel()

	result := "success"
	if err != nil {
		result = "error"
		klog.ErrorS(err, "Failed to bootstrap namespace in project control plane",
			"project", project, "namespace", miloSystemNamespace)
	}
	namespaceBootstrapDuration.WithLabelValues(result).Observe(b.now().Sub(start).Seconds())

	b.mu.Lock()
	a.err = err
	a.finished = b.now()
	if err == nil {
		b.ready.Store(project, struct{}{})
		delete(b.attempts, project)
	}
	b.mu.Unlock()
	close(a.done)
}

func createMiloSystemNamespace(ctx context.Context, loopbackConfig *rest.Config, project string) error {
	cfg := rest.CopyConfig(loopbackConfig)
	cfg.Host = strings.TrimSuffix(cfg.Host, "/") +
		fmt.Sprintf("/apis/resourcemanager.miloapis.com/v1alpha1/projects/%s/control-plane", project)

	clientset, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return fmt.Errorf("build client for project %s: %w", project, err)
	}

	_, err = clientset.CoreV1().Namespaces().Get(ctx, miloSystemNamespace, metav1.GetOptions{})
	if err == nil {
		return nil
	}
	if !apierrors.IsNotFound(err) {
		return fmt.Errorf("get namespace %s in project %s: %w", miloSystemNamespace, project, err)
	}

	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name:   miloSystemNamespace,
			Labels: map[string]string{"miloapis.com/system": "true"},
		},
	}
	_, err = clientset.CoreV1().Namespaces().Create(ctx, ns, metav1.CreateOptions{})
	if err != nil && !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("create namespace %s in project %s: %w", miloSystemNamespace, project, err)
	}
	return nil
}

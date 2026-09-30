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
	defaultBootstrapReadyTTL   = 10 * time.Minute
)

type NamespaceBootstrapper struct {
	timeout    time.Duration
	retryAfter time.Duration
	readyTTL   time.Duration
	now        func() time.Time
	run        func(ctx context.Context, project string) error

	ready sync.Map

	mu        sync.Mutex
	attempts  map[string]*bootstrapAttempt
	lastSweep time.Time
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
		readyTTL:   defaultBootstrapReadyTTL,
		now:        time.Now,
		run:        run,
		attempts:   map[string]*bootstrapAttempt{},
	}
}

func (b *NamespaceBootstrapper) Ensure(ctx context.Context, project string) {
	if b == nil || project == "" {
		return
	}
	now := b.now()
	if expiry, ok := b.ready.Load(project); ok && now.Before(expiry.(time.Time)) {
		return
	}

	b.mu.Lock()
	b.sweepLocked(now)
	a, ok := b.attempts[project]
	if ok && b.finishedLocked(a) {
		if a.err == nil || now.Sub(a.finished) < b.retryAfter {
			b.mu.Unlock()
			return
		}
		ok = false
	}
	if !ok {
		a = &bootstrapAttempt{done: make(chan struct{})}
		b.attempts[project] = a
		go b.attempt(project, a)
	}
	b.mu.Unlock()

	select {
	case <-a.done:
	case <-ctx.Done():
	}
}

func (b *NamespaceBootstrapper) attempt(project string, a *bootstrapAttempt) {
	start := b.now()
	var err error
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("namespace bootstrap panicked: %v", r)
		}
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
			b.ready.Store(project, a.finished.Add(b.readyTTL))
			if b.attempts[project] == a {
				delete(b.attempts, project)
			}
		}
		b.mu.Unlock()
		close(a.done)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), b.timeout)
	defer cancel()
	err = b.run(ctx, project)
}

func (b *NamespaceBootstrapper) finishedLocked(a *bootstrapAttempt) bool {
	select {
	case <-a.done:
		return true
	default:
		return false
	}
}

func (b *NamespaceBootstrapper) sweepLocked(now time.Time) {
	if now.Sub(b.lastSweep) < b.retryAfter {
		return
	}
	b.lastSweep = now
	for project, a := range b.attempts {
		if b.finishedLocked(a) && now.Sub(a.finished) >= b.retryAfter {
			delete(b.attempts, project)
		}
	}
	b.ready.Range(func(project, expiry any) bool {
		if !now.Before(expiry.(time.Time)) {
			b.ready.Delete(project)
		}
		return true
	})
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

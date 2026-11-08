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
	"k8s.io/apimachinery/pkg/types"
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
	attempts  map[bootstrapKey]*bootstrapAttempt
	lastSweep time.Time
}

// bootstrapKey names one Project: its name and its UID. The ready mark, the
// attempt in flight and the back-off all belong to one key, so a Project
// created again under a name starts afresh.
type bootstrapKey struct {
	name string
	uid  types.UID
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
		attempts:   map[bootstrapKey]*bootstrapAttempt{},
	}
}

// Ensure creates milo-system in the control plane of the live Project named
// project with uid, unless this process holds a fresh ready mark for it. The
// caller has looked the Project up; a failed bootstrap fails no request.
func (b *NamespaceBootstrapper) Ensure(ctx context.Context, project string, uid types.UID) {
	if b == nil || project == "" {
		return
	}
	key := bootstrapKey{name: project, uid: uid}
	now := b.now()
	if expiry, ok := b.ready.Load(key); ok && now.Before(expiry.(time.Time)) {
		return
	}

	a := b.attemptFor(key, now)
	if a == nil {
		return
	}
	select {
	case <-a.done:
	case <-ctx.Done():
	}
}

// attemptFor returns the attempt a caller for key waits on: the one in flight,
// or a new one. It returns nil when the last attempt succeeded, or failed less
// than retryAfter ago.
func (b *NamespaceBootstrapper) attemptFor(key bootstrapKey, now time.Time) *bootstrapAttempt {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.sweepLocked(now)
	a, ok := b.attempts[key]
	if ok && b.finishedLocked(a) {
		if a.err == nil || now.Sub(a.finished) < b.retryAfter {
			return nil
		}
		ok = false
	}
	if !ok {
		a = &bootstrapAttempt{done: make(chan struct{})}
		b.attempts[key] = a
		go b.attempt(key, a)
	}
	return a
}

func (b *NamespaceBootstrapper) attempt(key bootstrapKey, a *bootstrapAttempt) {
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
				"project", key.name, "uid", key.uid, "namespace", miloSystemNamespace)
		}
		namespaceBootstrapDuration.WithLabelValues(result).Observe(b.now().Sub(start).Seconds())

		b.mu.Lock()
		a.err = err
		a.finished = b.now()
		if err == nil {
			b.ready.Store(key, a.finished.Add(b.readyTTL))
			if b.attempts[key] == a {
				delete(b.attempts, key)
			}
		}
		b.mu.Unlock()
		close(a.done)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), b.timeout)
	defer cancel()
	err = b.run(ctx, key.name)
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
	for key, a := range b.attempts {
		if b.finishedLocked(a) && now.Sub(a.finished) >= b.retryAfter {
			delete(b.attempts, key)
		}
	}
	b.ready.Range(func(key, expiry any) bool {
		if !now.Before(expiry.(time.Time)) {
			b.ready.Delete(key)
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

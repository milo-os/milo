package events

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"time"

	corev1 "k8s.io/api/core/v1"
	eventsv1 "k8s.io/api/events/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/watch"
	apirequest "k8s.io/apiserver/pkg/endpoints/request"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/transport"
)

// DynamicProvider proxies Kubernetes Events to the Activity API server,
// injecting X-Remote-* headers to forward user context
type DynamicProvider struct {
	base        *rest.Config
	gvr         schema.GroupVersionResource
	eventsv1GVR schema.GroupVersionResource
	to          time.Duration
	retries     int
	allowExtras map[string]struct{}
}

// NewDynamicProvider creates a new events provider that proxies to the Activity service.
func NewDynamicProvider(cfg Config) (*DynamicProvider, error) {
	if cfg.ProviderURL == "" {
		return nil, fmt.Errorf("ProviderURL is required")
	}

	base := &rest.Config{}
	base.Host = cfg.ProviderURL

	var sni string
	if u, err := url.Parse(cfg.ProviderURL); err == nil {
		sni = u.Hostname()
	}

	base.TLSClientConfig = rest.TLSClientConfig{
		CAFile:     cfg.CAFile,
		CertFile:   cfg.ClientCertFile,
		KeyFile:    cfg.ClientKeyFile,
		Insecure:   false,
		ServerName: sni,
	}

	if cfg.Timeout > 0 {
		base.Timeout = cfg.Timeout
	}

	gvr := schema.GroupVersionResource{
		Group:    "",
		Version:  "v1",
		Resource: "events",
	}

	eventsv1GVR := schema.GroupVersionResource{
		Group:    "events.k8s.io",
		Version:  "v1",
		Resource: "events",
	}

	return &DynamicProvider{
		base:        base,
		gvr:         gvr,
		eventsv1GVR: eventsv1GVR,
		to:          cfg.Timeout,
		retries:     max(0, cfg.Retries),
		allowExtras: cfg.ExtrasAllow,
	}, nil
}

// dynForUser creates a per-request dynamic client that forwards user identity via X-Remote-* headers
func (p *DynamicProvider) dynForUser(ctx context.Context) (dynamic.Interface, error) {
	u, ok := apirequest.UserFrom(ctx)
	if !ok || u == nil {
		return nil, fmt.Errorf("no user in context")
	}

	cfg := rest.CopyConfig(p.base)
	if p.to > 0 {
		cfg.Timeout = p.to
	}

	prev := cfg.WrapTransport
	cfg.WrapTransport = func(rt http.RoundTripper) http.RoundTripper {
		if prev != nil {
			rt = prev(rt)
		}
		return transport.NewAuthProxyRoundTripper(
			u.GetName(),
			u.GetUID(),
			u.GetGroups(),
			p.filterExtras(u.GetExtra()),
			rt,
		)
	}

	return dynamic.NewForConfig(cfg)
}

func (p *DynamicProvider) filterExtras(src map[string][]string) map[string][]string {
	if len(p.allowExtras) == 0 || len(src) == 0 {
		return nil
	}
	out := make(map[string][]string, len(src))
	for k, v := range src {
		if _, ok := p.allowExtras[k]; ok {
			out[k] = v
		}
	}
	return out
}

// CreateEvent creates a new event in Activity storage.
func (p *DynamicProvider) CreateEvent(ctx context.Context, namespace string, event *corev1.Event) (*corev1.Event, error) {
	dyn, err := p.dynForUser(ctx)
	if err != nil {
		return nil, err
	}

	unstructuredEvent, err := runtime.DefaultUnstructuredConverter.ToUnstructured(event)
	if err != nil {
		return nil, err
	}
	uobj := &unstructured.Unstructured{Object: unstructuredEvent}

	var lastErr error
	var result *unstructured.Unstructured
	for i := 0; i <= p.retries; i++ {
		result, lastErr = dyn.Resource(p.gvr).Namespace(namespace).Create(ctx, uobj, metav1.CreateOptions{})
		if lastErr == nil {
			break
		}
		if !isTransient(lastErr) {
			return nil, lastErr
		}
	}
	if lastErr != nil {
		return nil, lastErr
	}

	out := &corev1.Event{}
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(result.UnstructuredContent(), out); err != nil {
		return nil, err
	}

	return out, nil
}

// GetEvent retrieves an event by name from Activity storage.
func (p *DynamicProvider) GetEvent(ctx context.Context, namespace, name string) (*corev1.Event, error) {
	dyn, err := p.dynForUser(ctx)
	if err != nil {
		return nil, err
	}

	var lastErr error
	var uobj *unstructured.Unstructured
	for i := 0; i <= p.retries; i++ {
		uobj, lastErr = dyn.Resource(p.gvr).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
		if lastErr == nil {
			break
		}
		if !isTransient(lastErr) {
			return nil, lastErr
		}
	}
	if lastErr != nil {
		return nil, lastErr
	}

	out := &corev1.Event{}
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(uobj.UnstructuredContent(), out); err != nil {
		return nil, err
	}

	return out, nil
}

// ListEvents lists events from Activity storage with optional filtering.
func (p *DynamicProvider) ListEvents(ctx context.Context, namespace string, opts *metav1.ListOptions) (*corev1.EventList, error) {
	if opts == nil {
		opts = &metav1.ListOptions{}
	}

	dyn, err := p.dynForUser(ctx)
	if err != nil {
		return nil, err
	}

	var lastErr error
	var ul *unstructured.UnstructuredList
	for i := 0; i <= p.retries; i++ {
		ul, lastErr = dyn.Resource(p.gvr).Namespace(namespace).List(ctx, *opts)
		if lastErr == nil {
			break
		}
		if !isTransient(lastErr) {
			return nil, lastErr
		}
	}
	if lastErr != nil {
		return nil, lastErr
	}

	out := &corev1.EventList{}
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(ul.UnstructuredContent(), out); err != nil {
		return nil, err
	}

	return out, nil
}

// UpdateEvent updates an existing event in Activity storage.
func (p *DynamicProvider) UpdateEvent(ctx context.Context, namespace string, event *corev1.Event) (*corev1.Event, error) {
	dyn, err := p.dynForUser(ctx)
	if err != nil {
		return nil, err
	}

	unstructuredEvent, err := runtime.DefaultUnstructuredConverter.ToUnstructured(event)
	if err != nil {
		return nil, err
	}
	uobj := &unstructured.Unstructured{Object: unstructuredEvent}

	var lastErr error
	var result *unstructured.Unstructured
	for i := 0; i <= p.retries; i++ {
		result, lastErr = dyn.Resource(p.gvr).Namespace(namespace).Update(ctx, uobj, metav1.UpdateOptions{})
		if lastErr == nil {
			break
		}
		if !isTransient(lastErr) {
			return nil, lastErr
		}
	}
	if lastErr != nil {
		return nil, lastErr
	}

	out := &corev1.Event{}
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(result.UnstructuredContent(), out); err != nil {
		return nil, err
	}

	return out, nil
}

// DeleteEvent deletes an event from Activity storage.
func (p *DynamicProvider) DeleteEvent(ctx context.Context, namespace, name string, opts *metav1.DeleteOptions) error {
	if opts == nil {
		opts = &metav1.DeleteOptions{}
	}

	dyn, err := p.dynForUser(ctx)
	if err != nil {
		return err
	}

	var lastErr error
	for i := 0; i <= p.retries; i++ {
		lastErr = dyn.Resource(p.gvr).Namespace(namespace).Delete(ctx, name, *opts)
		if lastErr == nil {
			return nil
		}
		if !isTransient(lastErr) {
			return lastErr
		}
	}

	return lastErr
}

// WatchEvents establishes a watch connection to Activity storage for event changes.
func (p *DynamicProvider) WatchEvents(ctx context.Context, namespace string, opts *metav1.ListOptions) (watch.Interface, error) {
	if opts == nil {
		opts = &metav1.ListOptions{}
	}

	dyn, err := p.dynForUser(ctx)
	if err != nil {
		return nil, err
	}

	return dyn.Resource(p.gvr).Namespace(namespace).Watch(ctx, *opts)
}

// isTransient returns true if the error should be retried
func isTransient(err error) bool {
	if err == nil {
		return false
	}

	var netErr net.Error
	if errors.As(err, &netErr) {
		return true
	}

	if apierrors.IsTimeout(err) || apierrors.IsServerTimeout(err) {
		return true
	}
	if apierrors.IsServiceUnavailable(err) {
		return true
	}
	if apierrors.IsInternalError(err) {
		return true
	}
	if apierrors.IsTooManyRequests(err) {
		return true
	}

	return false
}

// CreateEventsV1Event creates a new events.k8s.io/v1 event in Activity storage.
func (p *DynamicProvider) CreateEventsV1Event(ctx context.Context, namespace string, event *eventsv1.Event) (*eventsv1.Event, error) {
	dyn, err := p.dynForUser(ctx)
	if err != nil {
		return nil, err
	}

	unstructuredEvent, err := runtime.DefaultUnstructuredConverter.ToUnstructured(event)
	if err != nil {
		return nil, err
	}
	uobj := &unstructured.Unstructured{Object: unstructuredEvent}

	var lastErr error
	var result *unstructured.Unstructured
	for i := 0; i <= p.retries; i++ {
		result, lastErr = dyn.Resource(p.eventsv1GVR).Namespace(namespace).Create(ctx, uobj, metav1.CreateOptions{})
		if lastErr == nil {
			break
		}
		if !isTransient(lastErr) {
			return nil, lastErr
		}
	}
	if lastErr != nil {
		return nil, lastErr
	}

	out := &eventsv1.Event{}
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(result.UnstructuredContent(), out); err != nil {
		return nil, err
	}

	return out, nil
}

// GetEventsV1Event retrieves an events.k8s.io/v1 event by name from Activity storage.
func (p *DynamicProvider) GetEventsV1Event(ctx context.Context, namespace, name string) (*eventsv1.Event, error) {
	dyn, err := p.dynForUser(ctx)
	if err != nil {
		return nil, err
	}

	var lastErr error
	var uobj *unstructured.Unstructured
	for i := 0; i <= p.retries; i++ {
		uobj, lastErr = dyn.Resource(p.eventsv1GVR).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
		if lastErr == nil {
			break
		}
		if !isTransient(lastErr) {
			return nil, lastErr
		}
	}
	if lastErr != nil {
		return nil, lastErr
	}

	out := &eventsv1.Event{}
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(uobj.UnstructuredContent(), out); err != nil {
		return nil, err
	}

	return out, nil
}

// ListEventsV1Events lists events.k8s.io/v1 events from Activity storage with optional filtering.
func (p *DynamicProvider) ListEventsV1Events(ctx context.Context, namespace string, opts *metav1.ListOptions) (*eventsv1.EventList, error) {
	if opts == nil {
		opts = &metav1.ListOptions{}
	}

	dyn, err := p.dynForUser(ctx)
	if err != nil {
		return nil, err
	}

	var lastErr error
	var ul *unstructured.UnstructuredList
	for i := 0; i <= p.retries; i++ {
		ul, lastErr = dyn.Resource(p.eventsv1GVR).Namespace(namespace).List(ctx, *opts)
		if lastErr == nil {
			break
		}
		if !isTransient(lastErr) {
			return nil, lastErr
		}
	}
	if lastErr != nil {
		return nil, lastErr
	}

	out := &eventsv1.EventList{}
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(ul.UnstructuredContent(), out); err != nil {
		return nil, err
	}

	return out, nil
}

// UpdateEventsV1Event updates an existing events.k8s.io/v1 event in Activity storage.
func (p *DynamicProvider) UpdateEventsV1Event(ctx context.Context, namespace string, event *eventsv1.Event) (*eventsv1.Event, error) {
	dyn, err := p.dynForUser(ctx)
	if err != nil {
		return nil, err
	}

	unstructuredEvent, err := runtime.DefaultUnstructuredConverter.ToUnstructured(event)
	if err != nil {
		return nil, err
	}
	uobj := &unstructured.Unstructured{Object: unstructuredEvent}

	var lastErr error
	var result *unstructured.Unstructured
	for i := 0; i <= p.retries; i++ {
		result, lastErr = dyn.Resource(p.eventsv1GVR).Namespace(namespace).Update(ctx, uobj, metav1.UpdateOptions{})
		if lastErr == nil {
			break
		}
		if !isTransient(lastErr) {
			return nil, lastErr
		}
	}
	if lastErr != nil {
		return nil, lastErr
	}

	out := &eventsv1.Event{}
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(result.UnstructuredContent(), out); err != nil {
		return nil, err
	}

	return out, nil
}

// DeleteEventsV1Event deletes an events.k8s.io/v1 event from Activity storage.
func (p *DynamicProvider) DeleteEventsV1Event(ctx context.Context, namespace, name string, opts *metav1.DeleteOptions) error {
	if opts == nil {
		opts = &metav1.DeleteOptions{}
	}

	dyn, err := p.dynForUser(ctx)
	if err != nil {
		return err
	}

	var lastErr error
	for i := 0; i <= p.retries; i++ {
		lastErr = dyn.Resource(p.eventsv1GVR).Namespace(namespace).Delete(ctx, name, *opts)
		if lastErr == nil {
			return nil
		}
		if !isTransient(lastErr) {
			return lastErr
		}
	}

	return lastErr
}

// WatchEventsV1Events establishes a watch connection to Activity storage for events.k8s.io/v1 event changes.
func (p *DynamicProvider) WatchEventsV1Events(ctx context.Context, namespace string, opts *metav1.ListOptions) (watch.Interface, error) {
	if opts == nil {
		opts = &metav1.ListOptions{}
	}

	dyn, err := p.dynForUser(ctx)
	if err != nil {
		return nil, err
	}

	return dyn.Resource(p.eventsv1GVR).Namespace(namespace).Watch(ctx, *opts)
}

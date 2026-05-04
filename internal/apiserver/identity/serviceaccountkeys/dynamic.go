package serviceaccountkeys

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"time"

	identityv1alpha1 "go.miloapis.com/milo/pkg/apis/identity/v1alpha1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	authuser "k8s.io/apiserver/pkg/authentication/user"
	apirequest "k8s.io/apiserver/pkg/endpoints/request"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/transport"
)

// Config controls how the provider talks to the remote serviceaccountkeys API **always via a remote URL**.

type Config struct {
	BaseConfig *rest.Config

	ProviderURL string

	CAFile         string
	ClientCertFile string
	ClientKeyFile  string

	Timeout     time.Duration
	Retries     int
	ExtrasAllow map[string]struct{}
}

// DynamicProvider implements Backend by proxying to a remote auth-provider
// that serves the serviceaccountkeys API (e.g. auth-provider-zitadel).
type DynamicProvider struct {
	base        *rest.Config
	gvr         schema.GroupVersionResource
	to          time.Duration
	retries     int
	allowExtras map[string]struct{}
}

func NewDynamicProvider(cfg Config) (*DynamicProvider, error) {
	if cfg.ProviderURL == "" {
		return nil, fmt.Errorf("ProviderURL is required")
	}

	// Build from scratch
	base := &rest.Config{}
	base.Host = cfg.ProviderURL

	var sni string
	if u, err := url.Parse(cfg.ProviderURL); err == nil {
		sni = u.Hostname()
	}

	// Wire TLS from files
	base.TLSClientConfig = rest.TLSClientConfig{
		CAFile:   cfg.CAFile,
		CertFile: cfg.ClientCertFile,
		KeyFile:  cfg.ClientKeyFile,
		// We enforce verification; set Insecure=true only for dev
		Insecure:   false,
		ServerName: sni,
	}

	// Respect our explicit timeout
	if cfg.Timeout > 0 {
		base.Timeout = cfg.Timeout
	}

	gvr := identityv1alpha1.SchemeGroupVersion.WithResource("serviceaccountkeys")

	return &DynamicProvider{
		base:        base,
		gvr:         gvr,
		to:          cfg.Timeout,
		retries:     max(0, cfg.Retries),
		allowExtras: cfg.ExtrasAllow,
	}, nil
}

// dynForUser creates a per-call client-go dynamic.Interface that forwards identity via X-Remote-*.
func (b *DynamicProvider) dynForUser(ctx context.Context) (dynamic.Interface, error) {
	u, ok := apirequest.UserFrom(ctx)
	if !ok || u == nil {
		return nil, fmt.Errorf("no user in context")
	}
	cfg := rest.CopyConfig(b.base)
	if b.to > 0 {
		cfg.Timeout = b.to
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
			b.filterExtras(u.GetExtra()),
			rt,
		)
	}
	return dynamic.NewForConfig(cfg)
}

func (b *DynamicProvider) filterExtras(src map[string][]string) map[string][]string {
	if len(b.allowExtras) == 0 || len(src) == 0 {
		return nil
	}
	out := make(map[string][]string, len(src))
	for k, v := range src {
		if _, ok := b.allowExtras[k]; ok {
			out[k] = v
		}
	}
	return out
}

// ---- Public API (implements Backend) ----

func (b *DynamicProvider) CreateServiceAccountKey(ctx context.Context, _ authuser.Info, key *identityv1alpha1.ServiceAccountKey, opts *metav1.CreateOptions) (*identityv1alpha1.ServiceAccountKey, error) {
	dyn, err := b.dynForUser(ctx)
	if err != nil {
		return nil, err
	}
	if opts == nil {
		opts = &metav1.CreateOptions{}
	}

	// Convert to unstructured for the dynamic client
	uobj, err := runtime.DefaultUnstructuredConverter.ToUnstructured(key)
	if err != nil {
		return nil, fmt.Errorf("failed to convert ServiceAccountKey to unstructured: %w", err)
	}

	var lastErr error
	var created *unstructured.Unstructured
	for i := 0; i <= b.retries; i++ {
		created, lastErr = dyn.Resource(b.gvr).Create(ctx, &unstructured.Unstructured{Object: uobj}, *opts)
		if lastErr == nil {
			break
		}
	}
	if lastErr != nil {
		return nil, lastErr
	}

	out := new(identityv1alpha1.ServiceAccountKey)
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(created.UnstructuredContent(), out); err != nil {
		return nil, err
	}
	return out, nil
}

func (b *DynamicProvider) ListServiceAccountKeys(ctx context.Context, _ authuser.Info, opts *metav1.ListOptions) (*identityv1alpha1.ServiceAccountKeyList, error) {
	if opts == nil {
		opts = &metav1.ListOptions{}
	}
	dyn, err := b.dynForUser(ctx)
	if err != nil {
		return nil, err
	}
	var lastErr error
	var ul *unstructured.UnstructuredList
	for i := 0; i <= b.retries; i++ {
		ul, lastErr = dyn.Resource(b.gvr).List(ctx, *opts)
		if lastErr == nil {
			break
		}
	}
	if lastErr != nil {
		return nil, lastErr
	}
	out := new(identityv1alpha1.ServiceAccountKeyList)
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(ul.UnstructuredContent(), out); err != nil {
		return nil, err
	}
	return out, nil
}

func (b *DynamicProvider) GetServiceAccountKey(ctx context.Context, _ authuser.Info, name string) (*identityv1alpha1.ServiceAccountKey, error) {
	dyn, err := b.dynForUser(ctx)
	if err != nil {
		return nil, err
	}
	var lastErr error
	var uobj *unstructured.Unstructured
	for i := 0; i <= b.retries; i++ {
		uobj, lastErr = dyn.Resource(b.gvr).Get(ctx, name, metav1.GetOptions{})
		if lastErr == nil {
			break
		}
	}
	if lastErr != nil {
		return nil, lastErr
	}
	out := new(identityv1alpha1.ServiceAccountKey)
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(uobj.UnstructuredContent(), out); err != nil {
		return nil, err
	}
	return out, nil
}

func (b *DynamicProvider) DeleteServiceAccountKey(ctx context.Context, _ authuser.Info, name string) error {
	dyn, err := b.dynForUser(ctx)
	if err != nil {
		return err
	}
	var lastErr error
	for i := 0; i <= b.retries; i++ {
		lastErr = dyn.Resource(b.gvr).Delete(ctx, name, metav1.DeleteOptions{})
		if lastErr == nil {
			return nil
		}
	}
	return lastErr
}

// small util
func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

package useridentities

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

// Config controls how the provider talks to the remote useridentities API **always via a remote URL**.
//
// We no longer assume in-cluster aggregated discovery. Instead we take a
// ProviderURL and force client-go to talk to that host with the right TLS/SNI
// while still injecting X-Remote-* via WrapTransport.
//
// Notes:
// - BaseConfig is used as a template for timeouts/proxies/etc.
// - We require proper SNI (ServerName from ProviderURL).
// - If you use mTLS for the front-proxy trust, set ClientCert/Key.
//

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

type DynamicProvider struct {
	base        *rest.Config
	baseRT      http.RoundTripper // shared transport — reuses TCP connections across requests
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

	// Build the base transport once so the underlying TCP connections and TLS
	// sessions are reused across all per-user requests.
	baseRT, err := rest.TransportFor(base)
	if err != nil {
		return nil, fmt.Errorf("building useridentities provider transport: %w", err)
	}

	gvr := identityv1alpha1.SchemeGroupVersion.WithResource("useridentities")

	return &DynamicProvider{
		base:        base,
		baseRT:      baseRT,
		gvr:         gvr,
		to:          cfg.Timeout,
		retries:     max(0, cfg.Retries),
		allowExtras: cfg.ExtrasAllow,
	}, nil
}

// dynForUser creates a per-call client-go dynamic.Interface that forwards identity via X-Remote-*.
// The underlying HTTP transport is shared across calls so TCP connections are reused.
func (b *DynamicProvider) dynForUser(ctx context.Context) (dynamic.Interface, error) {
	u, ok := apirequest.UserFrom(ctx)
	if !ok || u == nil {
		return nil, fmt.Errorf("no user in context")
	}
	cfg := rest.CopyConfig(b.base)
	if b.to > 0 {
		cfg.Timeout = b.to
	}
	// Wrap the shared base transport with per-user X-Remote-* headers only.
	// This avoids building a new TLS transport on every request.
	cfg.WrapTransport = func(_ http.RoundTripper) http.RoundTripper {
		return transport.NewAuthProxyRoundTripper(
			u.GetName(),
			u.GetUID(),
			u.GetGroups(),
			b.filterExtras(u.GetExtra()),
			b.baseRT,
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

// ---- Public API ----

func (b *DynamicProvider) ListUserIdentities(ctx context.Context, _ authuser.Info, opts *metav1.ListOptions) (*identityv1alpha1.UserIdentityList, error) {
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
	out := new(identityv1alpha1.UserIdentityList)
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(ul.UnstructuredContent(), out); err != nil {
		return nil, err
	}
	return out, nil
}

func (b *DynamicProvider) GetUserIdentity(ctx context.Context, _ authuser.Info, name string) (*identityv1alpha1.UserIdentity, error) {
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
	out := new(identityv1alpha1.UserIdentity)
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(uobj.UnstructuredContent(), out); err != nil {
		return nil, err
	}
	return out, nil
}

// small util
func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

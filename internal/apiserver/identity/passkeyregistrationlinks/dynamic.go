package passkeyregistrationlinks

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

// Config controls how the provider talks to the remote passkeyregistrationlinks API
// via a remote URL. It mirrors passkeys.Config: milo reuses the passkeys provider
// settings for this resource.
//
// Retries is accepted for parity with the other identity providers but is never
// applied: see CreatePasskeyRegistrationLink.
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
	gvr         schema.GroupVersionResource
	to          time.Duration
	allowExtras map[string]struct{}
}

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

	gvr := identityv1alpha1.SchemeGroupVersion.WithResource("passkeyregistrationlinks")

	return &DynamicProvider{
		base:        base,
		gvr:         gvr,
		to:          cfg.Timeout,
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

// ---- Public API ----

// CreatePasskeyRegistrationLink sends exactly one create to the provider. It is not
// retried: a create that failed on the wire may still have minted and emailed a
// link, and a retry would send the user a second one. The provider's error is
// returned as-is so the caller sees its status code.
func (b *DynamicProvider) CreatePasskeyRegistrationLink(ctx context.Context, _ authuser.Info, link *identityv1alpha1.PasskeyRegistrationLink, opts *metav1.CreateOptions) (*identityv1alpha1.PasskeyRegistrationLink, error) {
	dyn, err := b.dynForUser(ctx)
	if err != nil {
		return nil, err
	}
	if opts == nil {
		opts = &metav1.CreateOptions{}
	}

	uobj, err := runtime.DefaultUnstructuredConverter.ToUnstructured(link)
	if err != nil {
		return nil, fmt.Errorf("failed to convert PasskeyRegistrationLink to unstructured: %w", err)
	}

	created, err := dyn.Resource(b.gvr).Create(ctx, &unstructured.Unstructured{Object: uobj}, *opts)
	if err != nil {
		return nil, err
	}

	out := new(identityv1alpha1.PasskeyRegistrationLink)
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(created.UnstructuredContent(), out); err != nil {
		return nil, err
	}
	return out, nil
}

package app

import (
	"fmt"
	"net/http"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	semconv "go.opentelemetry.io/otel/semconv/v1.17.0"
	"go.opentelemetry.io/otel/trace"
	corev1 "k8s.io/api/core/v1"
	apiextensionsapiserver "k8s.io/apiextensions-apiserver/pkg/apiserver"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apiserver/pkg/admission"
	"k8s.io/apiserver/pkg/endpoints/filterlatency"
	genericapifilters "k8s.io/apiserver/pkg/endpoints/filters"
	"k8s.io/apiserver/pkg/endpoints/request"
	genericfeatures "k8s.io/apiserver/pkg/features"
	"k8s.io/apiserver/pkg/server"
	genericfilters "k8s.io/apiserver/pkg/server/filters"
	"k8s.io/apiserver/pkg/server/routine"
	flowcontrolrequest "k8s.io/apiserver/pkg/util/flowcontrol/request"
	"k8s.io/apiserver/pkg/util/webhook"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/rest"
	"k8s.io/component-base/tracing"
	utilversion "k8s.io/component-base/version"
	"k8s.io/klog/v2"
	aggregatorapiserver "k8s.io/kube-aggregator/pkg/apiserver"
	aggregatorscheme "k8s.io/kube-aggregator/pkg/apiserver/scheme"
	"k8s.io/kubernetes/pkg/api/legacyscheme"
	"k8s.io/kubernetes/pkg/controlplane"
	controlplaneapiserver "k8s.io/kubernetes/pkg/controlplane/apiserver"
	"k8s.io/kubernetes/pkg/controlplane/apiserver/options"
	generatedopenapi "k8s.io/kubernetes/pkg/generated/openapi"
	admissionregistrationrest "k8s.io/kubernetes/pkg/registry/admissionregistration/rest"
	apiserverinternalrest "k8s.io/kubernetes/pkg/registry/apiserverinternal/rest"
	authenticationrest "k8s.io/kubernetes/pkg/registry/authentication/rest"
	authorizationrest "k8s.io/kubernetes/pkg/registry/authorization/rest"
	coordinationrest "k8s.io/kubernetes/pkg/registry/coordination/rest"
	discoveryrest "k8s.io/kubernetes/pkg/registry/discovery/rest"
	flowcontrolrest "k8s.io/kubernetes/pkg/registry/flowcontrol/rest"
	rbacrest "k8s.io/kubernetes/pkg/registry/rbac/rest"
	svmrest "k8s.io/kubernetes/pkg/registry/storagemigration/rest"

	"go.miloapis.com/milo/internal/apiserver/admission/initializer"
	eventsbackend "go.miloapis.com/milo/internal/apiserver/events"
	serviceaccountkeysbackend "go.miloapis.com/milo/internal/apiserver/identity/serviceaccountkeys"
	sessionsbackend "go.miloapis.com/milo/internal/apiserver/identity/sessions"
	useridentitiesbackend "go.miloapis.com/milo/internal/apiserver/identity/useridentities"
	identitystorage "go.miloapis.com/milo/internal/apiserver/storage/identity"
	admissionquota "go.miloapis.com/milo/internal/quota/admission"
	identityapi "go.miloapis.com/milo/pkg/apis/identity"
	identityopenapi "go.miloapis.com/milo/pkg/apis/identity/v1alpha1"
	quotaapi "go.miloapis.com/milo/pkg/apis/quota"
	"go.miloapis.com/milo/pkg/features"
	discoveryctx "go.miloapis.com/milo/pkg/server/discovery"
	datumfilters "go.miloapis.com/milo/pkg/server/filters"
	utilfeature "k8s.io/apiserver/pkg/util/feature"
	openapicommon "k8s.io/kube-openapi/pkg/common"
)

type Config struct {
	Options options.CompletedOptions

	Aggregator    *aggregatorapiserver.Config
	ControlPlane  *controlplaneapiserver.Config
	APIExtensions *apiextensionsapiserver.Config

	// DiscoveryRegistry is the shared parent-context registry used by the
	// discovery filter. Created in NewConfig, populated from CRDs by a
	// post-start hook in CreateServerChain.
	DiscoveryRegistry *discoveryctx.Registry

	ExtraConfig
}

type ExtraConfig struct {
	SessionsProvider           SessionsProviderConfig
	UserIdentitiesProvider     UserIdentitiesProviderConfig
	ServiceAccountKeysProvider ServiceAccountKeysProviderConfig
	EventsProvider             EventsProviderConfig
}

// SessionsProviderConfig groups configuration for the sessions backend provider
type SessionsProviderConfig struct {
	URL            string
	CAFile         string
	ClientCertFile string
	ClientKeyFile  string
	TimeoutSeconds int
	Retries        int
	ForwardExtras  []string
}

// UserIdentitiesProviderConfig groups configuration for the useridentities backend provider
type UserIdentitiesProviderConfig struct {
	URL            string
	CAFile         string
	ClientCertFile string
	ClientKeyFile  string
	TimeoutSeconds int
	Retries        int
	ForwardExtras  []string
}

// EventsProviderConfig groups configuration for the events backend provider
type EventsProviderConfig struct {
	URL            string
	CAFile         string
	ClientCertFile string
	ClientKeyFile  string
	TimeoutSeconds int
	Retries        int
	ForwardExtras  []string
}

// ServiceAccountKeysProviderConfig groups configuration for the serviceaccountkeys backend provider
type ServiceAccountKeysProviderConfig struct {
	URL            string
	CAFile         string
	ClientCertFile string
	ClientKeyFile  string
	TimeoutSeconds int
	Retries        int
	ForwardExtras  []string
}

type completedConfig struct {
	Options options.CompletedOptions

	Aggregator    aggregatorapiserver.CompletedConfig
	ControlPlane  controlplaneapiserver.CompletedConfig
	APIExtensions apiextensionsapiserver.CompletedConfig

	DiscoveryRegistry *discoveryctx.Registry

	ExtraConfig
}

type CompletedConfig struct {
	// Embed a private pointer that cannot be instantiated outside of this package.
	*completedConfig
}

func (c *CompletedConfig) GenericStorageProviders(discovery discovery.DiscoveryInterface) ([]controlplaneapiserver.RESTStorageProvider, error) {
	// Initialize shared events backend if EventsProxy is enabled
	// This is done once and shared between core/v1 and events.k8s.io/v1 providers
	var eventsBackend *eventsbackend.DynamicProvider
	if utilfeature.DefaultFeatureGate.Enabled(features.EventsProxy) {
		backend, err := c.initEventsBackend()
		if err != nil {
			return nil, err
		}
		eventsBackend = backend
	}

	coreProvider := c.buildCoreProvider(eventsBackend)

	providers := []controlplaneapiserver.RESTStorageProvider{
		coreProvider,
		apiserverinternalrest.StorageProvider{},
		authenticationrest.RESTStorageProvider{Authenticator: c.ControlPlane.Generic.Authentication.Authenticator, APIAudiences: c.ControlPlane.Generic.Authentication.APIAudiences},
		authorizationrest.RESTStorageProvider{Authorizer: c.ControlPlane.Generic.Authorization.Authorizer, RuleResolver: c.ControlPlane.Generic.RuleResolver},
		coordinationrest.RESTStorageProvider{},
		rbacrest.RESTStorageProvider{Authorizer: c.ControlPlane.Generic.Authorization.Authorizer},
		svmrest.RESTStorageProvider{},
		flowcontrolrest.RESTStorageProvider{InformerFactory: c.ControlPlane.Generic.SharedInformerFactory},
		admissionregistrationrest.RESTStorageProvider{Authorizer: c.ControlPlane.Generic.Authorization.Authorizer, DiscoveryClient: discovery},
		discoveryrest.StorageProvider{},
	}

	providers = append(providers, newIdentityStorageProvider(c))

	if utilfeature.DefaultFeatureGate.Enabled(features.EventsProxy) {
		providers = append(providers, newEventsV1StorageProvider(eventsBackend))
	}

	return providers, nil
}

func newIdentityStorageProvider(c *CompletedConfig) controlplaneapiserver.RESTStorageProvider {
	provider := identitystorage.StorageProvider{}

	if utilfeature.DefaultFeatureGate.Enabled(features.Sessions) {
		allow := make(map[string]struct{}, len(c.ExtraConfig.SessionsProvider.ForwardExtras))
		for _, k := range c.ExtraConfig.SessionsProvider.ForwardExtras {
			allow[k] = struct{}{}
		}
		cfg := sessionsbackend.Config{
			BaseConfig:     c.ControlPlane.Generic.LoopbackClientConfig,
			ProviderURL:    c.ExtraConfig.SessionsProvider.URL,
			CAFile:         c.ExtraConfig.SessionsProvider.CAFile,
			ClientCertFile: c.ExtraConfig.SessionsProvider.ClientCertFile,
			ClientKeyFile:  c.ExtraConfig.SessionsProvider.ClientKeyFile,
			Timeout:        time.Duration(c.ExtraConfig.SessionsProvider.TimeoutSeconds) * time.Second,
			Retries:        c.ExtraConfig.SessionsProvider.Retries,
			ExtrasAllow:    allow,
		}
		backend, _ := sessionsbackend.NewDynamicProvider(cfg)
		provider.Sessions = backend
	}

	if utilfeature.DefaultFeatureGate.Enabled(features.UserIdentities) {
		allow := make(map[string]struct{}, len(c.ExtraConfig.UserIdentitiesProvider.ForwardExtras))
		for _, k := range c.ExtraConfig.UserIdentitiesProvider.ForwardExtras {
			allow[k] = struct{}{}
		}
		cfg := useridentitiesbackend.Config{
			BaseConfig:     c.ControlPlane.Generic.LoopbackClientConfig,
			ProviderURL:    c.ExtraConfig.UserIdentitiesProvider.URL,
			CAFile:         c.ExtraConfig.UserIdentitiesProvider.CAFile,
			ClientCertFile: c.ExtraConfig.UserIdentitiesProvider.ClientCertFile,
			ClientKeyFile:  c.ExtraConfig.UserIdentitiesProvider.ClientKeyFile,
			Timeout:        time.Duration(c.ExtraConfig.UserIdentitiesProvider.TimeoutSeconds) * time.Second,
			Retries:        c.ExtraConfig.UserIdentitiesProvider.Retries,
			ExtrasAllow:    allow,
		}
		backend, _ := useridentitiesbackend.NewDynamicProvider(cfg)
		provider.UserIdentities = backend
	}

	if utilfeature.DefaultFeatureGate.Enabled(features.ServiceAccountKeys) {
		allow := make(map[string]struct{}, len(c.ExtraConfig.ServiceAccountKeysProvider.ForwardExtras))
		for _, k := range c.ExtraConfig.ServiceAccountKeysProvider.ForwardExtras {
			allow[k] = struct{}{}
		}
		cfg := serviceaccountkeysbackend.Config{
			BaseConfig:     c.ControlPlane.Generic.LoopbackClientConfig,
			ProviderURL:    c.ExtraConfig.ServiceAccountKeysProvider.URL,
			CAFile:         c.ExtraConfig.ServiceAccountKeysProvider.CAFile,
			ClientCertFile: c.ExtraConfig.ServiceAccountKeysProvider.ClientCertFile,
			ClientKeyFile:  c.ExtraConfig.ServiceAccountKeysProvider.ClientKeyFile,
			Timeout:        time.Duration(c.ExtraConfig.ServiceAccountKeysProvider.TimeoutSeconds) * time.Second,
			Retries:        c.ExtraConfig.ServiceAccountKeysProvider.Retries,
			ExtrasAllow:    allow,
		}
		backend, _ := serviceaccountkeysbackend.NewDynamicProvider(cfg)
		provider.ServiceAccountKeys = backend
	}

	return provider
}

// initEventsBackend creates a shared DynamicProvider for both core/v1 and events.k8s.io/v1 APIs
func (c *CompletedConfig) initEventsBackend() (*eventsbackend.DynamicProvider, error) {
	allow := make(map[string]struct{}, len(c.ExtraConfig.EventsProvider.ForwardExtras))
	for _, k := range c.ExtraConfig.EventsProvider.ForwardExtras {
		allow[k] = struct{}{}
	}

	cfg := eventsbackend.Config{
		BaseConfig:     c.ControlPlane.Generic.LoopbackClientConfig,
		ProviderURL:    c.ExtraConfig.EventsProvider.URL,
		CAFile:         c.ExtraConfig.EventsProvider.CAFile,
		ClientCertFile: c.ExtraConfig.EventsProvider.ClientCertFile,
		ClientKeyFile:  c.ExtraConfig.EventsProvider.ClientKeyFile,
		Timeout:        time.Duration(c.ExtraConfig.EventsProvider.TimeoutSeconds) * time.Second,
		Retries:        c.ExtraConfig.EventsProvider.Retries,
		ExtrasAllow:    allow,
	}

	backend, err := eventsbackend.NewDynamicProvider(cfg)
	if err != nil {
		klog.ErrorS(err, "Failed to initialize events proxy (EventsProxy feature gate is enabled)",
			"providerURL", cfg.ProviderURL,
			"caFile", cfg.CAFile,
			"clientCertFile", cfg.ClientCertFile,
			"clientKeyFile", cfg.ClientKeyFile)
		return nil, fmt.Errorf("failed to initialize events proxy: %w", err)
	}

	klog.InfoS("Events proxy initialized successfully (shared backend for core/v1 and events.k8s.io/v1)",
		"providerURL", cfg.ProviderURL)

	return backend, nil
}

func newEventsV1StorageProvider(backend *eventsbackend.DynamicProvider) controlplaneapiserver.RESTStorageProvider {
	return &eventsbackend.EventsV1StorageProvider{
		Backend: backend,
	}
}

// buildCoreProvider creates the core storage provider with optional events proxy support.
// If eventsBackend is provided (non-nil), events storage will be proxied through it.
func (c *CompletedConfig) buildCoreProvider(eventsBackend *eventsbackend.DynamicProvider) controlplaneapiserver.RESTStorageProvider {
	coreProvider := c.ControlPlane.NewCoreGenericConfig()

	if eventsBackend == nil {
		return coreProvider
	}

	return eventsbackend.WrapCoreProvider(coreProvider, eventsBackend)
}

func (c *Config) Complete() (CompletedConfig, error) {
	return CompletedConfig{&completedConfig{
		Options: c.Options,

		Aggregator:    c.Aggregator.Complete(),
		ControlPlane:  c.ControlPlane.Complete(),
		APIExtensions: c.APIExtensions.Complete(),

		DiscoveryRegistry: c.DiscoveryRegistry,

		ExtraConfig: c.ExtraConfig,
	}}, nil
}

func NewConfig(opts options.CompletedOptions) (*Config, error) {
	var registry *discoveryctx.Registry
	if utilfeature.DefaultFeatureGate.Enabled(features.DiscoveryContextFilter) {
		registry = discoveryctx.NewRegistry()
	}

	c := &Config{
		Options:           opts,
		DiscoveryRegistry: registry,
	}

	miloScheme := runtime.NewScheme()
	identityapi.Install(miloScheme)
	identityapi.Install(legacyscheme.Scheme)
	quotaapi.Install(miloScheme)
	quotaapi.Install(legacyscheme.Scheme)

	apiResourceConfigSource := controlplane.DefaultAPIResourceConfigSource()
	apiResourceConfigSource.DisableResources(corev1.SchemeGroupVersion.WithResource("serviceaccounts"))
	apiResourceConfigSource.DisableResources(corev1.SchemeGroupVersion.WithResource("resourcequotas"))

	if utilfeature.DefaultFeatureGate.Enabled(features.EventsProxy) {
		apiResourceConfigSource.DisableResources(corev1.SchemeGroupVersion.WithResource("events"))
	}

	apiResourceConfigSource.EnableVersions(identityopenapi.SchemeGroupVersion)

	genericConfig, versionedInformers, storageFactory, err := controlplaneapiserver.BuildGenericConfig(
		opts,
		[]*runtime.Scheme{legacyscheme.Scheme, apiextensionsapiserver.Scheme, aggregatorscheme.Scheme, miloScheme},
		apiResourceConfigSource,
		func(ref openapicommon.ReferenceCallback) map[string]openapicommon.OpenAPIDefinition {
			base := generatedopenapi.GetOpenAPIDefinitions(ref)
			id := identityopenapi.GetOpenAPIDefinitions(ref)
			for k, v := range id {
				base[k] = v
			}
			return base
		},
	)
	if err != nil {
		return nil, err
	}

	loopbackClientConfig := genericConfig.LoopbackClientConfig

	genericConfig.BuildHandlerChainFunc = func(h http.Handler, c *server.Config) http.Handler {
		if registry != nil {
			h = discoveryctx.DiscoveryContextFilter(h, registry)
		}
		return datumfilters.ProjectRouterWithRequestInfo(
			DefaultBuildHandlerChain(h, c, loopbackClientConfig), // build stock chain first
			c.RequestInfoResolver,                                // then wrap with router
		)
	}

	serviceResolver := webhook.NewDefaultServiceResolver()

	loopbackInit := initializer.LoopbackInitializer{
		Loopback: rest.CopyConfig(genericConfig.LoopbackClientConfig),
	}

	kubeAPIs, upstreamInits, err := controlplaneapiserver.CreateConfig(opts, genericConfig, versionedInformers, storageFactory, serviceResolver, []admission.PluginInitializer{loopbackInit})
	if err != nil {
		return nil, err
	}
	c.ControlPlane = kubeAPIs
	c.ControlPlane.Generic.EffectiveVersion = utilversion.DefaultKubeEffectiveVersion()

	if kubeAPIs.Generic.LoopbackClientConfig != nil && kubeAPIs.Generic.TracerProvider != nil {
		kubeAPIs.Generic.LoopbackClientConfig.Wrap(tracing.WrapperFor(kubeAPIs.Generic.TracerProvider))
	}

	combinedInits := append(upstreamInits, loopbackInit)

	authInfoResolver := webhook.NewDefaultAuthenticationInfoResolverWrapper(kubeAPIs.ProxyTransport, kubeAPIs.Generic.EgressSelector, kubeAPIs.Generic.LoopbackClientConfig, kubeAPIs.Generic.TracerProvider)
	apiExtensions, err := controlplaneapiserver.CreateAPIExtensionsConfig(*kubeAPIs.Generic, kubeAPIs.VersionedInformers, combinedInits, opts, 3, serviceResolver, authInfoResolver)
	if err != nil {
		return nil, err
	}
	apiExtensions.GenericConfig.BuildHandlerChainFunc = func(h http.Handler, c *server.Config) http.Handler {
		if registry != nil {
			h = discoveryctx.DiscoveryContextFilter(h, registry)
		}
		return datumfilters.ProjectRouterWithRequestInfo(
			DefaultBuildHandlerChain(h, c, loopbackClientConfig),
			c.RequestInfoResolver,
		)
	}
	c.APIExtensions = apiExtensions

	// Add readiness check for quota validator to ensure cache is synced before serving traffic
	kubeAPIs.Generic.AddReadyzChecks(admissionquota.ReadinessCheck())

	// Add post-start hook to bootstrap CRDs from embedded filesystem
	// This installs all CRDs EXCEPT infrastructure.miloapis.com group, which should remain in the infrastructure cluster
	kubeAPIs.Generic.AddPostStartHookOrDie("bootstrap-crds", func(ctx server.PostStartHookContext) error {
		return bootstrapCRDsHook(ctx, kubeAPIs.Generic.LoopbackClientConfig)
	})

	c.APIExtensions.GenericConfig.DisabledPostStartHooks.Insert("start-legacy-token-tracking-controller")

	aggregator, err := controlplaneapiserver.CreateAggregatorConfig(*kubeAPIs.Generic, opts, kubeAPIs.VersionedInformers, serviceResolver, kubeAPIs.ProxyTransport, kubeAPIs.Extra.PeerProxy, combinedInits)
	if err != nil {
		return nil, err
	}
	aggregator.GenericConfig.BuildHandlerChainFunc = func(h http.Handler, c *server.Config) http.Handler {
		if registry != nil {
			h = discoveryctx.DiscoveryContextFilter(h, registry)
		}
		return datumfilters.ProjectRouterWithRequestInfo(
			DefaultBuildHandlerChain(h, c, loopbackClientConfig),
			c.RequestInfoResolver,
		)
	}
	c.Aggregator = aggregator
	c.Aggregator.ExtraConfig.DisableRemoteAvailableConditionController = true
	c.Aggregator.GenericConfig.EffectiveVersion = utilversion.DefaultKubeEffectiveVersion()

	return c, nil
}

// DefaultBuildHandlerChain builds the standard Kubernetes filter chain with Milo-specific filters
func DefaultBuildHandlerChain(apiHandler http.Handler, c *server.Config, loopbackConfig *rest.Config) http.Handler {
	handler := apiHandler

	handler = filterlatency.TrackCompleted(handler)
	handler = genericapifilters.WithAuthorization(handler, c.Authorization.Authorizer, c.Serializer)
	handler = filterlatency.TrackStarted(handler, c.TracerProvider, "authorization")

	if c.FlowControl != nil {
		workEstimatorCfg := flowcontrolrequest.DefaultWorkEstimatorConfig()
		requestWorkEstimator := flowcontrolrequest.NewWorkEstimator(
			c.StorageObjectCountTracker.Get, c.FlowControl.GetInterestedWatchCount, workEstimatorCfg, c.FlowControl.GetMaxSeats)
		handler = filterlatency.TrackCompleted(handler)
		handler = genericfilters.WithPriorityAndFairness(handler, c.LongRunningFunc, c.FlowControl, requestWorkEstimator, c.RequestTimeout/4)
		handler = filterlatency.TrackStarted(handler, c.TracerProvider, "priorityandfairness")
	} else {
		handler = genericfilters.WithMaxInFlightLimit(handler, c.MaxRequestsInFlight, c.MaxMutatingRequestsInFlight, c.LongRunningFunc)
	}

	handler = filterlatency.TrackCompleted(handler)
	handler = genericapifilters.WithAudit(handler, c.AuditBackend, c.AuditPolicyRuleEvaluator, c.LongRunningFunc)
	handler = filterlatency.TrackStarted(handler, c.TracerProvider, "audit")

	handler = datumfilters.AuditScopeAnnotationDecorator(handler)

	handler = datumfilters.UserContextAuthorizationDecorator(handler)
	handler = datumfilters.OrganizationContextAuthorizationDecorator(handler)
	handler = datumfilters.ProjectContextAuthorizationDecorator(handler)

	failedHandler := genericapifilters.Unauthorized(c.Serializer)
	failedHandler = genericapifilters.WithFailedAuthenticationAudit(failedHandler, c.AuditBackend, c.AuditPolicyRuleEvaluator)

	handler = filterlatency.TrackCompleted(handler)
	handler = genericapifilters.WithImpersonation(handler, c.Authorization.Authorizer, c.Serializer)
	handler = filterlatency.TrackStarted(handler, c.TracerProvider, "impersonation")

	failedHandler = filterlatency.TrackCompleted(failedHandler)
	handler = filterlatency.TrackCompleted(handler)
	handler = genericapifilters.WithAuthentication(handler, c.Authentication.Authenticator, failedHandler, c.Authentication.APIAudiences, c.Authentication.RequestHeaderConfig)
	handler = filterlatency.TrackStarted(handler, c.TracerProvider, "authentication")

	handler = genericfilters.WithCORS(handler, c.CorsAllowedOriginList, nil, nil, nil, "true")

	// WithWarningRecorder must be wrapped by the timeout handler
	// to make the addition of warning headers threadsafe
	handler = genericapifilters.WithWarningRecorder(handler)

	// WithTimeoutForNonLongRunningRequests will call the rest of the request handling in a go-routine with the
	// context with deadline. The go-routine can keep running, while the timeout logic will return a timeout to the client.
	handler = genericfilters.WithTimeoutForNonLongRunningRequests(handler, c.LongRunningFunc)

	handler = genericapifilters.WithRequestDeadline(handler, c.AuditBackend, c.AuditPolicyRuleEvaluator,
		c.LongRunningFunc, c.Serializer, c.RequestTimeout)
	handler = genericfilters.WithWaitGroup(handler, c.LongRunningFunc, c.NonLongRunningRequestWaitGroup)
	// if c.ShutdownWatchTerminationGracePeriod > 0 {
	// 	handler = genericfilters.WithWatchTerminationDuringShutdown(handler, c.lifecycleSignals, c.WatchRequestWaitGroup)
	// }
	if c.SecureServing != nil && !c.SecureServing.DisableHTTP2 && c.GoawayChance > 0 {
		handler = genericfilters.WithProbabilisticGoaway(handler, c.GoawayChance)
	}
	handler = genericapifilters.WithCacheControl(handler)
	handler = genericfilters.WithHSTS(handler, c.HSTSDirectives)
	// if c.ShutdownSendRetryAfter {
	// 	handler = genericfilters.WithRetryAfter(handler, c.lifecycleSignals.NotAcceptingNewRequest.Signaled())
	// }
	handler = genericfilters.WithHTTPLogging(handler)
	if c.FeatureGate.Enabled(genericfeatures.APIServerTracing) {
		handler = withCustomTracing(handler, c.TracerProvider)
	}
	handler = genericapifilters.WithLatencyTrackers(handler)
	if c.FeatureGate.Enabled(genericfeatures.APIServingWithRoutine) {
		handler = routine.WithRoutine(handler, c.LongRunningFunc)
	}

	handler = datumfilters.OrganizationProjectListConstraintDecorator(handler)
	handler = datumfilters.UserOrganizationMembershipListConstraintDecorator(handler)
	handler = datumfilters.UserUserInvitationListConstraintDecorator(handler)
	handler = datumfilters.UserContactListConstraintDecorator(handler)
	handler = datumfilters.UserContactGroupMembershipListConstraintDecorator(handler)
	handler = datumfilters.UserContactGroupMembershipRemovalListConstraintDecorator(handler)
	handler = datumfilters.ContactGroupVisibilityWithoutPrivateDecorator(handler)
	handler = genericapifilters.WithRequestInfo(handler, c.RequestInfoResolver)
	handler = genericapifilters.WithRequestReceivedTimestamp(handler)
	// handler = genericapifilters.WithMuxAndDiscoveryComplete(handler, c.lifecycleSignals.MuxAndDiscoveryComplete.Signaled())
	handler = genericfilters.WithPanicRecovery(handler, c.RequestInfoResolver)
	handler = genericapifilters.WithAuditInit(handler)

	handler = datumfilters.OrganizationContextHandler(handler, c.Serializer)
	handler = datumfilters.UserContextHandler(handler, c.Serializer)

	return handler
}

// withCustomTracing provides tracing that always creates child spans for proper trace hierarchy
func withCustomTracing(handler http.Handler, tp trace.TracerProvider) http.Handler {
	opts := []otelhttp.Option{
		otelhttp.WithPropagators(tracing.Propagators()),
		otelhttp.WithTracerProvider(tp),
		otelhttp.WithServerName("milo-apiserver"),
		otelhttp.WithSpanNameFormatter(func(operation string, r *http.Request) string {
			ctx := r.Context()
			info, exist := request.RequestInfoFrom(ctx)
			if !exist || !info.IsResourceRequest {
				return r.Method
			}
			return getSpanNameFromRequestInfo(info, r)
		}),
	}

	wrappedHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL != nil {
			trace.SpanFromContext(r.Context()).SetAttributes(semconv.HTTPTarget(r.URL.RequestURI()))
		}
		handler.ServeHTTP(w, r)
	})

	return otelhttp.NewHandler(wrappedHandler, "MiloAPI", opts...)
}

// getSpanNameFromRequestInfo creates span names from Kubernetes request info
func getSpanNameFromRequestInfo(info *request.RequestInfo, r *http.Request) string {
	spanName := "/" + info.APIPrefix
	if info.APIGroup != "" {
		spanName += "/" + info.APIGroup
	}
	spanName += "/" + info.APIVersion
	if info.Namespace != "" {
		spanName += "/namespaces/{:namespace}"
	}
	spanName += "/" + info.Resource
	if info.Name != "" {
		spanName += "/" + "{:name}"
	}
	if info.Subresource != "" {
		spanName += "/" + info.Subresource
	}
	return r.Method + " " + spanName
}

package app

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"
	crd "go.miloapis.com/milo/config/crd"
	"go.miloapis.com/milo/internal/apiserver/admission/plugin/namespace/lifecycle"
	projectstorage "go.miloapis.com/milo/internal/apiserver/storage/project"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apiextensionsclient "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1" // ← add / keep this
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/apiserver/pkg/admission"
	genericapifilters "k8s.io/apiserver/pkg/endpoints/filters"
	genericapiserver "k8s.io/apiserver/pkg/server"
	utilfeature "k8s.io/apiserver/pkg/util/feature"
	"k8s.io/apiserver/pkg/util/notfoundhandler"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	cliflag "k8s.io/component-base/cli/flag"
	"k8s.io/component-base/cli/globalflag"
	"k8s.io/component-base/featuregate"
	"k8s.io/component-base/logs"
	logsapi "k8s.io/component-base/logs/api/v1"
	_ "k8s.io/component-base/metrics/prometheus/workqueue"
	"k8s.io/component-base/term"
	"k8s.io/component-base/version"
	utilversion "k8s.io/component-base/version"
	"k8s.io/component-base/version/verflag"
	"k8s.io/klog/v2"
	aggregatorapiserver "k8s.io/kube-aggregator/pkg/apiserver"

	// Register JSON logging format
	_ "k8s.io/component-base/logs/json/register"
	controlplaneapiserver "k8s.io/kubernetes/pkg/controlplane/apiserver"
	"k8s.io/kubernetes/pkg/controlplane/apiserver/options"

	admissionquota "go.miloapis.com/milo/internal/quota/admission"

	// Import features package to register Milo feature gates via init()
	_ "go.miloapis.com/milo/pkg/features"
)

func init() {
	utilruntime.Must(logsapi.AddFeatureGates(utilfeature.DefaultMutableFeatureGate))
	utilfeature.DefaultMutableFeatureGate.Set("LoggingBetaOptions=true")
	utilfeature.DefaultMutableFeatureGate.Set("RemoteRequestHeaderUID=true")
}

var (
	SystemNamespace                      string
	sessionsProviderURL                  string
	sessionsProviderCAFile               string
	sessionsProviderClientCert           string
	sessionsProviderClientKey            string
	providerTimeoutSeconds               int
	providerRetries                      int
	forwardExtras                        []string
	userIdentitiesProviderURL            string
	userIdentitiesProviderCAFile         string
	userIdentitiesProviderClientCert     string
	userIdentitiesProviderClientKey      string
	serviceAccountKeysProviderURL        string
	serviceAccountKeysProviderCAFile     string
	serviceAccountKeysProviderClientCert string
	serviceAccountKeysProviderClientKey  string
	eventsProviderURL                    string
	eventsProviderCAFile                 string
	eventsProviderClientCert             string
	eventsProviderClientKey              string
	eventsProviderTimeoutSeconds         int
	eventsProviderRetries                int
	eventsForwardExtras                  []string
)

// NewCommand creates a *cobra.Command object with default parameters
func NewCommand() *cobra.Command {
	s := NewOptions()
	var namedFlagSets cliflag.NamedFlagSets

	cmd := &cobra.Command{
		Use:   "apiserver",
		Short: "Extendable API server",
		Long:  `The Milo API server provides an extensible API platform.`,

		SilenceUsage: true,
		PersistentPreRunE: func(*cobra.Command, []string) error {
			rest.SetDefaultWarningHandler(rest.NoWarnings{})
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			verflag.PrintAndExitIfRequested()
			fs := cmd.Flags()

			if err := logsapi.ValidateAndApply(s.Logs, utilfeature.DefaultFeatureGate); err != nil {
				return err
			}
			cliflag.PrintFlags(fs)
			s.SystemNamespaces = []string{metav1.NamespaceSystem, metav1.NamespaceDefault, SystemNamespace}

			completedOptions, err := s.Complete(cmd.Context(), namedFlagSets, []string{}, []net.IP{})
			if err != nil {
				return err
			}

			utilfeature.DefaultMutableFeatureGate.Set("APIServerTracing=true")

			if errs := completedOptions.Validate(); len(errs) != 0 {
				return utilerrors.NewAggregate(errs)
			}

			utilfeature.DefaultMutableFeatureGate.AddMetrics()

			ctx := genericapiserver.SetupSignalContext()
			return Run(ctx, completedOptions)
		},
		Args: func(cmd *cobra.Command, args []string) error {
			for _, arg := range args {
				if len(arg) > 0 {
					return fmt.Errorf("%q does not take any arguments, got %q", cmd.CommandPath(), args)
				}
			}
			return nil
		},
	}

	s.GenericServerRunOptions.ComponentGlobalsRegistry = featuregate.NewComponentGlobalsRegistry()
	s.GenericServerRunOptions.ComponentGlobalsRegistry.ComponentGlobalsOrRegister(
		featuregate.DefaultKubeComponent, utilversion.DefaultKubeEffectiveVersion(), utilfeature.DefaultMutableFeatureGate)
	s.GenericServerRunOptions.AddUniversalFlags(namedFlagSets.FlagSet("generic"))
	s.Etcd.AddFlags(namedFlagSets.FlagSet("etcd"))
	s.SecureServing.AddFlags(namedFlagSets.FlagSet("secure serving"))
	s.Audit.AddFlags(namedFlagSets.FlagSet("auditing"))
	s.Features.AddFlags(namedFlagSets.FlagSet("features"))
	s.Authentication.AddFlags(namedFlagSets.FlagSet("authentication"))
	s.Authorization.AddFlags(namedFlagSets.FlagSet("authorization"))
	s.Metrics.AddFlags(namedFlagSets.FlagSet("metrics"))
	logsapi.AddFlags(s.Logs, namedFlagSets.FlagSet("logs"))
	s.Traces.AddFlags(namedFlagSets.FlagSet("traces"))

	miscfs := namedFlagSets.FlagSet("misc")
	miscfs.DurationVar(&s.EventTTL, "event-ttl", s.EventTTL,
		"Amount of time to retain events.")
	miscfs.StringVar(&s.ProxyClientCertFile, "proxy-client-cert-file", s.ProxyClientCertFile,
		"Client certificate used to prove the identity of the aggregator or kube-apiserver "+
			"when it must call out during a request. This includes proxying requests to a user "+
			"api-server and calling out to webhook admission plugins. It is expected that this "+
			"cert includes a signature from the CA in the --requestheader-client-ca-file flag. "+
			"That CA is published in the 'extension-apiserver-authentication' configmap in "+
			"the kube-system namespace. Components receiving calls from kube-aggregator should "+
			"use that CA to perform their half of the mutual TLS verification.")
	miscfs.StringVar(&s.ProxyClientKeyFile, "proxy-client-key-file", s.ProxyClientKeyFile,
		"Private key for the client certificate used to prove the identity of the aggregator or kube-apiserver "+
			"when it must call out during a request. This includes proxying requests to a user "+
			"api-server and calling out to webhook admission plugins.")

	verflag.AddFlags(namedFlagSets.FlagSet("global"))
	globalflag.AddGlobalFlags(namedFlagSets.FlagSet("global"), cmd.Name(), logs.SkipLoggingConfigurationFlags())

	fs := cmd.Flags()
	for _, f := range namedFlagSets.FlagSets {
		fs.AddFlagSet(f)
	}

	fs.StringVar(&s.ServiceAccountSigningKeyFile, "service-account-signing-key-file", s.ServiceAccountSigningKeyFile, ""+
		"Path to the file that contains the current private key of the service account token issuer. The issuer will sign issued ID tokens with this private key.")

	fs.StringVar(&s.ServiceAccountSigningEndpoint, "service-account-signing-endpoint", s.ServiceAccountSigningEndpoint, ""+
		"Path to socket where a external JWT signer is listening. This flag is mutually exclusive with --service-account-signing-key-file and --service-account-key-file. Requires enabling feature gate (ExternalServiceAccountTokenSigner)")

	fs.StringVar(&SystemNamespace, "system-namespace", "milo-system", "The namespace to use for system components and resources that are automatically created to run the system.")
	fs.StringVar(&sessionsProviderURL, "sessions-provider-url", "", "Direct provider base URL (e.g., https://zitadel-apiserver:8443)")
	fs.StringVar(&sessionsProviderCAFile, "sessions-provider-ca-file", "", "Path to CA file to validate provider TLS")
	fs.StringVar(&sessionsProviderClientCert, "sessions-provider-client-cert", "", "Client certificate for mTLS to provider")
	fs.StringVar(&sessionsProviderClientKey, "sessions-provider-client-key", "", "Client private key for mTLS to provider")
	fs.IntVar(&providerTimeoutSeconds, "provider-timeout", 3, "Provider request timeout in seconds")
	fs.IntVar(&providerRetries, "provider-retries", 2, "Provider request retries")
	fs.StringSliceVar(&forwardExtras, "forward-extras", []string{"iam.miloapis.com/parent-api-group", "iam.miloapis.com/parent-type", "iam.miloapis.com/parent-name"}, "User extras keys to forward during impersonation")
	fs.StringVar(&userIdentitiesProviderURL, "useridentities-provider-url", "", "Direct provider base URL for useridentities (e.g., https://zitadel-apiserver:8443)")
	fs.StringVar(&userIdentitiesProviderCAFile, "useridentities-provider-ca-file", "", "Path to CA file to validate useridentities provider TLS")
	fs.StringVar(&userIdentitiesProviderClientCert, "useridentities-provider-client-cert", "", "Client certificate for mTLS to useridentities provider")
	fs.StringVar(&userIdentitiesProviderClientKey, "useridentities-provider-client-key", "", "Client private key for mTLS to useridentities provider")
	fs.StringVar(&serviceAccountKeysProviderURL, "serviceaccountkeys-provider-url", "", "Direct provider base URL for serviceaccountkeys (e.g., https://zitadel-apiserver:8443)")
	fs.StringVar(&serviceAccountKeysProviderCAFile, "serviceaccountkeys-provider-ca-file", "", "Path to CA file to validate serviceaccountkeys provider TLS")
	fs.StringVar(&serviceAccountKeysProviderClientCert, "serviceaccountkeys-provider-client-cert", "", "Client certificate for mTLS to serviceaccountkeys provider")
	fs.StringVar(&serviceAccountKeysProviderClientKey, "serviceaccountkeys-provider-client-key", "", "Client private key for mTLS to serviceaccountkeys provider")
	fs.StringVar(&eventsProviderURL, "events-provider-url", "", "Activity API server URL for events storage (e.g., https://activity-apiserver.activity-system.svc:443)")
	fs.StringVar(&eventsProviderCAFile, "events-provider-ca-file", "", "Path to CA file to validate Activity provider TLS")
	fs.StringVar(&eventsProviderClientCert, "events-provider-client-cert", "", "Client certificate for mTLS to Activity provider")
	fs.StringVar(&eventsProviderClientKey, "events-provider-client-key", "", "Client private key for mTLS to Activity provider")
	fs.IntVar(&eventsProviderTimeoutSeconds, "events-provider-timeout", 30, "Activity provider request timeout in seconds")
	fs.IntVar(&eventsProviderRetries, "events-provider-retries", 3, "Activity provider request retries")
	fs.StringSliceVar(&eventsForwardExtras, "events-forward-extras", []string{"iam.miloapis.com/parent-api-group", "iam.miloapis.com/parent-type", "iam.miloapis.com/parent-name"}, "User extras keys to forward to Activity for events")

	cols, _, _ := term.TerminalSize(cmd.OutOrStdout())
	cliflag.SetUsageAndHelpFunc(cmd, namedFlagSets, cols)

	return cmd
}

func NewOptions() *options.Options {
	s := options.NewOptions()

	if s.Admission.GenericAdmission.Plugins == nil {
		s.Admission.GenericAdmission.Plugins = admission.NewPlugins()
	}

	admissionquota.Register(s.Admission.GenericAdmission.Plugins)

	s.Admission.GenericAdmission.RecommendedPluginOrder = GetMiloOrderedPlugins()

	s.Admission.GenericAdmission.DefaultOffPlugins = DefaultOffAdmissionPlugins()

	lifecycle.Register(s.Admission.GenericAdmission.Plugins)

	s.Admission.GenericAdmission.RecommendedPluginOrder =
		append(s.Admission.GenericAdmission.RecommendedPluginOrder, lifecycle.PluginName)
	s.Admission.GenericAdmission.EnablePlugins = append(s.Admission.GenericAdmission.EnablePlugins, lifecycle.PluginName)

	wd, _ := os.Getwd()
	s.SecureServing.ServerCert.CertDirectory = filepath.Join(wd, ".sample-minimal-controlplane")

	s.Authentication.ServiceAccounts.OptionalTokenGetter = genericTokenGetter
	s.ServiceAccountIssuer = &jwtTokenGenerator{}

	return s
}

// Run runs the specified APIServer
func Run(ctx context.Context, opts options.CompletedOptions) error {
	klog.Infof("Version: %+v", version.Get())

	klog.InfoS("Golang settings", "GOGC", os.Getenv("GOGC"), "GOMAXPROCS", os.Getenv("GOMAXPROCS"), "GOTRACEBACK", os.Getenv("GOTRACEBACK"))

	config, err := NewConfig(opts)
	if err != nil {
		return err
	}

	config.ExtraConfig.SessionsProvider.URL = sessionsProviderURL
	config.ExtraConfig.SessionsProvider.CAFile = sessionsProviderCAFile
	config.ExtraConfig.SessionsProvider.ClientCertFile = sessionsProviderClientCert
	config.ExtraConfig.SessionsProvider.ClientKeyFile = sessionsProviderClientKey
	config.ExtraConfig.SessionsProvider.TimeoutSeconds = providerTimeoutSeconds
	config.ExtraConfig.SessionsProvider.Retries = providerRetries
	config.ExtraConfig.SessionsProvider.ForwardExtras = forwardExtras

	config.ExtraConfig.UserIdentitiesProvider.URL = userIdentitiesProviderURL
	config.ExtraConfig.UserIdentitiesProvider.CAFile = userIdentitiesProviderCAFile
	config.ExtraConfig.UserIdentitiesProvider.ClientCertFile = userIdentitiesProviderClientCert
	config.ExtraConfig.UserIdentitiesProvider.ClientKeyFile = userIdentitiesProviderClientKey
	config.ExtraConfig.UserIdentitiesProvider.TimeoutSeconds = providerTimeoutSeconds
	config.ExtraConfig.UserIdentitiesProvider.Retries = providerRetries
	config.ExtraConfig.UserIdentitiesProvider.ForwardExtras = forwardExtras

	config.ExtraConfig.ServiceAccountKeysProvider.URL = serviceAccountKeysProviderURL
	config.ExtraConfig.ServiceAccountKeysProvider.CAFile = serviceAccountKeysProviderCAFile
	config.ExtraConfig.ServiceAccountKeysProvider.ClientCertFile = serviceAccountKeysProviderClientCert
	config.ExtraConfig.ServiceAccountKeysProvider.ClientKeyFile = serviceAccountKeysProviderClientKey
	config.ExtraConfig.ServiceAccountKeysProvider.TimeoutSeconds = providerTimeoutSeconds
	config.ExtraConfig.ServiceAccountKeysProvider.Retries = providerRetries
	config.ExtraConfig.ServiceAccountKeysProvider.ForwardExtras = forwardExtras

	config.ExtraConfig.EventsProvider.URL = eventsProviderURL
	config.ExtraConfig.EventsProvider.CAFile = eventsProviderCAFile
	config.ExtraConfig.EventsProvider.ClientCertFile = eventsProviderClientCert
	config.ExtraConfig.EventsProvider.ClientKeyFile = eventsProviderClientKey
	config.ExtraConfig.EventsProvider.TimeoutSeconds = eventsProviderTimeoutSeconds
	config.ExtraConfig.EventsProvider.Retries = eventsProviderRetries
	config.ExtraConfig.EventsProvider.ForwardExtras = eventsForwardExtras

	completed, err := config.Complete()
	if err != nil {
		return err
	}

	server, err := CreateServerChain(completed)
	if err != nil {
		return err
	}

	prepared, err := server.PrepareRun()
	if err != nil {
		return err
	}

	return prepared.Run(ctx)
}

// CreateServerChain creates the apiservers connected via delegation
func CreateServerChain(config CompletedConfig) (*aggregatorapiserver.APIAggregator, error) {
	notFoundHandler := notfoundhandler.New(config.ControlPlane.Generic.Serializer, genericapifilters.NoMuxAndDiscoveryIncompleteKey)

	loopbackConfig := config.ControlPlane.Generic.LoopbackClientConfig

	config.APIExtensions.GenericConfig.RESTOptionsGetter =
		projectstorage.WithProjectAwareDecoratorAndConfig(config.APIExtensions.GenericConfig.RESTOptionsGetter, loopbackConfig)

	config.APIExtensions.ExtraConfig.CRDRESTOptionsGetter =
		projectstorage.WithProjectAwareDecoratorAndConfig(config.APIExtensions.ExtraConfig.CRDRESTOptionsGetter, loopbackConfig)

	apiExtensionsServer, err := config.APIExtensions.New(genericapiserver.NewEmptyDelegateWithCustomHandler(notFoundHandler))
	if err != nil {
		return nil, fmt.Errorf("failed to create apiextensions-apiserver: %w", err)
	}
	crdAPIEnabled := config.APIExtensions.GenericConfig.MergedResourceConfig.ResourceEnabled(apiextensionsv1.SchemeGroupVersion.WithResource("customresourcedefinitions"))

	// Populate the discovery context registry from CRD annotations. The
	// apiextensions informer factory is shared across the apiserver, so we
	// reuse it rather than spinning up a parallel informer.
	if reg := config.DiscoveryRegistry; reg != nil {
		informers := apiExtensionsServer.Informers
		apiExtensionsServer.GenericAPIServer.AddPostStartHookOrDie("milo-discovery-context-registry", func(hookCtx genericapiserver.PostStartHookContext) error {
			go func() {
				if err := reg.Run(hookCtx, informers); err != nil {
					klog.ErrorS(err, "discovery context registry stopped")
				}
			}()
			return nil
		})
	}

	nativeAPIs, err := config.ControlPlane.New("datum-apiserver", apiExtensionsServer.GenericAPIServer)
	if err != nil {
		return nil, fmt.Errorf("failed to create datum controlplane apiserver: %w", err)
	}
	client, err := kubernetes.NewForConfig(config.ControlPlane.Generic.LoopbackClientConfig)
	if err != nil {
		return nil, err
	}
	storageProviders, err := config.GenericStorageProviders(client.Discovery())
	if err != nil {
		return nil, fmt.Errorf("failed to create storage providers: %w", err)
	}

	wrapped := make([]controlplaneapiserver.RESTStorageProvider, 0, len(storageProviders))
	for _, p := range storageProviders {
		wrapped = append(wrapped, projectstorage.WrapProvider(p))
	}

	if err := nativeAPIs.InstallAPIs(wrapped...); err != nil {
		return nil, fmt.Errorf("failed to install APIs: %w", err)
	}

	aggregatorServer, err := controlplaneapiserver.CreateAggregatorServer(
		config.Aggregator,
		nativeAPIs.GenericAPIServer,
		apiExtensionsServer.Informers.Apiextensions().V1().CustomResourceDefinitions(),
		crdAPIEnabled,
		controlplaneapiserver.DefaultGenericAPIServicePriorities(),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create kube-aggregator: %w", err)
	}

	return aggregatorServer, nil
}

// bootstrapCRDsHook installs all embedded CRDs into the cluster as a post-start hook
func bootstrapCRDsHook(hookCtx genericapiserver.PostStartHookContext, loopbackConfig *rest.Config) error {
	logger := klog.FromContext(hookCtx).WithName("bootstrap-crds")

	extClient, err := apiextensionsclient.NewForConfig(loopbackConfig)
	if err != nil {
		return fmt.Errorf("failed to create apiextensions client: %w", err)
	}

	logger.Info("Starting CRD bootstrap from embedded filesystem")

	err = wait.PollUntilContextCancel(hookCtx, time.Second, true, func(ctx context.Context) (bool, error) {
		if err := crd.Bootstrap(ctx, extClient); err != nil {
			logger.Error(err, "failed to bootstrap CRDs, retrying")
			return false, nil
		}
		return true, nil
	})

	if err != nil {
		logger.Error(err, "failed to bootstrap CRDs")
		return nil
	}

	logger.Info("CRD bootstrap completed successfully")
	return nil
}

// SPDX-License-Identifier: AGPL-3.0-only
package app

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/util/wait"
	cacheddiscovery "k8s.io/client-go/discovery/cached/memory"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/restmapper"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/controller-manager/pkg/clientbuilder"
	"k8s.io/klog/v2"
	controllerruntime "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"

	iamv1alpha1webhook "go.miloapis.com/milo/internal/webhooks/iam/v1alpha1"
	identityv1alpha1webhook "go.miloapis.com/milo/internal/webhooks/identity/v1alpha1"
	notesv1alpha1webhook "go.miloapis.com/milo/internal/webhooks/notes/v1alpha1"
	notificationv1alpha1webhook "go.miloapis.com/milo/internal/webhooks/notification/v1alpha1"
	resourcemanagerv1alpha1webhook "go.miloapis.com/milo/internal/webhooks/resourcemanager/v1alpha1"
	milowebhook "go.miloapis.com/milo/pkg/webhook"
)

func buildControllerRuntimeConfig(opts *Options) (*rest.Config, error) {
	ctrlConfig, err := clientcmd.BuildConfigFromFlags(opts.Master, opts.Generic.ClientConnection.Kubeconfig)
	if err != nil {
		return nil, fmt.Errorf("building controller manager config: %w", err)
	}

	// Increase rate limits for controller-runtime manager to handle quota system load
	ctrlConfig.QPS = 100
	ctrlConfig.Burst = 200

	return ctrlConfig, nil
}

func servingCertPaths(opts *Options) (certPath, keyPath string) {
	certDir := opts.SecureServing.ServerCert.CertDirectory
	certName := strings.TrimPrefix(opts.SecureServing.ServerCert.CertKey.CertFile, certDir+"/")
	keyName := strings.TrimPrefix(opts.SecureServing.ServerCert.CertKey.KeyFile, certDir+"/")
	return filepath.Join(certDir, certName), filepath.Join(certDir, keyName)
}

func servingCertsReady(opts *Options) bool {
	certPath, keyPath := servingCertPaths(opts)
	if _, err := os.Stat(certPath); err != nil {
		return false
	}
	if _, err := os.Stat(keyPath); err != nil {
		return false
	}
	return true
}

func newClusterAwareWebhookServer(opts *Options, port int) webhook.Server {
	return milowebhook.NewClusterAwareServer(webhook.NewServer(webhook.Options{
		Port:     port,
		CertDir:  opts.SecureServing.ServerCert.CertDirectory,
		KeyName:  strings.TrimPrefix(opts.SecureServing.ServerCert.CertKey.KeyFile, opts.SecureServing.ServerCert.CertDirectory+"/"),
		CertName: strings.TrimPrefix(opts.SecureServing.ServerCert.CertKey.CertFile, opts.SecureServing.ServerCert.CertDirectory+"/"),
	}))
}

func registerCoreControlPlaneWebhooks(mgr controllerruntime.Manager, mcMgr mcmanager.Manager) error {
	if err := registerCoreControlPlaneWebhooksWithoutNotes(mgr); err != nil {
		return err
	}
	return registerNoteWebhooks(mgr, mcMgr)
}

func registerCoreControlPlaneWebhooksWithoutNotes(mgr controllerruntime.Manager) error {
	if err := resourcemanagerv1alpha1webhook.SetupProjectWebhooksWithManager(mgr, SystemNamespace, ProjectOwnerRoleName, ProjectOwnerRoleNamespace); err != nil {
		return fmt.Errorf("setting up project webhook: %w", err)
	}
	if err := resourcemanagerv1alpha1webhook.SetupOrganizationWebhooksWithManager(mgr, SystemNamespace, OrganizationOwnerRoleName, OrganizationOwnerRoleNamespace); err != nil {
		return fmt.Errorf("setting up organization webhook: %w", err)
	}
	if err := resourcemanagerv1alpha1webhook.SetupOrganizationMembershipWebhooksWithManager(mgr, OrganizationOwnerRoleName, OrganizationOwnerRoleNamespace); err != nil {
		return fmt.Errorf("setting up organizationmembership webhook: %w", err)
	}
	if err := iamv1alpha1webhook.SetupUserWebhooksWithManager(mgr, SystemNamespace, "iam-user-self-manage"); err != nil {
		return fmt.Errorf("setting up user webhook: %w", err)
	}
	if err := iamv1alpha1webhook.SetupUserDeactivationWebhooksWithManager(mgr, SystemNamespace); err != nil {
		return fmt.Errorf("setting up userdeactivation webhook: %w", err)
	}
	if err := identityv1alpha1webhook.SetupUserIdentityWebhooksWithManager(mgr); err != nil {
		return fmt.Errorf("setting up useridentity webhook: %w", err)
	}
	if err := notificationv1alpha1webhook.SetupEmailTemplateWebhooksWithManager(mgr, SystemNamespace); err != nil {
		return fmt.Errorf("setting up emailtemplate webhook: %w", err)
	}
	if err := notificationv1alpha1webhook.SetupEmailWebhooksWithManager(mgr); err != nil {
		return fmt.Errorf("setting up email webhook: %w", err)
	}
	if err := iamv1alpha1webhook.SetupUserInvitationWebhooksWithManager(mgr, SystemNamespace, AssignableRolesNamespace); err != nil {
		return fmt.Errorf("setting up user invitation webhook: %w", err)
	}
	if err := notificationv1alpha1webhook.SetupContactWebhooksWithManager(mgr); err != nil {
		return fmt.Errorf("setting up contact webhook: %w", err)
	}
	if err := notificationv1alpha1webhook.SetupContactGroupWebhooksWithManager(mgr); err != nil {
		return fmt.Errorf("setting up contactgroup webhook: %w", err)
	}
	if err := notificationv1alpha1webhook.SetupContactGroupMembershipWebhooksWithManager(mgr); err != nil {
		return fmt.Errorf("setting up contactgroupmembership webhook: %w", err)
	}
	if err := notificationv1alpha1webhook.SetupContactGroupMembershipRemovalWebhooksWithManager(mgr); err != nil {
		return fmt.Errorf("setting up contactgroupmembershipremoval webhook: %w", err)
	}
	if err := iamv1alpha1webhook.SetupPlatformInvitationWebhooksWithManager(mgr); err != nil {
		return fmt.Errorf("setting up platform invitation webhook: %w", err)
	}
	if err := iamv1alpha1webhook.SetupPlatformAccessApprovalWebhooksWithManager(mgr); err != nil {
		return fmt.Errorf("setting up platform access approval webhook: %w", err)
	}
	if err := iamv1alpha1webhook.SetupPlatformAccessRejectionWebhooksWithManager(mgr); err != nil {
		return fmt.Errorf("setting up platform access rejection webhook: %w", err)
	}
	if err := iamv1alpha1webhook.SetupPlatformAccessWebhooksWithManager(mgr); err != nil {
		return fmt.Errorf("setting up platform access webhook: %w", err)
	}
	if err := iamv1alpha1webhook.SetupPolicyBindingWebhooksWithManager(mgr); err != nil {
		return fmt.Errorf("setting up policybinding webhook: %w", err)
	}
	return nil
}

func registerNoteWebhooks(mgr controllerruntime.Manager, mcMgr mcmanager.Manager) error {
	if err := notesv1alpha1webhook.SetupNoteWebhooksWithManager(mgr, mcMgr); err != nil {
		return fmt.Errorf("setting up note webhook: %w", err)
	}
	if err := notesv1alpha1webhook.SetupClusterNoteWebhooksWithManager(mgr, mcMgr); err != nil {
		return fmt.Errorf("setting up clusternote webhook: %w", err)
	}
	return nil
}

func newWebhookRESTMapper(ctx context.Context, rootClientBuilder clientbuilder.ControllerClientBuilder) meta.RESTMapper {
	discoveryClient := rootClientBuilder.DiscoveryClientOrDie("webhook-discovery")
	cachedClient := cacheddiscovery.NewMemCacheClient(discoveryClient)
	restMapper := restmapper.NewDeferredDiscoveryRESTMapper(cachedClient)
	go wait.Until(func() {
		restMapper.Reset()
	}, 30*time.Second, ctx.Done())
	return restMapper
}

// startCoreControlPlaneWebhooks starts the admission webhook server on every replica.
// It is invoked before leader election when leader election is enabled so followers
// also listen on the webhook port and Service endpoints remain healthy.
func startCoreControlPlaneWebhooks(
	ctx context.Context,
	opts *Options,
	rootClientBuilder clientbuilder.ControllerClientBuilder,
	logger klog.Logger,
) error {
	restMapper := newWebhookRESTMapper(ctx, rootClientBuilder)

	ctrlConfig, err := buildControllerRuntimeConfig(opts)
	if err != nil {
		return err
	}

	webhookMgr, err := controllerruntime.NewManager(
		ctrlConfig,
		controllerruntime.Options{
			LeaderElection: false,
			Scheme:         Scheme,
			Cache: cache.Options{
				DefaultTransform: cache.TransformStripManagedFields(),
			},
			HealthProbeBindAddress: "0",
			Metrics: metricsserver.Options{
				BindAddress: "0",
			},
			MapperProvider: func(c *rest.Config, httpClient *http.Client) (meta.RESTMapper, error) {
				return restMapper, nil
			},
			WebhookServer: newClusterAwareWebhookServer(opts, opts.ControllerRuntimeWebhookPort),
		},
	)
	if err != nil {
		return fmt.Errorf("building webhook manager: %w", err)
	}

	if err := registerCoreControlPlaneWebhooksWithoutNotes(webhookMgr); err != nil {
		return err
	}

	go func() {
		if err := wait.PollUntilContextCancel(ctx, 2*time.Second, true, func(ctx context.Context) (bool, error) {
			if servingCertsReady(opts) {
				return true, nil
			}
			logger.Info("Waiting for webhook serving certificates")
			return false, nil
		}); err != nil {
			logger.Error(err, "Stopped waiting for webhook serving certificates")
			return
		}

		logger.Info("Starting core control plane webhook server", "port", opts.ControllerRuntimeWebhookPort)
		if err := webhookMgr.Start(ctx); err != nil {
			logger.Error(err, "Webhook manager failed; shutting down controller-manager")
			klog.FlushAndExit(klog.ExitFlushTimeout, 1)
		}
	}()

	return nil
}

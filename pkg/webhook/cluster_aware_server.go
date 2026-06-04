// Package webhook provides multi-cluster aware webhook utilities for services
// that integrate with Milo's project control plane architecture.
package webhook

import (
	"context"
	"net/http"

	iamv1alpha1 "go.miloapis.com/milo/pkg/apis/iam/v1alpha1"
	authv1 "k8s.io/api/authentication/v1"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
	mccontext "sigs.k8s.io/multicluster-runtime/pkg/context"
	"sigs.k8s.io/multicluster-runtime/pkg/multicluster"
)

// ClusterAwareServer wraps a webhook.Server to automatically inject the cluster
// name from the request's UserInfo.Extra into the context. This allows webhook
// handlers to use mccontext.ClusterFrom(ctx) to determine which project control
// plane the request is targeting.
//
// The cluster name is extracted from the "iam.miloapis.com/parent-name" extra
// field, which is set by Milo's API server when requests target a project
// control plane via the aggregated API path.
type ClusterAwareServer struct {
	webhook.Server
}

var _ webhook.Server = &ClusterAwareServer{}

// NewClusterAwareServer wraps a webhook.Server to inject cluster context from
// the request's UserInfo.Extra fields into the handler context.
//
// Example usage:
//
//	webhookServer := webhook.NewServer(webhook.Options{...})
//	webhookServer = milowebhook.NewClusterAwareServer(webhookServer)
//	mgr.Add(webhookServer)
func NewClusterAwareServer(server webhook.Server) *ClusterAwareServer {
	return &ClusterAwareServer{
		Server: server,
	}
}

// Register wraps the webhook handler to inject cluster context before calling
// the original handler.
func (s *ClusterAwareServer) Register(path string, hook http.Handler) {
	if h, ok := hook.(*admission.Webhook); ok {
		orig := h.Handler
		h.Handler = admission.HandlerFunc(func(ctx context.Context, req admission.Request) admission.Response {
			clusterName := clusterNameFromExtra(req.UserInfo.Extra)
			if clusterName != "" {
				ctx = mccontext.WithCluster(ctx, multicluster.ClusterName(clusterName))
			}
			return orig.Handle(ctx, req)
		})
	}

	s.Server.Register(path, hook)
}

// clusterNameFromExtra extracts the cluster/project name from the UserInfo.Extra
// fields. Returns empty string if not in a project context.
func clusterNameFromExtra(extra map[string]authv1.ExtraValue) string {
	// Check if this is a project context
	if parentKinds, ok := extra[iamv1alpha1.ParentKindExtraKey]; !ok || len(parentKinds) == 0 || parentKinds[0] != "Project" {
		return ""
	}

	// Extract the project name
	if parentNames, ok := extra[iamv1alpha1.ParentNameExtraKey]; ok && len(parentNames) > 0 {
		return parentNames[0]
	}

	return ""
}

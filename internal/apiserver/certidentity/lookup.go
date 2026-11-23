package certidentity

import (
	"context"
	"fmt"
	"net/url"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
)

var serviceAccountsGVR = schema.GroupVersionResource{Group: "iam.miloapis.com", Version: "v1alpha1", Resource: "serviceaccounts"}

// NewLoopbackLookup lists ServiceAccounts through the API server's own
// loopback endpoint, using the project's control plane path when a project
// is given.
func NewLoopbackLookup(base *rest.Config) Lookup {
	return func(ctx context.Context, project string) ([]unstructured.Unstructured, error) {
		cfg := rest.CopyConfig(base)
		if project != "" {
			host, err := url.Parse(cfg.Host)
			if err != nil {
				return nil, fmt.Errorf("parse API host %q: %w", cfg.Host, err)
			}
			host.Path = fmt.Sprintf("/apis/resourcemanager.miloapis.com/v1alpha1/projects/%s/control-plane", project)
			cfg.Host = host.String()
		}
		client, err := dynamic.NewForConfig(cfg)
		if err != nil {
			return nil, fmt.Errorf("create client for project %q: %w", project, err)
		}
		list, err := client.Resource(serviceAccountsGVR).List(ctx, metav1.ListOptions{})
		if apierrors.IsNotFound(err) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		return list.Items, nil
	}
}

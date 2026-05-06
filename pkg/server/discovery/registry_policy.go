package discovery

import (
	"context"
	"fmt"
	"time"

	discoveryv1alpha1 "go.miloapis.com/milo/pkg/apis/discovery/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/dynamic/dynamicinformer"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"
)

var discoveryContextPolicyGVR = schema.GroupVersionResource{
	Group:    "discovery.miloapis.com",
	Version:  "v1alpha1",
	Resource: "discoverycontextpolicies",
}

// RunPolicyInformer starts watching DiscoveryContextPolicy objects using a dynamic
// informer against the provided loopback REST config. It blocks until ctx is cancelled.
//
// The retry loop is required because the DiscoveryContextPolicy CRD is bootstrapped
// by Milo's own post-start hook, so this informer may be called before the CRD exists.
func (r *Registry) RunPolicyInformer(ctx context.Context, loopbackConfig *rest.Config) error {
	backoff := wait.Backoff{
		Duration: 500 * time.Millisecond,
		Factor:   2.0,
		Jitter:   0.1,
		Steps:    10,
		Cap:      30 * time.Second,
	}

	var factory dynamicinformer.DynamicSharedInformerFactory

	err := wait.ExponentialBackoffWithContext(ctx, backoff, func(ctx context.Context) (bool, error) {
		dynClient, err := dynamic.NewForConfig(loopbackConfig)
		if err != nil {
			klog.V(4).InfoS("Failed to create dynamic client for policy informer, retrying", "err", err)
			return false, nil
		}

		// Probe that the GVR is available before setting up the informer.
		_, err = dynClient.Resource(discoveryContextPolicyGVR).List(ctx, metav1.ListOptions{Limit: 1})
		if err != nil {
			klog.V(4).InfoS("DiscoveryContextPolicy CRD not yet available, retrying", "err", err)
			return false, nil
		}

		factory = dynamicinformer.NewDynamicSharedInformerFactory(dynClient, 0)
		return true, nil
	})
	if err != nil {
		return fmt.Errorf("timed out waiting for DiscoveryContextPolicy CRD: %w", err)
	}
	if factory == nil {
		return fmt.Errorf("context cancelled before DiscoveryContextPolicy CRD became available")
	}

	informer := factory.ForResource(discoveryContextPolicyGVR).Informer()

	_, err = informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    func(obj any) { r.upsertPolicyFromUnstructured(obj) },
		UpdateFunc: func(_, obj any) { r.upsertPolicyFromUnstructured(obj) },
		DeleteFunc: func(obj any) { r.deletePolicyFromUnstructured(obj) },
	})
	if err != nil {
		return fmt.Errorf("registering DiscoveryContextPolicy event handler: %w", err)
	}

	factory.Start(ctx.Done())

	if !cache.WaitForCacheSync(ctx.Done(), informer.HasSynced) {
		return fmt.Errorf("DiscoveryContextPolicy informer cache failed to sync")
	}

	r.mu.Lock()
	r.hasPolicyInit = true
	r.mu.Unlock()

	klog.InfoS("Discovery policy registry synced", "policyEntries", len(r.policy))
	<-ctx.Done()
	return nil
}

// unstrContent is the subset of *unstructured.Unstructured used for decoding.
type unstrContent interface {
	GetName() string
	UnstructuredContent() map[string]interface{}
}

func (r *Registry) upsertPolicyFromUnstructured(obj any) {
	u, ok := extractUnstructured(obj)
	if !ok {
		return
	}

	var policy discoveryv1alpha1.DiscoveryContextPolicy
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(u.UnstructuredContent(), &policy); err != nil {
		klog.ErrorS(err, "Failed to decode DiscoveryContextPolicy")
		return
	}

	spec := policySpec{rules: make([]policyRule, 0, len(policy.Spec.Rules))}
	for _, rule := range policy.Spec.Rules {
		spec.rules = append(spec.rules, policyRule{
			group:     rule.Group,
			resources: rule.Resources,
			contexts:  rule.Contexts,
		})
	}

	r.upsertFromPolicy(policy.Name, spec)
}

func (r *Registry) deletePolicyFromUnstructured(obj any) {
	u, ok := extractUnstructured(obj)
	if !ok {
		return
	}
	r.deleteFromPolicy(u.GetName())
}

// extractUnstructured returns the unstrContent from an event handler object,
// handling the DeletedFinalStateUnknown tombstone case.
func extractUnstructured(obj any) (unstrContent, bool) {
	switch v := obj.(type) {
	case unstrContent:
		return v, true
	case cache.DeletedFinalStateUnknown:
		u, ok := v.Obj.(unstrContent)
		return u, ok
	}
	return nil, false
}

package appsownership

import (
	"context"
	"reflect"
	"strings"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/apiserver/pkg/admission"
)

// FinalizerWriter pins exactly one native controller's finalizer. Removal on an
// already-terminating object can complete Kubernetes deletion; this is not
// permission to initiate deletion, clear ownership, or mutate other finalizers.
type FinalizerWriter struct {
	StatusWriter
	Finalizer string `json:"finalizer"`
}

func validNativeFinalizer(value string) bool {
	domain, name, ok := strings.Cut(value, "/")
	return ok && domain != "" && name != "" && len(validation.IsDNS1123Subdomain(domain)) == 0 && len(validation.IsQualifiedName(value)) == 0
}

func (p *Plugin) allowFinalizer(ctx context.Context, a admission.Attributes) bool {
	if len(p.finalizerWriters) == 0 || p.dynamicClient == nil || a.GetOperation() != admission.Update || a.GetSubresource() != "" {
		return false
	}
	old, next, projectUID, ok := ownershipObjects(a)
	if !ok || !sameOutsideFinalizers(old, next) {
		return false
	}
	oldMeta, _ := meta.Accessor(old)
	oldFinalizers, _, oldErr := unstructured.NestedStringSlice(old.UnstructuredContent(), "metadata", "finalizers")
	newFinalizers, _, newErr := unstructured.NestedStringSlice(next.UnstructuredContent(), "metadata", "finalizers")
	if oldErr != nil || newErr != nil {
		return false
	}
	for _, rule := range p.finalizerWriters {
		if !matchesNativeWriter(ctx, a, rule.StatusWriter) {
			continue
		}
		// Require one actual add/remove of the configured name and preserve the
		// order and content of every other finalizer. Duplicate entries fail closed.
		before, oldCount, validOld := withoutFinalizer(oldFinalizers, rule.Finalizer)
		after, newCount, validNew := withoutFinalizer(newFinalizers, rule.Finalizer)
		if !validOld || !validNew || !reflect.DeepEqual(before, after) {
			continue
		}
		adding := oldCount == 0 && newCount == 1 && oldMeta.GetDeletionTimestamp() == nil
		removing := oldCount == 1 && newCount == 0 && oldMeta.GetDeletionTimestamp() != nil
		if adding || removing {
			return p.liveOwnershipProject(ctx, a, projectUID)
		}
	}
	return false
}

func withoutFinalizer(values []string, permitted string) ([]string, int, bool) {
	others := make([]string, 0, len(values))
	count := 0
	seen := map[string]bool{}
	for _, value := range values {
		if value == "" || seen[value] {
			return nil, 0, false
		}
		seen[value] = true
		if value == permitted {
			count++
		} else {
			others = append(others, value)
		}
	}
	return others, count, true
}

func sameOutsideFinalizers(old, next runtime.Unstructured) bool {
	normalize := func(obj runtime.Unstructured) map[string]interface{} {
		value := runtime.DeepCopyJSON(obj.UnstructuredContent())
		if metadata, ok := value["metadata"].(map[string]interface{}); ok {
			delete(metadata, "resourceVersion")
			delete(metadata, "managedFields")
			delete(metadata, "finalizers")
		}
		return value
	}
	return reflect.DeepEqual(normalize(old), normalize(next))
}

package appsownership

import (
	"context"
	"fmt"
	"io"
	"reflect"
	"time"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/apiserver/pkg/admission"
	"k8s.io/apiserver/pkg/admission/initializer"
	"k8s.io/client-go/dynamic"
	"sigs.k8s.io/yaml"

	milorequest "go.miloapis.com/milo/pkg/request"
	datumfilters "go.miloapis.com/milo/pkg/server/filters"
)

// StatusWriter pins an authenticated principal and one native version's status
// endpoint. It grants no Kubernetes authorization and cannot create ownership.
// Credential custody outside consumer projects must be established separately.
type StatusWriter struct {
	Username string `json:"username"`
	UID      string `json:"uid"`
	Group    string `json:"group"`
	Version  string `json:"version"`
	Resource string `json:"resource"`
	Kind     string `json:"kind"`
}

type Configuration struct {
	APIVersion        string            `json:"apiVersion"`
	Kind              string            `json:"kind"`
	PendingRequests   string            `json:"pendingRequests,omitempty"`
	GrantRegistration string            `json:"grantRegistration,omitempty"`
	StatusWriters     []StatusWriter    `json:"statusWriters"`
	FinalizerWriters  []FinalizerWriter `json:"finalizerWriters"`
}

func readConfiguration(reader io.Reader) (Configuration, error) {
	if reader == nil {
		return Configuration{}, nil
	}
	raw, err := io.ReadAll(io.LimitReader(reader, 64*1024+1))
	if err != nil || len(raw) > 64*1024 {
		return Configuration{}, fmt.Errorf("Apps ownership configuration cannot be read within 64KiB")
	}
	var config Configuration
	if err = yaml.UnmarshalStrict(raw, &config); err != nil {
		return Configuration{}, fmt.Errorf("invalid Apps ownership configuration: %w", err)
	}
	if config.APIVersion != "apiserver.miloapis.com/v1alpha1" || config.Kind != "AppsOwnershipConfiguration" || len(config.StatusWriters)+len(config.FinalizerWriters) > 32 {
		return Configuration{}, fmt.Errorf("Apps ownership configuration must have the supported version/kind and at most 32 native writer rules")
	}
	if config.GrantRegistration != "" && config.GrantRegistration != GrantRegistrationV1 {
		return Configuration{}, fmt.Errorf("unsupported Apps grant registration contract")
	}
	if config.PendingRequests != "" && config.PendingRequests != PendingRequestsV1 {
		return Configuration{}, fmt.Errorf("unsupported pending App contract")
	}
	seen := map[StatusWriter]bool{}
	for _, rule := range config.StatusWriters {
		if !validWriter(rule) || seen[rule] {
			return Configuration{}, fmt.Errorf("Apps ownership status writer must uniquely pin a principal and exact non-core GVR/kind")
		}
		seen[rule] = true
	}
	seenFinalizers := map[FinalizerWriter]bool{}
	for _, rule := range config.FinalizerWriters {
		if !validWriter(rule.StatusWriter) || !validNativeFinalizer(rule.Finalizer) || seenFinalizers[rule] {
			return Configuration{}, fmt.Errorf("Apps ownership finalizer writer must uniquely pin a principal, exact non-core GVR/kind and qualified finalizer")
		}
		seenFinalizers[rule] = true
	}
	return config, nil
}

var _ initializer.WantsDynamicClient = &Plugin{}
var _ admission.InitializationValidator = &Plugin{}

func (p *Plugin) SetDynamicClient(client dynamic.Interface) { p.dynamicClient = client }
func (p *Plugin) ValidateInitialization() error {
	if (len(p.statusWriters) > 0 || len(p.finalizerWriters) > 0 || p.pendingRequests != "") && p.dynamicClient == nil {
		return fmt.Errorf("%s: configured native writers require the root Project client", PluginName)
	}
	return nil
}

// allowStatus is deliberately restricted to unstructured native objects, whose
// complete spec and metadata can be compared. It cannot exempt core requests,
// scale/finalize subresources, missing old objects, or a malformed marker pair.
func (p *Plugin) allowStatus(ctx context.Context, a admission.Attributes) bool {
	if len(p.statusWriters) == 0 || p.dynamicClient == nil || a.GetOperation() != admission.Update || a.GetSubresource() != "status" {
		return false
	}
	matched := false
	for _, rule := range p.statusWriters {
		if matchesNativeWriter(ctx, a, rule) {
			matched = true
			break
		}
	}
	if !matched {
		return false
	}
	old, next, projectUID, ok := ownershipObjects(a)
	if !ok || !sameOutsideStatus(old, next) {
		return false
	}
	return p.liveOwnershipProject(ctx, projectUID)
}

func validWriter(rule StatusWriter) bool {
	return !(rule.Username == "" || len(rule.Username) > 256 || rule.UID == "" || len(rule.UID) > 128 || rule.Group == "" || len(validation.IsDNS1123Subdomain(rule.Group)) != 0 || len(validation.IsDNS1035Label(rule.Version)) != 0 || len(validation.IsDNS1035Label(rule.Resource)) != 0 || rule.Kind == "" || len(validation.IsCIdentifier(rule.Kind)) != 0)
}

func matchesNativeWriter(ctx context.Context, a admission.Attributes, rule StatusWriter) bool {
	provenance, ok := datumfilters.AuthenticationProvenanceFrom(ctx)
	effective := a.GetUserInfo()
	return ok && provenance.User != nil && effective != nil && !provenance.ImpersonationRequested &&
		provenance.User.GetName() == effective.GetName() && provenance.User.GetUID() == effective.GetUID() &&
		rule.Username == provenance.User.GetName() && rule.UID == provenance.User.GetUID() &&
		a.GetResource() == (schema.GroupVersionResource{Group: rule.Group, Version: rule.Version, Resource: rule.Resource}) &&
		a.GetKind() == (schema.GroupVersionKind{Group: rule.Group, Version: rule.Version, Kind: rule.Kind})
}

func ownershipObjects(a admission.Attributes) (runtime.Unstructured, runtime.Unstructured, string, bool) {
	old, okOld := a.GetOldObject().(runtime.Unstructured)
	next, okNext := a.GetObject().(runtime.Unstructured)
	if !okOld || !okNext {
		return nil, nil, "", false
	}
	oldMeta, err := meta.Accessor(old)
	if err != nil {
		return nil, nil, "", false
	}
	nextMeta, err := meta.Accessor(next)
	if err != nil {
		return nil, nil, "", false
	}
	projectUID := oldMeta.GetLabels()[ProjectUIDLabel]
	appUID := oldMeta.GetLabels()[AppUIDLabel]
	if projectUID == "" || appUID == "" || len(validation.IsValidLabelValue(projectUID)) != 0 || len(validation.IsValidLabelValue(appUID)) != 0 || oldMeta.GetUID() == "" || nextMeta.GetUID() != oldMeta.GetUID() || oldMeta.GetName() != a.GetName() || oldMeta.GetNamespace() != a.GetNamespace() || len(oldMeta.GetOwnerReferences()) != 0 {
		return nil, nil, "", false
	}
	for _, obj := range []metav1.Object{oldMeta, nextMeta} {
		for _, key := range []string{ProjectUIDLabel, AppUIDLabel} {
			if _, exists := obj.GetAnnotations()[key]; exists {
				return nil, nil, "", false
			}
		}
	}
	if old.GetObjectKind().GroupVersionKind() != a.GetKind() || next.GetObjectKind().GroupVersionKind() != a.GetKind() {
		return nil, nil, "", false
	}
	return old, next, projectUID, true
}

func (p *Plugin) liveOwnershipProject(ctx context.Context, projectUID string) bool {
	projectName, present := milorequest.ProjectID(ctx)
	if !present || projectName == "" {
		return false
	}
	// Read root scope with a hard bound. No cache and no per-App lookup. A live
	// Project read is not an atomic transaction with project teardown or IAM.
	lookupCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	project, err := p.dynamicClient.Resource(schema.GroupVersionResource{Group: "resourcemanager.miloapis.com", Version: "v1alpha1", Resource: "projects"}).Get(lookupCtx, projectName, metav1.GetOptions{})
	return err == nil && project != nil && project.GetName() == projectName && string(project.GetUID()) == projectUID && project.GetDeletionTimestamp() == nil
}

func sameOutsideStatus(old, next runtime.Unstructured) bool {
	normalize := func(obj runtime.Unstructured) map[string]interface{} {
		value := runtime.DeepCopyJSON(obj.UnstructuredContent())
		delete(value, "status")
		if metadata, ok := value["metadata"].(map[string]interface{}); ok {
			// Storage owns resourceVersion; SSA owns managedFields. Neither conveys
			// authorization. Every other metadata field, including ownership/finalizers,
			// must remain byte-for-byte equivalent in decoded JSON.
			delete(metadata, "resourceVersion")
			delete(metadata, "managedFields")
		}
		return value
	}
	return reflect.DeepEqual(normalize(old), normalize(next))
}

package appsownership

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"fmt"
	"reflect"
	"strings"
	"time"

	"go.miloapis.com/milo/internal/apiserver/admission/plugin/appsownership/pendingwire"
	iam "go.miloapis.com/milo/pkg/apis/iam/v1alpha1"
	milorequest "go.miloapis.com/milo/pkg/request"
	datumfilters "go.miloapis.com/milo/pkg/server/filters"
	extv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/apiserver/pkg/admission"
	"k8s.io/apiserver/pkg/authentication/user"
	request "k8s.io/apiserver/pkg/endpoints/request"
	"sigs.k8s.io/yaml"
)

// PendingRequestsV1 allows only the permanently nonaccepting pending protocol.
// Source: Apps 71d7bf5ed3563bd44e8071867212a146fc6ecf14. Neither this option nor
// these CRDs enable installation, source authorization, or any grant operation.
const PendingRequestsV1 = "pending-only-v1@6ac865e64f72656a55d8760cca188bb6d02dc623e2dd22a86c9c8fa14479eb38+2b3833d67ca202d0758095fb495f5be92c79d11a075a1b82caff0141e3f875c9"
const PendingProtocol = "pending-only-v1"
const PendingFinalizer = "apps.miloapis.com/pending-request"
const PendingProtocolAnnotation = "apps.miloapis.com/request-protocol"
const PendingProjectAnnotation = "apps.miloapis.com/request-project-uid"
const PendingActorAnnotation = "apps.miloapis.com/request-actor-uid"
const PendingDigestAnnotation = "apps.miloapis.com/request-spec-digest"
const PendingGenerationAnnotation = "apps.miloapis.com/request-generation"
const pendingMessage = "Apps pending request custody denies this mutation; acceptance, installation history and new protocols remain disabled"

//go:embed contracts/apps-pending-v1.yaml
var pendingAppCRD []byte

//go:embed contracts/apprevisions-pending-v1.yaml
var pendingRevisionCRD []byte

var pendingContracts = map[string]*extv1.CustomResourceDefinition{
	"apps.apps.miloapis.com":         loadPendingCRD(pendingAppCRD, "6ac865e64f72656a55d8760cca188bb6d02dc623e2dd22a86c9c8fa14479eb38"),
	"apprevisions.apps.miloapis.com": loadPendingCRD(pendingRevisionCRD, "2b3833d67ca202d0758095fb495f5be92c79d11a075a1b82caff0141e3f875c9"),
}

func loadPendingCRD(raw []byte, hash string) *extv1.CustomResourceDefinition {
	if fmt.Sprintf("%x", sha256.Sum256(raw)) != hash {
		panic("pending App contract hash mismatch")
	}
	o := &extv1.CustomResourceDefinition{}
	if yaml.UnmarshalStrict(raw, o) != nil {
		panic("invalid pending App contract")
	}
	registrationScheme.Default(o)
	return o
}
func (p *Plugin) validatePending(ctx context.Context, a admission.Attributes) error {
	deny := func() error { return admission.NewForbidden(a, fmt.Errorf("%s", pendingMessage)) }
	r := a.GetResource()
	if r.Group == GrantGroup && (r.Resource == "apps" || r.Resource == "apprevisions") {
		if p.pendingRequests != PendingRequestsV1 || r.Resource != "apps" || !p.allowPending(ctx, a) {
			return deny()
		}
		return nil
	}
	if r.Group != "apiextensions.k8s.io" || r.Resource != "customresourcedefinitions" {
		return nil
	}
	protected := pendingContracts[a.GetName()] != nil
	objects := []runtime.Object{a.GetOldObject()}
	if a.GetOperation() != admission.Delete {
		objects = append(objects, a.GetObject())
	}
	for _, o := range objects {
		if o == nil {
			continue
		}
		m, err := meta.Accessor(o)
		if err != nil {
			return deny()
		}
		if pendingContracts[m.GetName()] != nil {
			protected = true
		}
		var value map[string]interface{}
		if u, ok := o.(runtime.Unstructured); ok {
			value = u.UnstructuredContent()
		} else {
			value, err = runtime.DefaultUnstructuredConverter.ToUnstructured(o)
			if err != nil {
				return deny()
			}
		}
		group, _, err := unstructured.NestedString(value, "spec", "group")
		if err != nil {
			return deny()
		}
		if group == GrantGroup {
			plural, _, err := unstructured.NestedString(value, "spec", "names", "plural")
			if err != nil {
				return deny()
			}
			if plural == "apps" || plural == "apprevisions" {
				protected = true
			}
		}
	}
	if !protected {
		return nil
	}
	expected := pendingContracts[a.GetName()]
	if p.pendingRequests == PendingRequestsV1 && expected != nil && allowExactRegistration(ctx, a, expected) {
		return nil
	}
	return deny()
}

// Scope is derived only from the router/private provenance. The Project
// decorator intentionally replaces the canonical parent extras after capture.
func pendingProjectScope(ctx context.Context, a admission.Attributes) (string, bool) {
	project, ok := milorequest.ProjectID(ctx)
	if !ok || project == "" || len(validation.IsDNS1123Subdomain(project)) != 0 {
		return "", false
	}
	if _, ok = milorequest.OrganizationID(ctx); ok {
		return "", false
	}
	if _, ok = datumfilters.UserID(ctx); ok {
		return "", false
	}
	provenance, ok := datumfilters.AuthenticationProvenanceFrom(ctx)
	effective := a.GetUserInfo()
	if !ok || provenance.User == nil || effective == nil || provenance.ImpersonationRequested || effective.GetName() == user.Anonymous || !pendingwire.Identity(effective.GetName()) || !pendingwire.Identity(effective.GetUID()) || provenance.User.GetName() != effective.GetName() || provenance.User.GetUID() != effective.GetUID() || !reflect.DeepEqual(provenance.User.GetGroups(), effective.GetGroups()) {
		return "", false
	}
	expected := map[string][]string{}
	for k, v := range provenance.User.GetExtra() {
		expected[k] = v
	}
	for _, u := range []user.Info{provenance.User, effective} {
		for k := range u.GetExtra() {
			for _, canonical := range []string{iam.ParentAPIGroupExtraKey, iam.ParentKindExtraKey, iam.ParentNameExtraKey} {
				if strings.EqualFold(k, canonical) && k != canonical {
					return "", false
				}
			}
		}
	}
	expected[iam.ParentAPIGroupExtraKey] = []string{"resourcemanager.miloapis.com"}
	expected[iam.ParentKindExtraKey] = []string{"Project"}
	expected[iam.ParentNameExtraKey] = []string{project}
	return project, reflect.DeepEqual(expected, effective.GetExtra())
}
func pendingObject(obj runtime.Object, name, projectUID string) (*unstructured.Unstructured, bool) {
	u, ok := obj.(*unstructured.Unstructured)
	if !ok || u == nil || u.GroupVersionKind() != (schema.GroupVersionKind{Group: GrantGroup, Version: "v1alpha1", Kind: "App"}) || u.GetName() != name || name == "" || len(validation.IsDNS1123Subdomain(name)) != 0 || u.GetNamespace() != "" || u.GetGenerateName() != "" || len(u.GetOwnerReferences()) != 0 {
		return nil, false
	}
	for k := range u.Object {
		if k != "apiVersion" && k != "kind" && k != "metadata" && k != "spec" && k != "status" {
			return nil, false
		}
	}
	// Match Apps validPending exactly: these three ownership/reservation keys
	// are forbidden in either metadata map, before native writer exceptions.
	for _, key := range []string{ProjectUIDLabel, AppUIDLabel, NamespaceReservationKey} {
		if _, present := u.GetLabels()[key]; present {
			return nil, false
		}
		if _, present := u.GetAnnotations()[key]; present {
			return nil, false
		}
	}
	annotations := u.GetAnnotations()
	if annotations[PendingProtocolAnnotation] != PendingProtocol || annotations[PendingProjectAnnotation] != projectUID || !pendingwire.Identity(annotations[PendingActorAnnotation]) || annotations[PendingGenerationAnnotation] != "1" {
		return nil, false
	}
	spec, ok := u.Object["spec"].(map[string]interface{})
	if !ok {
		return nil, false
	}
	digest, err := pendingwire.ValidateSpec(spec, projectUID)
	if err != nil || digest != annotations[PendingDigestAnnotation] {
		return nil, false
	}
	count := 0
	for _, f := range u.GetFinalizers() {
		if f == PendingFinalizer {
			count++
		}
	}
	if count > 1 || (u.GetDeletionTimestamp() == nil && count != 1) || u.GetGeneration() < 1 || (u.GetDeletionTimestamp() == nil && u.GetGeneration() != 1) {
		return nil, false
	}
	if raw, exists := u.Object["status"]; exists {
		status, ok := raw.(map[string]interface{})
		if !ok {
			return nil, false
		}
		if len(status) != 0 && !reflect.DeepEqual(status, pendingStatus(projectUID)) {
			return nil, false
		}
	}
	return u, true
}
func pendingStatus(uid string) map[string]interface{} {
	return map[string]interface{}{"projectUID": uid, "admittedGeneration": int64(1), "phase": "Pending"}
}
func pendingHasHold(o *unstructured.Unstructured) bool {
	for _, f := range o.GetFinalizers() {
		if f == PendingFinalizer {
			return true
		}
	}
	return false
}
func (p *Plugin) allowPending(ctx context.Context, a admission.Attributes) bool {
	if a.GetResource() != (schema.GroupVersionResource{Group: GrantGroup, Version: "v1alpha1", Resource: "apps"}) || a.GetKind() != (schema.GroupVersionKind{Group: GrantGroup, Version: "v1alpha1", Kind: "App"}) || a.GetNamespace() != "" || p.dynamicClient == nil {
		return false
	}
	name, ok := pendingProjectScope(ctx, a)
	if !ok {
		return false
	}
	ri, ok := request.RequestInfoFrom(ctx)
	if !ok || !ri.IsResourceRequest || ri.APIGroup != GrantGroup || ri.APIVersion != "v1alpha1" || ri.Resource != "apps" || ri.Namespace != "" || ri.Subresource != a.GetSubresource() {
		return false
	}
	lookup, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	project, err := p.dynamicClient.Resource(schema.GroupVersionResource{Group: "resourcemanager.miloapis.com", Version: "v1alpha1", Resource: "projects"}).Get(lookup, name, metav1.GetOptions{})
	if err != nil || project.GetName() != name || project.GetUID() == "" || project.GetNamespace() != "" {
		return false
	}
	uid := string(project.GetUID())
	if a.GetOperation() == admission.Create {
		next, ok := pendingObject(a.GetObject(), a.GetName(), uid)
		return ok && ri.Verb == "create" && ri.Name == "" && a.GetSubresource() == "" && a.GetOldObject() == nil && project.GetDeletionTimestamp() == nil && next.GetDeletionTimestamp() == nil && next.GetResourceVersion() == "" && next.GetAnnotations()[PendingActorAnnotation] == a.GetUserInfo().GetUID()
	}
	old, ok := pendingObject(a.GetOldObject(), a.GetName(), uid)
	if !ok || old.GetUID() == "" || old.GetResourceVersion() == "" || ri.Name != a.GetName() {
		return false
	}
	if a.GetOperation() == admission.Delete {
		options, ok := a.GetOperationOptions().(*metav1.DeleteOptions)
		return ok && options != nil && options.Preconditions != nil &&
			options.Preconditions.UID != nil && *options.Preconditions.UID == old.GetUID() &&
			options.Preconditions.ResourceVersion != nil && *options.Preconditions.ResourceVersion == old.GetResourceVersion() &&
			a.GetSubresource() == "" && ri.Verb == "delete"
	}
	if a.GetOperation() != admission.Update || (ri.Verb != "update" && ri.Verb != "patch") {
		return false
	}
	next, ok := pendingObject(a.GetObject(), a.GetName(), uid)
	if !ok || next.GetUID() != old.GetUID() || next.GetResourceVersion() != old.GetResourceVersion() {
		return false
	}
	before, after := old.DeepCopy(), next.DeepCopy()
	before.SetManagedFields(nil)
	after.SetManagedFields(nil)
	switch a.GetSubresource() {
	case "status":
		if !reflect.DeepEqual(after.Object["status"], pendingStatus(uid)) {
			return false
		}
		delete(before.Object, "status")
		delete(after.Object, "status")
	case "":
		if pendingHasHold(old) && !pendingHasHold(next) {
			if old.GetDeletionTimestamp() == nil {
				return false
			}
			finalizers := []string{}
			for _, f := range old.GetFinalizers() {
				if f != PendingFinalizer {
					finalizers = append(finalizers, f)
				}
			}
			before.SetFinalizers(finalizers)
		}
		// API metadata represents an empty finalizer list equivalently as absent.
		if len(before.GetFinalizers()) == 0 {
			before.SetFinalizers(nil)
		}
		if len(after.GetFinalizers()) == 0 {
			after.SetFinalizers(nil)
		}
	default:
		return false
	}
	return reflect.DeepEqual(before.Object, after.Object)
}

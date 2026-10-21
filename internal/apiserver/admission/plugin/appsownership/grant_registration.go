package appsownership

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"fmt"
	"reflect"
	"unicode/utf8"

	iam "go.miloapis.com/milo/pkg/apis/iam/v1alpha1"
	milorequest "go.miloapis.com/milo/pkg/request"
	datumfilters "go.miloapis.com/milo/pkg/server/filters"
	ext "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions"
	extinstall "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/install"
	extv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apiserver/pkg/admission"
	"k8s.io/apiserver/pkg/authentication/user"
	request "k8s.io/apiserver/pkg/endpoints/request"
	"sigs.k8s.io/yaml"
)

// GrantRegistrationV1 selects a compiled contract, never an arbitrary configured schema.
// It allows registration/status only; every grant instance write remains reserved.
const GrantRegistrationV1 = "disabled-consumption-v1@sha256:33c9be9ec2f9ac76c1945776b53568c18159bb50743f48144cd28bbc6e039d6d"
const grantCRDSHA256 = "33c9be9ec2f9ac76c1945776b53568c18159bb50743f48144cd28bbc6e039d6d"

// Apps e1be5c515cbffb798a67bc2da8278e0d522dddc4; source-owned exact wire contract.
//
//go:embed contracts/appinstallgrants-v1.yaml
var grantRegistrationSource []byte

var registrationScheme = func() *runtime.Scheme { s := runtime.NewScheme(); extinstall.Install(s); return s }()

var expectedGrantCRD = loadGrantRegistrationContract()

func loadGrantRegistrationContract() *extv1.CustomResourceDefinition {
	if fmt.Sprintf("%x", sha256.Sum256(grantRegistrationSource)) != grantCRDSHA256 {
		panic("Apps grant registration contract hash mismatch")
	}
	out := &extv1.CustomResourceDefinition{}
	if err := yaml.UnmarshalStrict(grantRegistrationSource, out); err != nil {
		panic("invalid compiled Apps grant registration contract")
	}
	// Use the native registered defaults, not a hand-maintained schema normalization.
	registrationScheme.Default(out)
	return out
}

func registrationCRD(obj runtime.Object) (*extv1.CustomResourceDefinition, bool) {
	switch obj := obj.(type) {
	case *extv1.CustomResourceDefinition:
		if obj == nil {
			return nil, false
		}
		return obj.DeepCopy(), true
	case *ext.CustomResourceDefinition:
		if obj == nil {
			return nil, false
		}
		out := &extv1.CustomResourceDefinition{}
		if registrationScheme.Convert(obj, out, nil) != nil {
			return nil, false
		}
		return out, true
	default:
		return nil, false
	}
}

func registrationRoot(ctx context.Context, a admission.Attributes) bool {
	if _, ok := milorequest.ProjectID(ctx); ok {
		return false
	}
	if _, ok := milorequest.OrganizationID(ctx); ok {
		return false
	}
	if _, ok := datumfilters.UserID(ctx); ok {
		return false
	}
	p, ok := datumfilters.AuthenticationProvenanceFrom(ctx)
	effective := a.GetUserInfo()
	if !ok || p.User == nil || effective == nil || p.ImpersonationRequested || p.User.GetName() == "" || p.User.GetUID() == "" || p.User.GetName() == user.Anonymous {
		return false
	}
	if p.User.GetName() != effective.GetName() || p.User.GetUID() != effective.GetUID() || !reflect.DeepEqual(p.User.GetGroups(), effective.GetGroups()) || !reflect.DeepEqual(p.User.GetExtra(), effective.GetExtra()) {
		return false
	}
	for _, u := range []user.Info{p.User, effective} {
		for _, key := range []string{iam.ParentAPIGroupExtraKey, iam.ParentKindExtraKey, iam.ParentNameExtraKey} {
			if _, exists := u.GetExtra()[key]; exists {
				return false
			}
		}
	}
	return true
}

func registrationMetadata(obj *extv1.CustomResourceDefinition) bool {
	m := &obj.ObjectMeta
	return m.Name == GrantCRDName && m.Namespace == "" && m.GenerateName == "" && m.SelfLink == "" && len(m.Labels) == 0 && reflect.DeepEqual(m.Annotations, expectedGrantCRD.Annotations) && len(m.OwnerReferences) == 0 && len(m.Finalizers) == 0 && m.DeletionTimestamp == nil && m.DeletionGracePeriodSeconds == nil
}

func (p *Plugin) allowGrantRegistration(ctx context.Context, a admission.Attributes) bool {
	if p.grantRegistration != GrantRegistrationV1 || a.GetResource() != (schema.GroupVersionResource{Group: "apiextensions.k8s.io", Version: "v1", Resource: "customresourcedefinitions"}) || a.GetKind() != (extv1.SchemeGroupVersion.WithKind("CustomResourceDefinition")) || a.GetName() != GrantCRDName || a.GetNamespace() != "" || !registrationRoot(ctx, a) {
		return false
	}
	ri, ok := request.RequestInfoFrom(ctx)
	if !ok || !ri.IsResourceRequest || ri.APIGroup != "apiextensions.k8s.io" || ri.APIVersion != "v1" || ri.Resource != "customresourcedefinitions" || ri.Subresource != a.GetSubresource() || ri.Namespace != "" {
		return false
	}
	next, ok := registrationCRD(a.GetObject())
	if !ok || !registrationMetadata(next) || !reflect.DeepEqual(next.Spec, expectedGrantCRD.Spec) {
		return false
	}
	switch {
	case a.GetOperation() == admission.Create && a.GetSubresource() == "":
		// Native SSA PATCH may invoke Create admission. It is not a bootstrap POST.
		return ri.Verb == "create" && ri.Name == "" && a.GetOldObject() == nil && next.ResourceVersion == "" && next.Generation == 1 && reflect.DeepEqual(next.Status, extv1.CustomResourceDefinitionStatus{StoredVersions: []string{"v1alpha1"}})
	case a.GetOperation() == admission.Update && a.GetSubresource() == "status":
		if (ri.Verb != "update" && ri.Verb != "patch") || ri.Name != GrantCRDName {
			return false
		}
		old, ok := registrationCRD(a.GetOldObject())
		if !ok || !registrationMetadata(old) || !reflect.DeepEqual(old.Spec, expectedGrantCRD.Spec) || old.UID == "" || next.UID != old.UID || old.ResourceVersion == "" || next.ResourceVersion != old.ResourceVersion || !registrationStatus(old.Status) || !registrationStatus(next.Status) {
			return false
		}
		before, after := old.ObjectMeta.DeepCopy(), next.ObjectMeta.DeepCopy()
		before.ResourceVersion, after.ResourceVersion = "", ""
		before.ManagedFields, after.ManagedFields = nil, nil
		if !reflect.DeepEqual(before, after) {
			return false
		}
		if reflect.DeepEqual(old.Status.AcceptedNames, expectedGrantCRD.Spec.Names) && !reflect.DeepEqual(next.Status.AcceptedNames, old.Status.AcceptedNames) {
			return false
		}
		return true
	default:
		return false
	}
}

func registrationStatus(s extv1.CustomResourceDefinitionStatus) bool {
	if !reflect.DeepEqual(s.StoredVersions, []string{"v1alpha1"}) || len(s.Conditions) > 4 {
		return false
	}
	n, expected := s.AcceptedNames, expectedGrantCRD.Spec.Names
	if (n.Plural != "" && n.Plural != expected.Plural) || (n.Singular != "" && n.Singular != expected.Singular) || (n.Kind != "" && n.Kind != expected.Kind) || (n.ListKind != "" && n.ListKind != expected.ListKind) || len(n.ShortNames) != 0 || len(n.Categories) != 0 {
		return false
	}
	seen := map[extv1.CustomResourceDefinitionConditionType]bool{}
	var established, accepted bool
	for _, c := range s.Conditions {
		if seen[c.Type] || !utf8.ValidString(c.Reason) || !utf8.ValidString(c.Message) || len(c.Reason) > 1024 || len(c.Message) > 8192 || c.LastTransitionTime.IsZero() {
			return false
		}
		seen[c.Type] = true
		switch c.Type {
		case extv1.NamesAccepted, extv1.Established, extv1.NonStructuralSchema, extv1.KubernetesAPIApprovalPolicyConformant:
		default:
			return false
		}
		if c.Status != extv1.ConditionTrue && c.Status != extv1.ConditionFalse && c.Status != extv1.ConditionUnknown {
			return false
		}
		if c.Type == extv1.Established && c.Status == extv1.ConditionTrue {
			established = true
		}
		if c.Type == extv1.NamesAccepted && c.Status == extv1.ConditionTrue {
			accepted = true
		}
	}
	return !established || (accepted && reflect.DeepEqual(n, expected))
}

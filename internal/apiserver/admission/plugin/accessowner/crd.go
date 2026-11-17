package accessowner

import (
	"context"
	"fmt"

	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"k8s.io/apiserver/pkg/admission"
	"k8s.io/apiserver/pkg/warning"

	"go.miloapis.com/milo/pkg/server/access"
)

var crdResource = schema.GroupResource{Group: apiextensionsv1.GroupName, Resource: "customresourcedefinitions"}

// validateCRD refuses a CRD create or update that declares access rules Milo
// can't apply, so the mistake is caught when it's written instead of
// refusing the resource's requests once it's served.
//
// An update is only refused for problems the old CRD didn't have, so a CRD
// stored before this check (which the access filter refuses at runtime) can
// still be updated to fix it, or for unrelated reasons.
//
// An update that changes the rule warns the client that each API server
// applies it once its CRD cache sees the change.
func validateCRD(ctx context.Context, attrs admission.Attributes) error {
	crd, err := toV1(attrs.GetObject())
	if err != nil {
		return apierrors.NewInternalError(err)
	}
	if crd == nil {
		return nil
	}
	d := access.Declare(crd)

	var old *access.Declaration
	if attrs.GetOperation() == admission.Update {
		oldCRD, err := toV1(attrs.GetOldObject())
		if err != nil {
			return apierrors.NewInternalError(err)
		}
		if oldCRD != nil {
			od := access.Declare(oldCRD)
			old = &od
		}
	}

	var errs field.ErrorList
	annotations := field.NewPath("metadata", "annotations")
	for _, p := range d.Problems {
		if old != nil && hasProblem(*old, p) {
			continue
		}
		errs = append(errs, field.Invalid(annotations.Key(p.Annotation), crd.Annotations[p.Annotation], p.Message))
	}
	if len(errs) > 0 {
		return apierrors.NewInvalid(schema.GroupKind{Group: apiextensionsv1.GroupName, Kind: "CustomResourceDefinition"}, crd.Name, errs)
	}

	if old != nil && old.Rule != d.Rule {
		warning.AddWarning(ctx, "", fmt.Sprintf(
			"the access rules of %s change on each API server once its CRD cache sees this update, normally within a second; "+
				"until then requests there use the previous rules", crd.Name))
	}
	return nil
}

func hasProblem(d access.Declaration, p access.Problem) bool {
	for _, q := range d.Problems {
		if q == p {
			return true
		}
	}
	return false
}

// toV1 returns obj as a v1 CRD. Admission sees the internal type.
func toV1(obj runtime.Object) (*apiextensionsv1.CustomResourceDefinition, error) {
	switch crd := obj.(type) {
	case nil:
		return nil, nil
	case *apiextensionsv1.CustomResourceDefinition:
		return crd, nil
	case *apiextensions.CustomResourceDefinition:
		out := &apiextensionsv1.CustomResourceDefinition{}
		if err := apiextensionsv1.Convert_apiextensions_CustomResourceDefinition_To_v1_CustomResourceDefinition(crd, out, nil); err != nil {
			return nil, fmt.Errorf("converting CustomResourceDefinition %s: %w", crd.Name, err)
		}
		return out, nil
	default:
		return nil, fmt.Errorf("unexpected CustomResourceDefinition type %T", obj)
	}
}

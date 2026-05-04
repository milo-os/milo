package identity

import (
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"go.miloapis.com/milo/pkg/apis/identity/v1alpha1"
)

// Install registers the identity API group versions into the provided scheme,
// along with FieldLabelConversionFuncs for any selectors served by aggregated
// apiservers under this group. Without these registrations the milo aggregator
// pre-rejects requests with the default "is not a known field selector" error
// before proxying them to the backing apiserver.
func Install(scheme *runtime.Scheme) {
	v1alpha1.AddToScheme(scheme)

	_ = scheme.AddFieldLabelConversionFunc(
		schema.GroupVersionKind{
			Group:   v1alpha1.SchemeGroupVersion.Group,
			Version: v1alpha1.SchemeGroupVersion.Version,
			Kind:    "ServiceAccountKey",
		},
		func(label, value string) (string, string, error) {
			switch label {
			case "spec.serviceAccountUserName", "metadata.name", "metadata.namespace":
				return label, value, nil
			default:
				return "", "", nil
			}
		},
	)

	// Sessions are listed by callers (e.g. fraud-operator) using
	// status.userUID=<uid> for cross-user lookups. The auth-provider-zitadel
	// aggregated apiserver consumes this selector behind a SAR-gated REST
	// handler, but the request never reaches it without this registration on
	// the milo aggregator.
	_ = scheme.AddFieldLabelConversionFunc(
		schema.GroupVersionKind{
			Group:   v1alpha1.SchemeGroupVersion.Group,
			Version: v1alpha1.SchemeGroupVersion.Version,
			Kind:    "Session",
		},
		func(label, value string) (string, string, error) {
			switch label {
			case "status.userUID", "metadata.name", "metadata.namespace":
				return label, value, nil
			default:
				return "", "", nil
			}
		},
	)
}

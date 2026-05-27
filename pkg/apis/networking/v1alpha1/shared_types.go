package v1alpha1

// LocationClassName categorizes a Location by its ownership model.
//
// +kubebuilder:validation:Enum=datum-managed;provider-dedicated;self-managed
type LocationClassName string

const (
	// LocationClassDatumManaged identifies locations operated by Datum on behalf
	// of all consumers.
	LocationClassDatumManaged LocationClassName = "datum-managed"
	// LocationClassProviderDedicated identifies locations dedicated to a single
	// provider project.
	LocationClassProviderDedicated LocationClassName = "provider-dedicated"
	// LocationClassSelfManaged identifies locations owned and operated by the
	// consumer project itself.
	LocationClassSelfManaged LocationClassName = "self-managed"
)

// LocalObjectReference references an object by name within the same scope.
type LocalObjectReference struct {
	// Name is the name of the referenced object.
	//
	// +kubebuilder:validation:Required
	Name string `json:"name"`
}

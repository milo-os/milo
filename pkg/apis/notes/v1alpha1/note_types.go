package v1alpha1

import (
	iamv1alpha1 "go.miloapis.com/milo/pkg/apis/iam/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// Note is the Schema for the notes API.
// It represents a namespaced note attached to a subject resource.
// +kubebuilder:printcolumn:name="Subject Kind",type="string",JSONPath=".spec.subjectRef.kind"
// +kubebuilder:printcolumn:name="Subject Name",type="string",JSONPath=".spec.subjectRef.name"
// +kubebuilder:printcolumn:name="Creator",type="string",JSONPath=".spec.creatorRef.name"
// +kubebuilder:printcolumn:name="Ready",type="string",JSONPath=".status.conditions[?(@.type=='Ready')].status"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"
// +kubebuilder:resource:scope=Namespaced
// +kubebuilder:selectablefield:JSONPath=".spec.creatorRef.name"
// +kubebuilder:selectablefield:JSONPath=".spec.subjectRef.name"
// +kubebuilder:selectablefield:JSONPath=".spec.subjectRef.namespace"
// +kubebuilder:selectablefield:JSONPath=".spec.subjectRef.kind"
// +kubebuilder:selectablefield:JSONPath=".spec.nextActionTime"
// +kubebuilder:selectablefield:JSONPath=".spec.followUp"
// +kubebuilder:selectablefield:JSONPath=".status.createdBy"
// +kubebuilder:metadata:annotations="discovery.miloapis.com/parent-contexts=Platform"
type Note struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   NoteSpec   `json:"spec,omitempty"`
	Status NoteStatus `json:"status,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// ClusterNote is the Schema for the cluster-scoped notes API.
// It represents a note attached to a cluster-scoped subject resource.
// +kubebuilder:printcolumn:name="Subject Kind",type="string",JSONPath=".spec.subjectRef.kind"
// +kubebuilder:printcolumn:name="Subject Name",type="string",JSONPath=".spec.subjectRef.name"
// +kubebuilder:printcolumn:name="Creator",type="string",JSONPath=".spec.creatorRef.name"
// +kubebuilder:printcolumn:name="Ready",type="string",JSONPath=".status.conditions[?(@.type=='Ready')].status"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"
// +kubebuilder:resource:scope=Cluster
// +kubebuilder:selectablefield:JSONPath=".spec.creatorRef.name"
// +kubebuilder:selectablefield:JSONPath=".spec.subjectRef.name"
// +kubebuilder:selectablefield:JSONPath=".spec.subjectRef.kind"
// +kubebuilder:selectablefield:JSONPath=".spec.nextActionTime"
// +kubebuilder:selectablefield:JSONPath=".spec.followUp"
// +kubebuilder:selectablefield:JSONPath=".status.createdBy"
// +kubebuilder:metadata:annotations="discovery.miloapis.com/parent-contexts=Platform"
type ClusterNote struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   NoteSpec   `json:"spec,omitempty"`
	Status NoteStatus `json:"status,omitempty"`
}

// NoteSpec defines the desired state of Note.
// +kubebuilder:validation:Type=object
type NoteSpec struct {
	// Subject is a reference to the subject of the note.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:XValidation:rule="type(oldSelf) == null_type || self == oldSelf",message="subject type is immutable"
	SubjectRef SubjectReference `json:"subjectRef"`

	// Content is the text content of the note.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MaxLength=1000
	Content string `json:"content"`

	// InteractionTime is the timestamp of the interaction with the subject.
	// +kubebuilder:validation:Optional
	InteractionTime *metav1.Time `json:"interactionTime,omitempty"`

	// NextAction is an optional follow-up action.
	// +kubebuilder:validation:Optional
	NextAction string `json:"nextAction,omitempty"`

	// NextActionTime is the timestamp for the follow-up action.
	// +kubebuilder:validation:Optional
	NextActionTime *metav1.Time `json:"nextActionTime,omitempty"`

	// FollowUp indicates whether this note requires follow-up.
	// When true, the note is being actively tracked for further action.
	// +kubebuilder:validation:Optional
	// +kubebuilder:default=false
	FollowUp bool `json:"followUp,omitempty"`

	// CreatorRef is a reference to the user that created the note.
	// Defaults to the user that created the note.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:XValidation:rule="type(oldSelf) == null_type || self == oldSelf",message="creatorRef type is immutable"
	CreatorRef iamv1alpha1.UserReference `json:"creatorRef"`
}

// SubjectReference is a reference to the subject of the note.
// +kubebuilder:validation:Type=object
type SubjectReference struct {
	// APIGroup is the group for the resource being referenced.
	// +kubebuilder:validation:Required
	APIGroup string `json:"apiGroup"`

	// Kind is the type of resource being referenced.
	// +kubebuilder:validation:Required
	Kind string `json:"kind"`

	// Name is the name of resource being referenced.
	// +kubebuilder:validation:Required
	Name string `json:"name"`

	// Namespace is the namespace of resource being referenced.
	// Required for namespace-scoped resources. Omitted for cluster-scoped resources.
	// +kubebuilder:validation:Optional
	Namespace string `json:"namespace,omitempty"`
}

// +kubebuilder:object:root=true

// NoteList contains a list of Note.
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type NoteList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Note `json:"items"`
}

// +kubebuilder:object:root=true

// ClusterNoteList contains a list of ClusterNote.
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type ClusterNoteList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ClusterNote `json:"items"`
}

// NoteStatus defines the observed state of Note
type NoteStatus struct {
	// Conditions provide conditions that represent the current status of the Note.
	// +kubebuilder:default={{type: "Ready", status: "Unknown", reason: "Unknown", message: "Waiting for control plane to reconcile", lastTransitionTime: "1970-01-01T00:00:00Z"}}
	// +kubebuilder:validation:Optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// CreatedBy is the email of the user that created the note.
	// +kubebuilder:validation:Optional
	CreatedBy string `json:"createdBy,omitempty"`
}

// GetCreatorRef returns the creator reference for the Note
func (n *Note) GetCreatorRef() iamv1alpha1.UserReference {
	return n.Spec.CreatorRef
}

// GetNoteKind returns the kind string for Note
func (n *Note) GetNoteKind() string {
	return "Note"
}

// GetCreatorRef returns the creator reference for the ClusterNote
func (cn *ClusterNote) GetCreatorRef() iamv1alpha1.UserReference {
	return cn.Spec.CreatorRef
}

// GetNoteKind returns the kind string for ClusterNote
func (cn *ClusterNote) GetNoteKind() string {
	return "ClusterNote"
}

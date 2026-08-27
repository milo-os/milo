package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ProjectSpec defines the desired state of Project.
type ProjectSpec struct {
	// OwnerRef is a reference to the owner of the project. Must be a valid
	// resource.
	// +kubebuilder:validation:Required
	OwnerRef OwnerReference `json:"ownerRef"`
}

// ProjectSuspensionInfo contains the details of a suspension affecting the project.
type ProjectSuspensionInfo struct {
	// Reason is the category of suspension.
	// +kubebuilder:validation:Required
	Reason ProjectSuspensionReason `json:"reason"`

	// SuspendedAt is the timestamp when the suspension was created.
	// +kubebuilder:validation:Required
	SuspendedAt metav1.Time `json:"suspendedAt"`

	// ReinstateAuthority defines who can lift this suspension.
	// +kubebuilder:validation:Required
	ReinstateAuthority ProjectSuspensionReinstateAuthority `json:"reinstateAuthority"`
}

// ProjectStatus defines the observed state of Project.
type ProjectStatus struct {
	// Represents the observations of a project's current state.
	// Known condition types are: "Ready"
	// +kubebuilder:default={{type: "Ready", status: "Unknown", reason: "Unknown", message: "Waiting for control plane to reconcile", lastTransitionTime: "1970-01-01T00:00:00Z"}}
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// Suspensions lists the active/all suspensions currently affecting the project.
	// +kubebuilder:validation:Optional
	Suspensions []ProjectSuspensionInfo `json:"suspensions,omitempty"`

	// SuspensionEscalation tracks the retention-window deadline and
	// notification history for a suspended project that is pending escalation
	// to deletion. It is only present while the project is suspended and is
	// cleared once the project is reinstated.
	// +kubebuilder:validation:Optional
	SuspensionEscalation *ProjectSuspensionEscalationStatus `json:"suspensionEscalation,omitempty"`
}

// ProjectSuspensionEscalationStatus records the retention-window deadline and
// notification history for a project's pending escalation from suspension to
// deletion.
type ProjectSuspensionEscalationStatus struct {
	// DeletionAt is the time at which the project will be deleted if it
	// remains suspended. It is fixed once computed so it does not drift if
	// the configured retention window changes while the project is already
	// suspended.
	// +kubebuilder:validation:Required
	DeletionAt metav1.Time `json:"deletionAt"`

	// NotifiedDaysRemaining lists the "days until deletion" thresholds for
	// which a warning e-mail has already been sent, so that reconciliation
	// does not send duplicate notifications.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:MaxItems=64
	NotifiedDaysRemaining []int32 `json:"notifiedDaysRemaining,omitempty"`
}

const (
	// ProjectReady indicates that the project has been provisioned and is ready
	// for use.
	ProjectReady = "Ready"

	// ProjectResourceCleanup indicates that project resources are being deleted
	// as part of project teardown.
	ProjectResourceCleanup = "ResourceCleanup"

	// ProjectSuspended indicates whether the project is suspended.
	ProjectSuspended = "Suspended"

	// ProjectPendingDeletion indicates that the project is suspended past its
	// retention window grace period and will be deleted at
	// status.suspensionEscalation.deletionAt unless it is reinstated first.
	ProjectPendingDeletion = "PendingDeletion"
)

// ProjectSuspendedCause is the metav1.StatusCause Type set on the
// Details.Causes of the 403 Forbidden response returned by the
// ProjectSuspensionEnforcement admission plugin when a write is denied
// because the request's target project is suspended. API clients match on
// this value (via apierrors.StatusError.ErrStatus.Details.Causes) to
// distinguish suspension-caused denials from other admission failures
// (quota, RBAC, NamespaceLifecycle's NamespaceTerminatingCause).
const ProjectSuspendedCause metav1.CauseType = "ProjectSuspended"

const (
	// ProjectReadyReason indicates that the project is ready for use.
	ProjectReadyReason = "Ready"

	// ProjectSuspendedReason indicates that the project is suspended.
	ProjectSuspendedReason = "Suspended"

	// ProjectActiveReason indicates that the project is active.
	ProjectActiveReason = "Active"

	// ProjectProvisioningReason indicates that the project is provisioning.
	ProjectProvisioningReason = "Provisioning"

	// ProjectNameConflict indicates that the project name already exists
	ProjectNameConflictReason = "ProjectNameConflict"

	// ProjectCleanupStartedReason indicates that resource cleanup has been
	// initiated and delete commands are being issued.
	ProjectCleanupStartedReason = "CleanupStarted"

	// ProjectCleanupAwaitingCompletionReason indicates that delete commands
	// have been issued and the controller is waiting for resources to be removed.
	ProjectCleanupAwaitingCompletionReason = "CleanupAwaitingCompletion"

	// ProjectCleanupCompleteReason indicates that all project resources have
	// been deleted.
	ProjectCleanupCompleteReason = "CleanupComplete"

	// ProjectPendingDeletionReason indicates that the project's suspension
	// retention window is active and counting down to deletion.
	ProjectPendingDeletionReason = "SuspensionRetentionWindowElapsing"

	// ProjectPendingDeletionClearedReason indicates that a pending escalation
	// to deletion was cancelled because the project was reinstated.
	ProjectPendingDeletionClearedReason = "Reinstated"
)

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster

// Project is the Schema for the projects API.
// +kubebuilder:printcolumn:name="Ready",type="string",JSONPath=".status.conditions[?(@.type=='Ready')].status"
// +kubebuilder:printcolumn:name="Delete At",type="string",JSONPath=".status.suspensionEscalation.deletionAt"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"
// +kubebuilder:metadata:annotations="discovery.miloapis.com/parent-contexts=Organization"
type Project struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// +kubebuilder:validation:Required
	Spec   ProjectSpec   `json:"spec,omitempty"`
	Status ProjectStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// ProjectList contains a list of Project.
type ProjectList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Project `json:"items"`
}

// OwnerReference is a reference to the owner of the project.
type OwnerReference struct {
	// Kind is the kind of the resource.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Enum=Organization
	Kind string `json:"kind"`

	// Name is the name of the resource.
	//
	// +kubebuilder:validation:Required
	Name string `json:"name"`
}

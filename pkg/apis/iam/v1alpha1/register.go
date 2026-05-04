package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// SchemeGroupVersion is group version used to register these objects
var SchemeGroupVersion = schema.GroupVersion{Group: "iam.miloapis.com", Version: "v1alpha1"}

var (
	// SchemeBuilder initializes a scheme builder
	SchemeBuilder = runtime.NewSchemeBuilder(addKnownTypes)
	// AddToScheme is a global function that registers this API group & version to a scheme
	AddToScheme = SchemeBuilder.AddToScheme
)

// Adds the list of known types to Scheme.
func addKnownTypes(scheme *runtime.Scheme) error {
	scheme.AddKnownTypes(SchemeGroupVersion,
		&Group{},
		&GroupList{},
		&GroupMembership{},
		&GroupMembershipList{},
		&PolicyBinding{},
		&PolicyBindingList{},
		&UserInvitation{},
		&UserInvitationList{},
		&Role{},
		&RoleList{},
		&User{},
		&UserList{},
		&ProtectedResource{},
		&ProtectedResourceList{},
		&ServiceAccount{},
		&ServiceAccountList{},
		&UserPreference{},
		&UserPreferenceList{},
		&UserDeactivation{},
		&UserDeactivationList{},
		&UserInvitation{},
		&UserInvitationList{},
		&PlatformInvitation{},
		&PlatformInvitationList{},
		&PlatformAccessApproval{},
		&PlatformAccessApprovalList{},
		&PlatformAccessRejection{},
		&PlatformAccessRejectionList{},
	)
	metav1.AddToGroupVersion(scheme, SchemeGroupVersion)
	return nil
}

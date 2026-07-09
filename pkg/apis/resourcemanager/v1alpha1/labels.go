package v1alpha1

const (
	LabelNamespace = "resourcemanager.miloapis.com"

	OrganizationNameLabel = LabelNamespace + "/organization-name"
	ProjectNameLabel      = LabelNamespace + "/project-name"
	ProjectUIDLabel       = LabelNamespace + "/project-uid"
	OwnerNameLabel        = LabelNamespace + "/owner-name"

	// SubjectUserNameLabel records the name of the User subject a PolicyBinding
	// was created for. It lets a controller find and reap PolicyBindings whose
	// sole subject no longer exists once the referenced User is deleted.
	SubjectUserNameLabel = LabelNamespace + "/subject-user-name"
)

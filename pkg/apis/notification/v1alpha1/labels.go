package v1alpha1

const (
	LabelNamespace = "notification.miloapis.com"

	// NotificationKindLabel marks what kind of notification an Email
	// represents, so the frontend can group and filter a project's sent mail
	// by category without matching on each Email resource's name. Its value is
	// one of the NotificationKind* constants below.
	NotificationKindLabel = LabelNamespace + "/notification-kind"
)

// NotificationKind* values are the acceptable values for NotificationKindLabel.
const (
	// NotificationKindProjectSuspensionWarning marks an Email as a project
	// suspension deletion warning sent by the suspension escalation controller.
	NotificationKindProjectSuspensionWarning = "project-suspension-deletion-warning"
)

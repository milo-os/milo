package v1alpha1

const (
	LabelNamespace = "notification.miloapis.com"

	// NotificationKindLabel marks what kind of notification an Email
	// represents, so the frontend can group and filter a project's sent mail
	// by category without matching on each Email resource's name. Its value is
	// one of the NotificationKind* constants below.
	NotificationKindLabel = LabelNamespace + "/notification-kind"

	// CreatedDateLabel records the UTC date an Email was created, formatted
	// with CreatedDateLayout, so clients can list recent mail with a label
	// selector. The list API can't sort and Email names don't follow creation
	// order, so without it the only way to find the newest Emails is to list
	// them all. The Email mutating webhook sets it on create; Emails created
	// before the label existed are backfilled by
	// scripts/backfill-email-created-date.sh.
	CreatedDateLabel = LabelNamespace + "/created-date"

	// CreatedDateLayout is the time layout of CreatedDateLabel values.
	CreatedDateLayout = "2006-01-02"
)

// NotificationKind* values are the acceptable values for NotificationKindLabel.
const (
	// NotificationKindProjectSuspensionWarning marks an Email as a project
	// suspension deletion warning sent by the suspension escalation controller.
	NotificationKindProjectSuspensionWarning = "project-suspension-deletion-warning"
)

package v1alpha1

import (
	"fmt"
	"strings"
)

const (
	// OrganizationCreatorUserUIDAnnotation stores the creating user's Milo User
	// resource name (UID) during unified org create when the final organization
	// name is not yet assigned at admission time.
	OrganizationCreatorUserUIDAnnotation = "resourcemanager.miloapis.com/creator-user-uid"
)

// OrganizationNamespace returns the namespace used for organization-scoped resources.
func OrganizationNamespace(orgName string) string {
	return fmt.Sprintf("organization-%s", orgName)
}

// IsOrganizationContactInfoComplete reports whether required org contact fields are set.
func IsOrganizationContactInfoComplete(info *OrganizationContactInfo) bool {
	if info == nil {
		return false
	}
	return strings.TrimSpace(info.Email) != "" && strings.TrimSpace(info.Name) != ""
}

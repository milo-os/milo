package app

import "fmt"

// ownerRoleNamespaceWarnings reports owner roles that organization-scope
// callers cannot assign.
//
// In organization context, the OrganizationMembership, UserInvitation,
// PolicyBinding, and Organization (spec.emailDomainJoining) webhooks accept
// only roles from the assignable-roles namespace. When an owner role lives
// elsewhere, the organization creator still receives it, because a controller
// grants it, but no owner can grant it to another member through the
// organization API. A sole owner then cannot hand over ownership or leave the
// organization without a platform administrator.
func ownerRoleNamespaceWarnings(orgOwnerNamespace, projectOwnerNamespace, assignableNamespace string) []string {
	var warnings []string
	check := func(flag, namespace string) {
		if namespace == assignableNamespace {
			return
		}
		warnings = append(warnings, fmt.Sprintf(
			"--%s is %q but --assignable-roles-namespace is %q: organization-scope callers cannot assign the owner role, "+
				"so owners cannot add other owners through the organization API. "+
				"Set both flags to the same namespace, for example --%s=%s.",
			flag, namespace, assignableNamespace, flag, assignableNamespace,
		))
	}
	check("organization-owner-role-namespace", orgOwnerNamespace)
	check("project-owner-role-namespace", projectOwnerNamespace)
	return warnings
}

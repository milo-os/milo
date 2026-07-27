# Issue: Prevent Deletion of the Last User/Owner of an Organization or Project

## Problem
Currently, users are allowed to delete their `User` resource without any validation preventing it, even if they are the last remaining owner or member of an active organization or project. This leaves the organization/project in a stale, "orphaned" state with no owners, meaning it cannot be managed or deleted by anyone other than a platform administrator.

This issue was identified during triage of a `PolicyBindingsNotReady` alert caused by a user deletion that orphaned an organization's resources.

---

## Root Cause
1. We already have a webhook guarding `OrganizationMembership` to prevent removing or updating the last owner role from an organization.
2. However, when a `User` resource is deleted directly:
   - The `UserController` runs its membership cleanup finalizer (`iam.miloapis.com/user-membership-cleanup`), which attempts to delete all `OrganizationMembership` resources referencing that user.
   - The `OrganizationMembership` validation webhook has a bypass rule allowing deletions if the referenced user has a non-zero `DeletionTimestamp` or no longer exists in the API server. This bypass exists to prevent the `User` resource deletion from hanging on finalization.
   - Because the bypass allows the memberships to be deleted, the user deletion succeeds, leaving the organization with zero owners/members.

---

## Suggested Fix
We should prevent this by validating user deletion at the `User` validation stage:

1. **Enable Deletion Webhook for Users**:
   Update the validating webhook marker in [user_webhook.go](file:///Users/joseszycho/milo/internal/webhooks/iam/v1alpha1/user_webhook.go) to include the `delete` verb:
   ```go
   // +kubebuilder:webhook:path=/validate-iam-miloapis-com-v1alpha1-user,mutating=false,failurePolicy=fail,sideEffects=NoneOnDryRun,groups=iam.miloapis.com,resources=users,verbs=create;update;delete,versions=v1alpha1,name=vuser.iam.miloapis.com,admissionReviewVersions={v1,v1beta1},serviceName=milo-controller-manager,servicePort=9443,serviceNamespace=milo-system
   ```

2. **Implement Delete Validation Logic**:
   Implement `ValidateDelete` in the `UserValidator`:
   - Query all `OrganizationMembership` resources referencing this user.
   - Identify the organizations/projects where the user is currently an owner.
   - For each of these organizations, check if there are other active owners/users.
   - If this user is the last owner of any active organization/project, reject the deletion.
   - The validation error should instruct the user to either:
     - Transfer ownership/owner role to another active user.
     - Delete the organization/project entirely before deleting their user account.

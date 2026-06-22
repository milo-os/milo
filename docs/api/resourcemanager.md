# API Reference

Packages:

- [resourcemanager.miloapis.com/v1alpha1](#resourcemanagermiloapiscomv1alpha1)

# resourcemanager.miloapis.com/v1alpha1

Resource Types:

- [OrganizationMembership](#organizationmembership)

- [Organization](#organization)

- [Project](#project)




## OrganizationMembership
<sup><sup>[↩ Parent](#resourcemanagermiloapiscomv1alpha1 )</sup></sup>







OrganizationMembership establishes a user's membership in an organization and
optionally assigns roles to grant permissions. The controller automatically
manages PolicyBinding resources for each assigned role, simplifying access
control management.

Key features:
  - Establishes user-organization relationship
  - Automatic PolicyBinding creation and deletion for assigned roles
  - Supports multiple roles per membership
  - Cross-namespace role references
  - Detailed status tracking with per-role reconciliation state

Prerequisites:
  - User resource must exist
  - Organization resource must exist
  - Referenced Role resources must exist in their respective namespaces

Example - Basic membership with role assignment:

	apiVersion: resourcemanager.miloapis.com/v1alpha1
	kind: OrganizationMembership
	metadata:
	  name: jane-acme-membership
	  namespace: organization-acme-corp
	spec:
	  organizationRef:
	    name: acme-corp
	  userRef:
	    name: jane-doe
	  roles:
	  - name: organization-viewer
	    namespace: organization-acme-corp

Related resources:
  - User: The user being granted membership
  - Organization: The organization the user joins
  - Role: Defines permissions granted to the user
  - PolicyBinding: Automatically created by the controller for each role

<table>
    <thead>
        <tr>
            <th>Name</th>
            <th>Type</th>
            <th>Description</th>
            <th>Required</th>
        </tr>
    </thead>
    <tbody><tr>
      <td><b>apiVersion</b></td>
      <td>string</td>
      <td>resourcemanager.miloapis.com/v1alpha1</td>
      <td>true</td>
      </tr>
      <tr>
      <td><b>kind</b></td>
      <td>string</td>
      <td>OrganizationMembership</td>
      <td>true</td>
      </tr>
      <tr>
      <td><b><a href="https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.27/#objectmeta-v1-meta">metadata</a></b></td>
      <td>object</td>
      <td>Refer to the Kubernetes API documentation for the fields of the `metadata` field.</td>
      <td>true</td>
      </tr><tr>
        <td><b><a href="#organizationmembershipspec">spec</a></b></td>
        <td>object</td>
        <td>
          OrganizationMembershipSpec defines the desired state of OrganizationMembership.
It specifies which user should be a member of which organization, and optionally
which roles should be assigned to grant permissions.<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b><a href="#organizationmembershipstatus">status</a></b></td>
        <td>object</td>
        <td>
          OrganizationMembershipStatus defines the observed state of OrganizationMembership.
The controller populates this status to reflect the current reconciliation state,
including whether the membership is ready and which roles have been successfully applied.<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### OrganizationMembership.spec
<sup><sup>[↩ Parent](#organizationmembership)</sup></sup>



OrganizationMembershipSpec defines the desired state of OrganizationMembership.
It specifies which user should be a member of which organization, and optionally
which roles should be assigned to grant permissions.

<table>
    <thead>
        <tr>
            <th>Name</th>
            <th>Type</th>
            <th>Description</th>
            <th>Required</th>
        </tr>
    </thead>
    <tbody><tr>
        <td><b><a href="#organizationmembershipspecorganizationref">organizationRef</a></b></td>
        <td>object</td>
        <td>
          OrganizationRef identifies the organization to grant membership in.
The organization must exist before creating the membership.

Required field.<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b><a href="#organizationmembershipspecuserref">userRef</a></b></td>
        <td>object</td>
        <td>
          UserRef identifies the user to grant organization membership.
The user must exist before creating the membership.

Required field.<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b><a href="#organizationmembershipspecrolesindex">roles</a></b></td>
        <td>[]object</td>
        <td>
          Roles specifies a list of roles to assign to the user within the organization.
The controller automatically creates and manages PolicyBinding resources for
each role. Roles can be added or removed after the membership is created.

Optional field. When omitted or empty, the membership is established without
any role assignments. Roles can be added later via update operations.

Each role reference must specify:
  - name: The role name (required)
  - namespace: The role namespace (optional, defaults to membership namespace)

Duplicate roles are prevented by admission webhook validation.

Example:

  roles:
  - name: organization-admin
    namespace: organization-acme-corp
  - name: billing-manager
    namespace: organization-acme-corp
  - name: shared-developer
    namespace: milo-system<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### OrganizationMembership.spec.organizationRef
<sup><sup>[↩ Parent](#organizationmembershipspec)</sup></sup>



OrganizationRef identifies the organization to grant membership in.
The organization must exist before creating the membership.

Required field.

<table>
    <thead>
        <tr>
            <th>Name</th>
            <th>Type</th>
            <th>Description</th>
            <th>Required</th>
        </tr>
    </thead>
    <tbody><tr>
        <td><b>name</b></td>
        <td>string</td>
        <td>
          Name is the name of resource being referenced<br/>
        </td>
        <td>true</td>
      </tr></tbody>
</table>


### OrganizationMembership.spec.userRef
<sup><sup>[↩ Parent](#organizationmembershipspec)</sup></sup>



UserRef identifies the user to grant organization membership.
The user must exist before creating the membership.

Required field.

<table>
    <thead>
        <tr>
            <th>Name</th>
            <th>Type</th>
            <th>Description</th>
            <th>Required</th>
        </tr>
    </thead>
    <tbody><tr>
        <td><b>name</b></td>
        <td>string</td>
        <td>
          Name is the name of resource being referenced<br/>
        </td>
        <td>true</td>
      </tr></tbody>
</table>


### OrganizationMembership.spec.roles[index]
<sup><sup>[↩ Parent](#organizationmembershipspec)</sup></sup>



RoleReference defines a reference to a Role resource for organization membership.

<table>
    <thead>
        <tr>
            <th>Name</th>
            <th>Type</th>
            <th>Description</th>
            <th>Required</th>
        </tr>
    </thead>
    <tbody><tr>
        <td><b>name</b></td>
        <td>string</td>
        <td>
          Name of the referenced Role.<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>namespace</b></td>
        <td>string</td>
        <td>
          Namespace of the referenced Role.
If not specified, it defaults to the organization membership's namespace.<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### OrganizationMembership.status
<sup><sup>[↩ Parent](#organizationmembership)</sup></sup>



OrganizationMembershipStatus defines the observed state of OrganizationMembership.
The controller populates this status to reflect the current reconciliation state,
including whether the membership is ready and which roles have been successfully applied.

<table>
    <thead>
        <tr>
            <th>Name</th>
            <th>Type</th>
            <th>Description</th>
            <th>Required</th>
        </tr>
    </thead>
    <tbody><tr>
        <td><b><a href="#organizationmembershipstatusappliedrolesindex">appliedRoles</a></b></td>
        <td>[]object</td>
        <td>
          AppliedRoles tracks the reconciliation state of each role in spec.roles.
This array provides per-role status, making it easy to identify which
roles are applied and which failed.

Each entry includes:
  - name and namespace: Identifies the role
  - status: "Applied", "Pending", or "Failed"
  - policyBindingRef: Reference to the created PolicyBinding (when Applied)
  - appliedAt: Timestamp when role was applied (when Applied)
  - message: Error details (when Failed)

Use this to troubleshoot role assignment issues. Roles marked as "Failed"
include a message explaining why the PolicyBinding could not be created.

Example:

  appliedRoles:
  - name: org-admin
    namespace: organization-acme-corp
    status: Applied
    appliedAt: "2025-10-28T10:00:00Z"
    policyBindingRef:
      name: jane-acme-membership-a1b2c3d4
      namespace: organization-acme-corp
  - name: invalid-role
    namespace: organization-acme-corp
    status: Failed
    message: "role 'invalid-role' not found in namespace 'organization-acme-corp'"<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b><a href="#organizationmembershipstatusconditionsindex">conditions</a></b></td>
        <td>[]object</td>
        <td>
          Conditions represent the current status of the membership.

Standard conditions:
  - Ready: Indicates membership has been established (user and org exist)
  - RolesApplied: Indicates whether all roles have been successfully applied

Check the RolesApplied condition to determine overall role assignment status:
  - True with reason "AllRolesApplied": All roles successfully applied
  - True with reason "NoRolesSpecified": No roles in spec, membership only
  - False with reason "PartialRolesApplied": Some roles failed (check appliedRoles for details)<br/>
          <br/>
            <i>Default</i>: [map[lastTransitionTime:1970-01-01T00:00:00Z message:Waiting for control plane to reconcile reason:Unknown status:Unknown type:Ready]]<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>observedGeneration</b></td>
        <td>integer</td>
        <td>
          ObservedGeneration tracks the most recent membership spec that the
controller has processed. Use this to determine if status reflects
the latest changes.<br/>
          <br/>
            <i>Format</i>: int64<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b><a href="#organizationmembershipstatusorganization">organization</a></b></td>
        <td>object</td>
        <td>
          Organization contains cached information about the organization in this membership.
This information is populated by the controller from the referenced organization.<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b><a href="#organizationmembershipstatususer">user</a></b></td>
        <td>object</td>
        <td>
          User contains cached information about the user in this membership.
This information is populated by the controller from the referenced user.<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### OrganizationMembership.status.appliedRoles[index]
<sup><sup>[↩ Parent](#organizationmembershipstatus)</sup></sup>



AppliedRole tracks the reconciliation status of a single role assignment
within an organization membership. The controller maintains this status to
provide visibility into which roles are successfully applied and which failed.

<table>
    <thead>
        <tr>
            <th>Name</th>
            <th>Type</th>
            <th>Description</th>
            <th>Required</th>
        </tr>
    </thead>
    <tbody><tr>
        <td><b>name</b></td>
        <td>string</td>
        <td>
          Name identifies the Role resource.

Required field.<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>status</b></td>
        <td>enum</td>
        <td>
          Status indicates the current state of this role assignment.

Valid values:
  - "Applied": PolicyBinding successfully created and role is active
  - "Pending": Role is being reconciled (transitional state)
  - "Failed": PolicyBinding could not be created (see Message for details)

Required field.<br/>
          <br/>
            <i>Enum</i>: Applied, Pending, Failed<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>appliedAt</b></td>
        <td>string</td>
        <td>
          AppliedAt records when this role was successfully applied.
Corresponds to the PolicyBinding creation time.

Only populated when Status is "Applied".<br/>
          <br/>
            <i>Format</i>: date-time<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>message</b></td>
        <td>string</td>
        <td>
          Message provides additional context about the role status.
Contains error details when Status is "Failed", explaining why the
PolicyBinding could not be created.

Common failure messages:
  - "role 'role-name' not found in namespace 'namespace'"
  - "Failed to create PolicyBinding: <error details>"

Empty when Status is "Applied" or "Pending".<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>namespace</b></td>
        <td>string</td>
        <td>
          Namespace identifies the namespace containing the Role resource.
Empty when the role is in the membership's namespace.<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b><a href="#organizationmembershipstatusappliedrolesindexpolicybindingref">policyBindingRef</a></b></td>
        <td>object</td>
        <td>
          PolicyBindingRef references the PolicyBinding resource that was
automatically created for this role.

Only populated when Status is "Applied". Use this reference to
inspect or troubleshoot the underlying PolicyBinding.<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### OrganizationMembership.status.appliedRoles[index].policyBindingRef
<sup><sup>[↩ Parent](#organizationmembershipstatusappliedrolesindex)</sup></sup>



PolicyBindingRef references the PolicyBinding resource that was
automatically created for this role.

Only populated when Status is "Applied". Use this reference to
inspect or troubleshoot the underlying PolicyBinding.

<table>
    <thead>
        <tr>
            <th>Name</th>
            <th>Type</th>
            <th>Description</th>
            <th>Required</th>
        </tr>
    </thead>
    <tbody><tr>
        <td><b>name</b></td>
        <td>string</td>
        <td>
          Name of the PolicyBinding resource.<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>namespace</b></td>
        <td>string</td>
        <td>
          Namespace of the PolicyBinding resource.<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### OrganizationMembership.status.conditions[index]
<sup><sup>[↩ Parent](#organizationmembershipstatus)</sup></sup>



Condition contains details for one aspect of the current state of this API Resource.

<table>
    <thead>
        <tr>
            <th>Name</th>
            <th>Type</th>
            <th>Description</th>
            <th>Required</th>
        </tr>
    </thead>
    <tbody><tr>
        <td><b>lastTransitionTime</b></td>
        <td>string</td>
        <td>
          lastTransitionTime is the last time the condition transitioned from one status to another.
This should be when the underlying condition changed.  If that is not known, then using the time when the API field changed is acceptable.<br/>
          <br/>
            <i>Format</i>: date-time<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>message</b></td>
        <td>string</td>
        <td>
          message is a human readable message indicating details about the transition.
This may be an empty string.<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>reason</b></td>
        <td>string</td>
        <td>
          reason contains a programmatic identifier indicating the reason for the condition's last transition.
Producers of specific condition types may define expected values and meanings for this field,
and whether the values are considered a guaranteed API.
The value should be a CamelCase string.
This field may not be empty.<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>status</b></td>
        <td>enum</td>
        <td>
          status of the condition, one of True, False, Unknown.<br/>
          <br/>
            <i>Enum</i>: True, False, Unknown<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>type</b></td>
        <td>string</td>
        <td>
          type of condition in CamelCase or in foo.example.com/CamelCase.<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>observedGeneration</b></td>
        <td>integer</td>
        <td>
          observedGeneration represents the .metadata.generation that the condition was set based upon.
For instance, if .metadata.generation is currently 12, but the .status.conditions[x].observedGeneration is 9, the condition is out of date
with respect to the current state of the instance.<br/>
          <br/>
            <i>Format</i>: int64<br/>
            <i>Minimum</i>: 0<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### OrganizationMembership.status.organization
<sup><sup>[↩ Parent](#organizationmembershipstatus)</sup></sup>



Organization contains cached information about the organization in this membership.
This information is populated by the controller from the referenced organization.

<table>
    <thead>
        <tr>
            <th>Name</th>
            <th>Type</th>
            <th>Description</th>
            <th>Required</th>
        </tr>
    </thead>
    <tbody><tr>
        <td><b>displayName</b></td>
        <td>string</td>
        <td>
          DisplayName is the display name of the organization in the membership.<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>type</b></td>
        <td>string</td>
        <td>
          Type is the type of the organization in the membership.<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### OrganizationMembership.status.user
<sup><sup>[↩ Parent](#organizationmembershipstatus)</sup></sup>



User contains cached information about the user in this membership.
This information is populated by the controller from the referenced user.

<table>
    <thead>
        <tr>
            <th>Name</th>
            <th>Type</th>
            <th>Description</th>
            <th>Required</th>
        </tr>
    </thead>
    <tbody><tr>
        <td><b>avatarUrl</b></td>
        <td>string</td>
        <td>
          AvatarURL is the avatar URL of the user in the membership.<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>email</b></td>
        <td>string</td>
        <td>
          Email is the email of the user in the membership.<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>familyName</b></td>
        <td>string</td>
        <td>
          FamilyName is the family name of the user in the membership.<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>givenName</b></td>
        <td>string</td>
        <td>
          GivenName is the given name of the user in the membership.<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>

## Organization
<sup><sup>[↩ Parent](#resourcemanagermiloapiscomv1alpha1 )</sup></sup>





Use lowercase for path, which influences plural name. Ensure kind is Organization.
Organization is the Schema for the Organizations API

<table>
    <thead>
        <tr>
            <th>Name</th>
            <th>Type</th>
            <th>Description</th>
            <th>Required</th>
        </tr>
    </thead>
    <tbody><tr>
      <td><b>apiVersion</b></td>
      <td>string</td>
      <td>resourcemanager.miloapis.com/v1alpha1</td>
      <td>true</td>
      </tr>
      <tr>
      <td><b>kind</b></td>
      <td>string</td>
      <td>Organization</td>
      <td>true</td>
      </tr>
      <tr>
      <td><b><a href="https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.27/#objectmeta-v1-meta">metadata</a></b></td>
      <td>object</td>
      <td>Refer to the Kubernetes API documentation for the fields of the `metadata` field.</td>
      <td>true</td>
      </tr><tr>
        <td><b><a href="#organizationspec">spec</a></b></td>
        <td>object</td>
        <td>
          OrganizationSpec defines the desired state of Organization<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b><a href="#organizationstatus">status</a></b></td>
        <td>object</td>
        <td>
          OrganizationStatus defines the observed state of Organization<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### Organization.spec
<sup><sup>[↩ Parent](#organization)</sup></sup>



OrganizationSpec defines the desired state of Organization

<table>
    <thead>
        <tr>
            <th>Name</th>
            <th>Type</th>
            <th>Description</th>
            <th>Required</th>
        </tr>
    </thead>
    <tbody><tr>
        <td><b>type</b></td>
        <td>enum</td>
        <td>
          The type of organization.<br/>
          <br/>
            <i>Validations</i>:<li>type(oldSelf) == null_type || self == oldSelf: organization type is immutable</li>
            <i>Enum</i>: Personal, Standard<br/>
        </td>
        <td>true</td>
      </tr></tbody>
</table>


### Organization.status
<sup><sup>[↩ Parent](#organization)</sup></sup>



OrganizationStatus defines the observed state of Organization

<table>
    <thead>
        <tr>
            <th>Name</th>
            <th>Type</th>
            <th>Description</th>
            <th>Required</th>
        </tr>
    </thead>
    <tbody><tr>
        <td><b><a href="#organizationstatusconditionsindex">conditions</a></b></td>
        <td>[]object</td>
        <td>
          Conditions represents the observations of an organization's current state.
Known condition types are: "Ready"<br/>
          <br/>
            <i>Default</i>: [map[lastTransitionTime:1970-01-01T00:00:00Z message:Waiting for control plane to reconcile reason:Unknown status:Unknown type:Ready]]<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>observedGeneration</b></td>
        <td>integer</td>
        <td>
          ObservedGeneration is the most recent generation observed for this Organization by the controller.<br/>
          <br/>
            <i>Format</i>: int64<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### Organization.status.conditions[index]
<sup><sup>[↩ Parent](#organizationstatus)</sup></sup>



Condition contains details for one aspect of the current state of this API Resource.

<table>
    <thead>
        <tr>
            <th>Name</th>
            <th>Type</th>
            <th>Description</th>
            <th>Required</th>
        </tr>
    </thead>
    <tbody><tr>
        <td><b>lastTransitionTime</b></td>
        <td>string</td>
        <td>
          lastTransitionTime is the last time the condition transitioned from one status to another.
This should be when the underlying condition changed.  If that is not known, then using the time when the API field changed is acceptable.<br/>
          <br/>
            <i>Format</i>: date-time<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>message</b></td>
        <td>string</td>
        <td>
          message is a human readable message indicating details about the transition.
This may be an empty string.<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>reason</b></td>
        <td>string</td>
        <td>
          reason contains a programmatic identifier indicating the reason for the condition's last transition.
Producers of specific condition types may define expected values and meanings for this field,
and whether the values are considered a guaranteed API.
The value should be a CamelCase string.
This field may not be empty.<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>status</b></td>
        <td>enum</td>
        <td>
          status of the condition, one of True, False, Unknown.<br/>
          <br/>
            <i>Enum</i>: True, False, Unknown<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>type</b></td>
        <td>string</td>
        <td>
          type of condition in CamelCase or in foo.example.com/CamelCase.<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>observedGeneration</b></td>
        <td>integer</td>
        <td>
          observedGeneration represents the .metadata.generation that the condition was set based upon.
For instance, if .metadata.generation is currently 12, but the .status.conditions[x].observedGeneration is 9, the condition is out of date
with respect to the current state of the instance.<br/>
          <br/>
            <i>Format</i>: int64<br/>
            <i>Minimum</i>: 0<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>

## Project
<sup><sup>[↩ Parent](#resourcemanagermiloapiscomv1alpha1 )</sup></sup>





Project is the Schema for the projects API.

<table>
    <thead>
        <tr>
            <th>Name</th>
            <th>Type</th>
            <th>Description</th>
            <th>Required</th>
        </tr>
    </thead>
    <tbody><tr>
      <td><b>apiVersion</b></td>
      <td>string</td>
      <td>resourcemanager.miloapis.com/v1alpha1</td>
      <td>true</td>
      </tr>
      <tr>
      <td><b>kind</b></td>
      <td>string</td>
      <td>Project</td>
      <td>true</td>
      </tr>
      <tr>
      <td><b><a href="https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.27/#objectmeta-v1-meta">metadata</a></b></td>
      <td>object</td>
      <td>Refer to the Kubernetes API documentation for the fields of the `metadata` field.</td>
      <td>true</td>
      </tr><tr>
        <td><b><a href="#projectspec">spec</a></b></td>
        <td>object</td>
        <td>
          ProjectSpec defines the desired state of Project.<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b><a href="#projectstatus">status</a></b></td>
        <td>object</td>
        <td>
          ProjectStatus defines the observed state of Project.<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### Project.spec
<sup><sup>[↩ Parent](#project)</sup></sup>



ProjectSpec defines the desired state of Project.

<table>
    <thead>
        <tr>
            <th>Name</th>
            <th>Type</th>
            <th>Description</th>
            <th>Required</th>
        </tr>
    </thead>
    <tbody><tr>
        <td><b><a href="#projectspecownerref">ownerRef</a></b></td>
        <td>object</td>
        <td>
          OwnerRef is a reference to the owner of the project. Must be a valid
resource.<br/>
        </td>
        <td>true</td>
      </tr></tbody>
</table>


### Project.spec.ownerRef
<sup><sup>[↩ Parent](#projectspec)</sup></sup>



OwnerRef is a reference to the owner of the project. Must be a valid
resource.

<table>
    <thead>
        <tr>
            <th>Name</th>
            <th>Type</th>
            <th>Description</th>
            <th>Required</th>
        </tr>
    </thead>
    <tbody><tr>
        <td><b>kind</b></td>
        <td>enum</td>
        <td>
          Kind is the kind of the resource.<br/>
          <br/>
            <i>Enum</i>: Organization<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>name</b></td>
        <td>string</td>
        <td>
          Name is the name of the resource.<br/>
        </td>
        <td>true</td>
      </tr></tbody>
</table>


### Project.status
<sup><sup>[↩ Parent](#project)</sup></sup>



ProjectStatus defines the observed state of Project.

<table>
    <thead>
        <tr>
            <th>Name</th>
            <th>Type</th>
            <th>Description</th>
            <th>Required</th>
        </tr>
    </thead>
    <tbody><tr>
        <td><b><a href="#projectstatusconditionsindex">conditions</a></b></td>
        <td>[]object</td>
        <td>
          Represents the observations of a project's current state.
Known condition types are: "Ready" and "ResourceCleanup".
<br/>
<i>Default</i>: [map[lastTransitionTime:1970-01-01T00:00:00Z message:Waiting for control plane to reconcile reason:Unknown status:Unknown type:Ready]]<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### Project.status.conditions[index]
<sup><sup>[↩ Parent](#projectstatus)</sup></sup>



Condition contains details for one aspect of the current state of this API Resource.

For Project resources, known condition types include:
- **Ready**: Indicates project and infrastructure are ready for use. See the Reason for additional details.
- **ResourceCleanup**: Indicates progress and completion of project resource deletion when the project is being deleted. This condition is managed by the controller to track cleanup status.

For the **ResourceCleanup** condition, the following values are used:
- **status**: 
  - `True` when cleanup is ongoing,
  - `False` when cleanup is complete.
- **reason**:
  - `CleanupStarted`: Resource cleanup (deletion) has started; delete commands are being issued.
  - `CleanupAwaitingCompletion`: Cleanup commands have been issued and the controller is waiting for resources to be removed.
  - `CleanupComplete`: All resources have been deleted and finalizer will be removed, completing project deletion.

Example:
```
status:
  conditions:
  - type: ResourceCleanup
    status: "False"
    reason: CleanupComplete
    message: Project resources have been deleted
```

<table>
    <thead>
        <tr>
            <th>Name</th>
            <th>Type</th>
            <th>Description</th>
            <th>Required</th>
        </tr>
    </thead>
    <tbody><tr>
        <td><b>lastTransitionTime</b></td>
        <td>string</td>
        <td>
          lastTransitionTime is the last time the condition transitioned from one status to another.
This should be when the underlying condition changed. If that is not known, then using the time when the API field changed is acceptable.<br/>
          <br/>
            <i>Format</i>: date-time<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>message</b></td>
        <td>string</td>
        <td>
          message is a human readable message indicating details about the transition.
This may be an empty string.<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>reason</b></td>
        <td>string</td>
        <td>
          reason contains a programmatic identifier indicating the reason for the condition's last transition.
Producers of specific condition types may define expected values and meanings for this field,
and whether the values are considered a guaranteed API.
The value should be a CamelCase string.
This field may not be empty.<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>status</b></td>
        <td>enum</td>
        <td>
          status of the condition, one of True, False, Unknown.<br/>
          <br/>
            <i>Enum</i>: True, False, Unknown<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>type</b></td>
        <td>string</td>
        <td>
          type of condition in CamelCase or in foo.example.com/CamelCase.<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>observedGeneration</b></td>
        <td>integer</td>
        <td>
          observedGeneration represents the .metadata.generation that the condition was set based upon.
For instance, if .metadata.generation is currently 12, but the .status.conditions[x].observedGeneration is 9, the condition is out of date
with respect to the current state of the instance.<br/>
          <br/>
            <i>Format</i>: int64<br/>
            <i>Minimum</i>: 0<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>
 
# API Reference

Packages:

- [iam.miloapis.com/v1alpha1](#iammiloapiscomv1alpha1)

# iam.miloapis.com/v1alpha1

Resource Types:

- [GroupMembership](#groupmembership)

- [Group](#group)

- [ServiceAccount](#serviceaccount)

- [PlatformAccessApproval](#platformaccessapproval)

- [PlatformAccessRejection](#platformaccessrejection)

- [PlatformInvitation](#platforminvitation)

- [PolicyBinding](#policybinding)

- [ProtectedResource](#protectedresource)

- [Role](#role)

- [UserDeactivation](#userdeactivation)

- [UserInvitation](#userinvitation)

- [UserPreference](#userpreference)

- [User](#user)




## GroupMembership
<sup><sup>[↩ Parent](#iammiloapiscomv1alpha1 )</sup></sup>







GroupMembership is the Schema for the groupmemberships API

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
      <td>iam.miloapis.com/v1alpha1</td>
      <td>true</td>
      </tr>
      <tr>
      <td><b>kind</b></td>
      <td>string</td>
      <td>GroupMembership</td>
      <td>true</td>
      </tr>
      <tr>
      <td><b><a href="https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.27/#objectmeta-v1-meta">metadata</a></b></td>
      <td>object</td>
      <td>Refer to the Kubernetes API documentation for the fields of the `metadata` field.</td>
      <td>true</td>
      </tr><tr>
        <td><b><a href="#groupmembershipspec">spec</a></b></td>
        <td>object</td>
        <td>
          GroupMembershipSpec defines the desired state of GroupMembership<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b><a href="#groupmembershipstatus">status</a></b></td>
        <td>object</td>
        <td>
          GroupMembershipStatus defines the observed state of GroupMembership<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### GroupMembership.spec
<sup><sup>[↩ Parent](#groupmembership)</sup></sup>



GroupMembershipSpec defines the desired state of GroupMembership

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
        <td><b><a href="#groupmembershipspecgroupref">groupRef</a></b></td>
        <td>object</td>
        <td>
          GroupRef is a reference to the Group.
Group is a namespaced resource.<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b><a href="#groupmembershipspecuserref">userRef</a></b></td>
        <td>object</td>
        <td>
          UserRef is a reference to the User that is a member of the Group.
User is a cluster-scoped resource.<br/>
        </td>
        <td>true</td>
      </tr></tbody>
</table>


### GroupMembership.spec.groupRef
<sup><sup>[↩ Parent](#groupmembershipspec)</sup></sup>



GroupRef is a reference to the Group.
Group is a namespaced resource.

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
          Name is the name of the Group being referenced.<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>namespace</b></td>
        <td>string</td>
        <td>
          Namespace of the referenced Group.<br/>
        </td>
        <td>true</td>
      </tr></tbody>
</table>


### GroupMembership.spec.userRef
<sup><sup>[↩ Parent](#groupmembershipspec)</sup></sup>



UserRef is a reference to the User that is a member of the Group.
User is a cluster-scoped resource.

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
          Name is the name of the User being referenced.<br/>
        </td>
        <td>true</td>
      </tr></tbody>
</table>


### GroupMembership.status
<sup><sup>[↩ Parent](#groupmembership)</sup></sup>



GroupMembershipStatus defines the observed state of GroupMembership

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
        <td><b><a href="#groupmembershipstatusconditionsindex">conditions</a></b></td>
        <td>[]object</td>
        <td>
          Conditions represent the latest available observations of an object's current state.<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### GroupMembership.status.conditions[index]
<sup><sup>[↩ Parent](#groupmembershipstatus)</sup></sup>



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

## Group
<sup><sup>[↩ Parent](#iammiloapiscomv1alpha1 )</sup></sup>







Group is the Schema for the groups API

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
      <td>iam.miloapis.com/v1alpha1</td>
      <td>true</td>
      </tr>
      <tr>
      <td><b>kind</b></td>
      <td>string</td>
      <td>Group</td>
      <td>true</td>
      </tr>
      <tr>
      <td><b><a href="https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.27/#objectmeta-v1-meta">metadata</a></b></td>
      <td>object</td>
      <td>Refer to the Kubernetes API documentation for the fields of the `metadata` field.</td>
      <td>true</td>
      </tr><tr>
        <td><b><a href="#groupstatus">status</a></b></td>
        <td>object</td>
        <td>
          GroupStatus defines the observed state of Group<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### Group.status
<sup><sup>[↩ Parent](#group)</sup></sup>



GroupStatus defines the observed state of Group

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
        <td><b><a href="#groupstatusconditionsindex">conditions</a></b></td>
        <td>[]object</td>
        <td>
          Conditions represent the latest available observations of an object's current state.<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### Group.status.conditions[index]
<sup><sup>[↩ Parent](#groupstatus)</sup></sup>



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

## ServiceAccount
<sup><sup>[↩ Parent](#iammiloapiscomv1alpha1 )</sup></sup>







ServiceAccount is the Schema for the service accounts API

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
      <td>iam.miloapis.com/v1alpha1</td>
      <td>true</td>
      </tr>
      <tr>
      <td><b>kind</b></td>
      <td>string</td>
      <td>ServiceAccount</td>
      <td>true</td>
      </tr>
      <tr>
      <td><b><a href="https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.27/#objectmeta-v1-meta">metadata</a></b></td>
      <td>object</td>
      <td>Refer to the Kubernetes API documentation for the fields of the `metadata` field.</td>
      <td>true</td>
      </tr><tr>
        <td><b><a href="#serviceaccountspec">spec</a></b></td>
        <td>object</td>
        <td>
          ServiceAccountSpec defines the desired state of ServiceAccount<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b><a href="#serviceaccountstatus">status</a></b></td>
        <td>object</td>
        <td>
          ServiceAccountStatus defines the observed state of ServiceAccount<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### ServiceAccount.spec
<sup><sup>[↩ Parent](#serviceaccount)</sup></sup>



ServiceAccountSpec defines the desired state of ServiceAccount

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
        <td><b>state</b></td>
        <td>enum</td>
        <td>
          The state of the service account. This state can be safely changed as needed.
States:
  - Active: The service account can be used to authenticate.
  - Inactive: The service account is prohibited to be used to authenticate, and revokes all existing sessions.<br/>
          <br/>
            <i>Enum</i>: Active, Inactive<br/>
            <i>Default</i>: Active<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### ServiceAccount.status
<sup><sup>[↩ Parent](#serviceaccount)</sup></sup>



ServiceAccountStatus defines the observed state of ServiceAccount

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
        <td><b><a href="#serviceaccountstatusconditionsindex">conditions</a></b></td>
        <td>[]object</td>
        <td>
          Conditions provide conditions that represent the current status of the ServiceAccount.<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>email</b></td>
        <td>string</td>
        <td>
          The computed email of the service account following the pattern:
{metadata.name}@{metadata.namespace}.{project.metadata.name}.{global-suffix}<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>state</b></td>
        <td>enum</td>
        <td>
          State represents the current activation state of the service account from the auth provider.
This field tracks the state from the previous generation and is updated when state changes
are successfully propagated to the auth provider. It helps optimize performance by only
updating the auth provider when a state change is detected.<br/>
          <br/>
            <i>Enum</i>: Active, Inactive<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### ServiceAccount.status.conditions[index]
<sup><sup>[↩ Parent](#serviceaccountstatus)</sup></sup>



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

## PlatformAccessApproval
<sup><sup>[↩ Parent](#iammiloapiscomv1alpha1 )</sup></sup>






PlatformAccessApproval is the Schema for the platformaccessapprovals API.
It represents a platform access approval for a user. Once the platform access approval is created, an email will be sent to the user.

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
      <td>iam.miloapis.com/v1alpha1</td>
      <td>true</td>
      </tr>
      <tr>
      <td><b>kind</b></td>
      <td>string</td>
      <td>PlatformAccessApproval</td>
      <td>true</td>
      </tr>
      <tr>
      <td><b><a href="https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.27/#objectmeta-v1-meta">metadata</a></b></td>
      <td>object</td>
      <td>Refer to the Kubernetes API documentation for the fields of the `metadata` field.</td>
      <td>true</td>
      </tr><tr>
        <td><b><a href="#platformaccessapprovalspec">spec</a></b></td>
        <td>object</td>
        <td>
          PlatformAccessApprovalSpec defines the desired state of PlatformAccessApproval.<br/>
          <br/>
            <i>Validations</i>:<li>self == oldSelf: spec is immutable</li>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### PlatformAccessApproval.spec
<sup><sup>[↩ Parent](#platformaccessapproval)</sup></sup>



PlatformAccessApprovalSpec defines the desired state of PlatformAccessApproval.

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
        <td><b><a href="#platformaccessapprovalspecsubjectref">subjectRef</a></b></td>
        <td>object</td>
        <td>
          SubjectRef is the reference to the subject being approved.<br/>
          <br/>
            <i>Validations</i>:<li>(has(self.email) && !has(self.userRef)) || (!has(self.email) && has(self.userRef)): Exactly one of email or userRef must be specified</li>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b><a href="#platformaccessapprovalspecapproverref">approverRef</a></b></td>
        <td>object</td>
        <td>
          ApproverRef is the reference to the approver being approved.
If not specified, the approval was made by the system.<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### PlatformAccessApproval.spec.subjectRef
<sup><sup>[↩ Parent](#platformaccessapprovalspec)</sup></sup>



SubjectRef is the reference to the subject being approved.

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
        <td><b>email</b></td>
        <td>string</td>
        <td>
          Email is the email of the user being approved.
Use Email to approve an email address that is not associated with a created user. (e.g. when using PlatformInvitation)
UserRef and Email are mutually exclusive. Exactly one of them must be specified.<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b><a href="#platformaccessapprovalspecsubjectrefuserref">userRef</a></b></td>
        <td>object</td>
        <td>
          UserRef is the reference to the user being approved.
UserRef and Email are mutually exclusive. Exactly one of them must be specified.<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### PlatformAccessApproval.spec.subjectRef.userRef
<sup><sup>[↩ Parent](#platformaccessapprovalspecsubjectref)</sup></sup>



UserRef is the reference to the user being approved.
UserRef and Email are mutually exclusive. Exactly one of them must be specified.

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
          Name is the name of the User being referenced.<br/>
        </td>
        <td>true</td>
      </tr></tbody>
</table>


### PlatformAccessApproval.spec.approverRef
<sup><sup>[↩ Parent](#platformaccessapprovalspec)</sup></sup>



ApproverRef is the reference to the approver being approved.
If not specified, the approval was made by the system.

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
          Name is the name of the User being referenced.<br/>
        </td>
        <td>true</td>
      </tr></tbody>
</table>

## PlatformAccessRejection
<sup><sup>[↩ Parent](#iammiloapiscomv1alpha1 )</sup></sup>






PlatformAccessRejection is the Schema for the platformaccessrejections API.
It represents a formal denial of platform access for a user. Once the rejection is created, a notification can be sent to the user.

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
      <td>iam.miloapis.com/v1alpha1</td>
      <td>true</td>
      </tr>
      <tr>
      <td><b>kind</b></td>
      <td>string</td>
      <td>PlatformAccessRejection</td>
      <td>true</td>
      </tr>
      <tr>
      <td><b><a href="https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.27/#objectmeta-v1-meta">metadata</a></b></td>
      <td>object</td>
      <td>Refer to the Kubernetes API documentation for the fields of the `metadata` field.</td>
      <td>true</td>
      </tr><tr>
        <td><b><a href="#platformaccessrejectionspec">spec</a></b></td>
        <td>object</td>
        <td>
          PlatformAccessRejectionSpec defines the desired state of PlatformAccessRejection.<br/>
          <br/>
            <i>Validations</i>:<li>self == oldSelf: spec is immutable</li>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### PlatformAccessRejection.spec
<sup><sup>[↩ Parent](#platformaccessrejection)</sup></sup>



PlatformAccessRejectionSpec defines the desired state of PlatformAccessRejection.

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
        <td><b>reason</b></td>
        <td>string</td>
        <td>
          Reason is the reason for the rejection.<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b><a href="#platformaccessrejectionspecsubjectref">subjectRef</a></b></td>
        <td>object</td>
        <td>
          UserRef is the reference to the user being rejected.<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b><a href="#platformaccessrejectionspecrejecterref">rejecterRef</a></b></td>
        <td>object</td>
        <td>
          RejecterRef is the reference to the actor who issued the rejection.
If not specified, the rejection was made by the system.<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### PlatformAccessRejection.spec.subjectRef
<sup><sup>[↩ Parent](#platformaccessrejectionspec)</sup></sup>



UserRef is the reference to the user being rejected.

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
          Name is the name of the User being referenced.<br/>
        </td>
        <td>true</td>
      </tr></tbody>
</table>


### PlatformAccessRejection.spec.rejecterRef
<sup><sup>[↩ Parent](#platformaccessrejectionspec)</sup></sup>



RejecterRef is the reference to the actor who issued the rejection.
If not specified, the rejection was made by the system.

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
          Name is the name of the User being referenced.<br/>
        </td>
        <td>true</td>
      </tr></tbody>
</table>

## PlatformInvitation
<sup><sup>[↩ Parent](#iammiloapiscomv1alpha1 )</sup></sup>






PlatformInvitation is the Schema for the platforminvitations API
It represents a platform invitation for a user. Once the platform invitation is created, an email will be sent to the user to invite them to the platform.
The invited user will have access to the platform after they create an account using the asociated email.
It represents a platform invitation for a user.

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
      <td>iam.miloapis.com/v1alpha1</td>
      <td>true</td>
      </tr>
      <tr>
      <td><b>kind</b></td>
      <td>string</td>
      <td>PlatformInvitation</td>
      <td>true</td>
      </tr>
      <tr>
      <td><b><a href="https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.27/#objectmeta-v1-meta">metadata</a></b></td>
      <td>object</td>
      <td>Refer to the Kubernetes API documentation for the fields of the `metadata` field.</td>
      <td>true</td>
      </tr><tr>
        <td><b><a href="#platforminvitationspec">spec</a></b></td>
        <td>object</td>
        <td>
          PlatformInvitationSpec defines the desired state of PlatformInvitation.<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b><a href="#platforminvitationstatus">status</a></b></td>
        <td>object</td>
        <td>
          PlatformInvitationStatus defines the observed state of PlatformInvitation.<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### PlatformInvitation.spec
<sup><sup>[↩ Parent](#platforminvitation)</sup></sup>



PlatformInvitationSpec defines the desired state of PlatformInvitation.

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
        <td><b>email</b></td>
        <td>string</td>
        <td>
          The email of the user being invited.<br/>
          <br/>
            <i>Validations</i>:<li>type(oldSelf) == null_type || self == oldSelf: email type is immutable</li>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>familyName</b></td>
        <td>string</td>
        <td>
          The family name of the user being invited.<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>givenName</b></td>
        <td>string</td>
        <td>
          The given name of the user being invited.<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b><a href="#platforminvitationspecinvitedby">invitedBy</a></b></td>
        <td>object</td>
        <td>
          The user who created the platform invitation. A mutation webhook will default this field to the user who made the request.<br/>
          <br/>
            <i>Validations</i>:<li>type(oldSelf) == null_type || self == oldSelf: invitedBy type is immutable</li>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>scheduleAt</b></td>
        <td>string</td>
        <td>
          The schedule at which the platform invitation will be sent.
It can only be updated before the platform invitation is sent.<br/>
          <br/>
            <i>Format</i>: date-time<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### PlatformInvitation.spec.invitedBy
<sup><sup>[↩ Parent](#platforminvitationspec)</sup></sup>



The user who created the platform invitation. A mutation webhook will default this field to the user who made the request.

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
          Name is the name of the User being referenced.<br/>
        </td>
        <td>true</td>
      </tr></tbody>
</table>


### PlatformInvitation.status
<sup><sup>[↩ Parent](#platforminvitation)</sup></sup>



PlatformInvitationStatus defines the observed state of PlatformInvitation.

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
        <td><b><a href="#platforminvitationstatusconditionsindex">conditions</a></b></td>
        <td>[]object</td>
        <td>
          Conditions provide conditions that represent the current status of the PlatformInvitation.<br/>
          <br/>
            <i>Default</i>: [map[lastTransitionTime:1970-01-01T00:00:00Z message:Platform invitation reconciliation is pending reason:ReconcilePending status:Unknown type:Ready]]<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b><a href="#platforminvitationstatusemail">email</a></b></td>
        <td>object</td>
        <td>
          The email resource that was created for the platform invitation.<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### PlatformInvitation.status.conditions[index]
<sup><sup>[↩ Parent](#platforminvitationstatus)</sup></sup>



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


### PlatformInvitation.status.email
<sup><sup>[↩ Parent](#platforminvitationstatus)</sup></sup>



The email resource that was created for the platform invitation.

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
          The name of the email resource that was created for the platform invitation.<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>namespace</b></td>
        <td>string</td>
        <td>
          The namespace of the email resource that was created for the platform invitation.<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>

## PolicyBinding
<sup><sup>[↩ Parent](#iammiloapiscomv1alpha1 )</sup></sup>






PolicyBinding is the Schema for the policybindings API

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
      <td>iam.miloapis.com/v1alpha1</td>
      <td>true</td>
      </tr>
      <tr>
      <td><b>kind</b></td>
      <td>string</td>
      <td>PolicyBinding</td>
      <td>true</td>
      </tr>
      <tr>
      <td><b><a href="https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.27/#objectmeta-v1-meta">metadata</a></b></td>
      <td>object</td>
      <td>Refer to the Kubernetes API documentation for the fields of the `metadata` field.</td>
      <td>true</td>
      </tr><tr>
        <td><b><a href="#policybindingspec">spec</a></b></td>
        <td>object</td>
        <td>
          PolicyBindingSpec defines the desired state of PolicyBinding<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b><a href="#policybindingstatus">status</a></b></td>
        <td>object</td>
        <td>
          PolicyBindingStatus defines the observed state of PolicyBinding<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### PolicyBinding.spec
<sup><sup>[↩ Parent](#policybinding)</sup></sup>



PolicyBindingSpec defines the desired state of PolicyBinding

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
        <td><b><a href="#policybindingspecresourceselector">resourceSelector</a></b></td>
        <td>object</td>
        <td>
          ResourceSelector defines which resources the subjects in the policy binding
should have the role applied to. Options within this struct are mutually
exclusive.<br/>
          <br/>
            <i>Validations</i>:<li>oldSelf == null || self == oldSelf: ResourceSelector is immutable and cannot be changed after creation</li><li>has(self.resourceRef) != has(self.resourceKind): exactly one of resourceRef or resourceKind must be specified, but not both</li>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b><a href="#policybindingspecroleref">roleRef</a></b></td>
        <td>object</td>
        <td>
          RoleRef is a reference to the Role that is being bound.
This can be a reference to a Role custom resource.<br/>
          <br/>
            <i>Validations</i>:<li>oldSelf == null || self == oldSelf: RoleRef is immutable and cannot be changed after creation</li>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b><a href="#policybindingspecsubjectsindex">subjects</a></b></td>
        <td>[]object</td>
        <td>
          Subjects holds references to the objects the role applies to.<br/>
        </td>
        <td>true</td>
      </tr></tbody>
</table>


### PolicyBinding.spec.resourceSelector
<sup><sup>[↩ Parent](#policybindingspec)</sup></sup>



ResourceSelector defines which resources the subjects in the policy binding
should have the role applied to. Options within this struct are mutually
exclusive.

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
        <td><b><a href="#policybindingspecresourceselectorresourcekind">resourceKind</a></b></td>
        <td>object</td>
        <td>
          ResourceKind specifies that the policy binding should apply to all resources of a specific kind.
Mutually exclusive with resourceRef.<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b><a href="#policybindingspecresourceselectorresourceref">resourceRef</a></b></td>
        <td>object</td>
        <td>
          ResourceRef provides a reference to a specific resource instance.
Mutually exclusive with resourceKind.<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### PolicyBinding.spec.resourceSelector.resourceKind
<sup><sup>[↩ Parent](#policybindingspecresourceselector)</sup></sup>



ResourceKind specifies that the policy binding should apply to all resources of a specific kind.
Mutually exclusive with resourceRef.

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
        <td>string</td>
        <td>
          Kind is the type of resource being referenced.<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>apiGroup</b></td>
        <td>string</td>
        <td>
          APIGroup is the group for the resource type being referenced. If APIGroup
is not specified, the specified Kind must be in the core API group.<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### PolicyBinding.spec.resourceSelector.resourceRef
<sup><sup>[↩ Parent](#policybindingspecresourceselector)</sup></sup>



ResourceRef provides a reference to a specific resource instance.
Mutually exclusive with resourceKind.

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
        <td>string</td>
        <td>
          Kind is the type of resource being referenced.<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>name</b></td>
        <td>string</td>
        <td>
          Name is the name of resource being referenced.<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>uid</b></td>
        <td>string</td>
        <td>
          UID is the unique identifier of the resource being referenced.<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>apiGroup</b></td>
        <td>string</td>
        <td>
          APIGroup is the group for the resource being referenced.
If APIGroup is not specified, the specified Kind must be in the core API group.
For any other third-party types, APIGroup is required.<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>namespace</b></td>
        <td>string</td>
        <td>
          Namespace is the namespace of resource being referenced.
Required for namespace-scoped resources. Omitted for cluster-scoped resources.<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### PolicyBinding.spec.roleRef
<sup><sup>[↩ Parent](#policybindingspec)</sup></sup>



RoleRef is a reference to the Role that is being bound.
This can be a reference to a Role custom resource.

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
      </tr><tr>
        <td><b>namespace</b></td>
        <td>string</td>
        <td>
          Namespace of the referenced Role. If empty, it is assumed to be in the PolicyBinding's namespace.<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### PolicyBinding.spec.subjects[index]
<sup><sup>[↩ Parent](#policybindingspec)</sup></sup>



Subject contains a reference to the object or user identities a role binding applies to.
This can be a User, Group, or MachineAccount.

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
          Kind of object being referenced. Values defined in Kind constants.<br/>
          <br/>
            <i>Enum</i>: User, Group, MachineAccount<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>name</b></td>
        <td>string</td>
        <td>
          Name of the object being referenced. A special group name of
"system:authenticated-users" can be used to refer to all authenticated
users.<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>namespace</b></td>
        <td>string</td>
        <td>
          Namespace of the referenced object.
If not specified for a Group, User or MachineAccount, it is ignored.<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>uid</b></td>
        <td>string</td>
        <td>
          UID of the referenced object. Optional for system groups (groups with names starting with "system:").<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### PolicyBinding.status
<sup><sup>[↩ Parent](#policybinding)</sup></sup>



PolicyBindingStatus defines the observed state of PolicyBinding

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
        <td><b><a href="#policybindingstatusconditionsindex">conditions</a></b></td>
        <td>[]object</td>
        <td>
          Conditions provide conditions that represent the current status of the PolicyBinding.<br/>
          <br/>
            <i>Default</i>: [map[lastTransitionTime:1970-01-01T00:00:00Z message:Waiting for control plane to reconcile reason:Unknown status:Unknown type:Ready]]<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>observedGeneration</b></td>
        <td>integer</td>
        <td>
          ObservedGeneration is the most recent generation observed for this PolicyBinding by the controller.<br/>
          <br/>
            <i>Format</i>: int64<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### PolicyBinding.status.conditions[index]
<sup><sup>[↩ Parent](#policybindingstatus)</sup></sup>



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

## ProtectedResource
<sup><sup>[↩ Parent](#iammiloapiscomv1alpha1 )</sup></sup>






ProtectedResource is the Schema for the protectedresources API

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
      <td>iam.miloapis.com/v1alpha1</td>
      <td>true</td>
      </tr>
      <tr>
      <td><b>kind</b></td>
      <td>string</td>
      <td>ProtectedResource</td>
      <td>true</td>
      </tr>
      <tr>
      <td><b><a href="https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.27/#objectmeta-v1-meta">metadata</a></b></td>
      <td>object</td>
      <td>Refer to the Kubernetes API documentation for the fields of the `metadata` field.</td>
      <td>true</td>
      </tr><tr>
        <td><b><a href="#protectedresourcespec">spec</a></b></td>
        <td>object</td>
        <td>
          ProtectedResourceSpec defines the desired state of ProtectedResource<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b><a href="#protectedresourcestatus">status</a></b></td>
        <td>object</td>
        <td>
          ProtectedResourceStatus defines the observed state of ProtectedResource<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### ProtectedResource.spec
<sup><sup>[↩ Parent](#protectedresource)</sup></sup>



ProtectedResourceSpec defines the desired state of ProtectedResource

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
        <td>string</td>
        <td>
          The kind of the resource.
This will be in the format `Workload`.<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>permissions</b></td>
        <td>[]string</td>
        <td>
          A list of permissions that are associated with the resource.<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>plural</b></td>
        <td>string</td>
        <td>
          The plural form for the resource type, e.g. 'workloads'. Must follow
camelCase format.<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b><a href="#protectedresourcespecserviceref">serviceRef</a></b></td>
        <td>object</td>
        <td>
          ServiceRef references the service definition this protected resource belongs to.<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>singular</b></td>
        <td>string</td>
        <td>
          The singular form for the resource type, e.g. 'workload'. Must follow
camelCase format.<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b><a href="#protectedresourcespecparentresourcesindex">parentResources</a></b></td>
        <td>[]object</td>
        <td>
          A list of resources that are registered with the platform that may be a
parent to the resource. Permissions may be bound to a parent resource so
they can be inherited down the resource hierarchy.<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### ProtectedResource.spec.serviceRef
<sup><sup>[↩ Parent](#protectedresourcespec)</sup></sup>



ServiceRef references the service definition this protected resource belongs to.

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
          Name is the resource name of the service definition.<br/>
        </td>
        <td>true</td>
      </tr></tbody>
</table>


### ProtectedResource.spec.parentResources[index]
<sup><sup>[↩ Parent](#protectedresourcespec)</sup></sup>



ParentResourceRef defines the reference to a parent resource

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
        <td>string</td>
        <td>
          Kind is the type of resource being referenced.<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>apiGroup</b></td>
        <td>string</td>
        <td>
          APIGroup is the group for the resource being referenced.
If APIGroup is not specified, the specified Kind must be in the core API group.
For any other third-party types, APIGroup is required.<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### ProtectedResource.status
<sup><sup>[↩ Parent](#protectedresource)</sup></sup>



ProtectedResourceStatus defines the observed state of ProtectedResource

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
        <td><b><a href="#protectedresourcestatusconditionsindex">conditions</a></b></td>
        <td>[]object</td>
        <td>
          Conditions provide conditions that represent the current status of the ProtectedResource.<br/>
          <br/>
            <i>Default</i>: [map[lastTransitionTime:1970-01-01T00:00:00Z message:Waiting for control plane to reconcile reason:Unknown status:Unknown type:Ready]]<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>observedGeneration</b></td>
        <td>integer</td>
        <td>
          ObservedGeneration is the most recent generation observed for this ProtectedResource. It corresponds to the
ProtectedResource's generation, which is updated on mutation by the API Server.<br/>
          <br/>
            <i>Format</i>: int64<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### ProtectedResource.status.conditions[index]
<sup><sup>[↩ Parent](#protectedresourcestatus)</sup></sup>



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

## Role
<sup><sup>[↩ Parent](#iammiloapiscomv1alpha1 )</sup></sup>






Role is the Schema for the roles API

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
      <td>iam.miloapis.com/v1alpha1</td>
      <td>true</td>
      </tr>
      <tr>
      <td><b>kind</b></td>
      <td>string</td>
      <td>Role</td>
      <td>true</td>
      </tr>
      <tr>
      <td><b><a href="https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.27/#objectmeta-v1-meta">metadata</a></b></td>
      <td>object</td>
      <td>Refer to the Kubernetes API documentation for the fields of the `metadata` field.</td>
      <td>true</td>
      </tr><tr>
        <td><b><a href="#rolespec">spec</a></b></td>
        <td>object</td>
        <td>
          RoleSpec defines the desired state of Role<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b><a href="#rolestatus">status</a></b></td>
        <td>object</td>
        <td>
          RoleStatus defines the observed state of Role<br/>
          <br/>
            <i>Default</i>: map[conditions:[map[lastTransitionTime:1970-01-01T00:00:00Z message:Waiting for control plane to reconcile reason:Unknown status:Unknown type:Ready]]]<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### Role.spec
<sup><sup>[↩ Parent](#role)</sup></sup>



RoleSpec defines the desired state of Role

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
        <td><b>launchStage</b></td>
        <td>string</td>
        <td>
          Defines the launch stage of the IAM Role. Must be one of: Early Access,
Alpha, Beta, Stable, Deprecated.<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>includedPermissions</b></td>
        <td>[]string</td>
        <td>
          The names of the permissions this role grants when bound in an IAM policy.
All permissions must be in the format: `{service}.{resource}.{action}`
(e.g. compute.workloads.create).<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b><a href="#rolespecinheritedrolesindex">inheritedRoles</a></b></td>
        <td>[]object</td>
        <td>
          The list of roles from which this role inherits permissions.
Each entry must be a valid role resource name.<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### Role.spec.inheritedRoles[index]
<sup><sup>[↩ Parent](#rolespec)</sup></sup>



ScopedRoleReference defines a reference to another Role, scoped by namespace.
This is used for purposes like role inheritance where a simple name and namespace
is sufficient to identify the target role.

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
If not specified, it defaults to the namespace of the resource containing this reference.<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### Role.status
<sup><sup>[↩ Parent](#role)</sup></sup>



RoleStatus defines the observed state of Role

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
        <td><b><a href="#rolestatusconditionsindex">conditions</a></b></td>
        <td>[]object</td>
        <td>
          Conditions provide conditions that represent the current status of the Role.<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>effectivePermissions</b></td>
        <td>[]string</td>
        <td>
          EffectivePermissions is the complete flattened list of all permissions
granted by this role, including permissions from inheritedRoles and
directly specified includedPermissions. This is computed by the controller
and provides a single source of truth for all permissions this role grants.<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>observedGeneration</b></td>
        <td>integer</td>
        <td>
          ObservedGeneration is the most recent generation observed by the controller.<br/>
          <br/>
            <i>Format</i>: int64<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>parent</b></td>
        <td>string</td>
        <td>
          The resource name of the parent the role was created under.<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### Role.status.conditions[index]
<sup><sup>[↩ Parent](#rolestatus)</sup></sup>



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

## UserDeactivation
<sup><sup>[↩ Parent](#iammiloapiscomv1alpha1 )</sup></sup>






UserDeactivation is the Schema for the userdeactivations API

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
      <td>iam.miloapis.com/v1alpha1</td>
      <td>true</td>
      </tr>
      <tr>
      <td><b>kind</b></td>
      <td>string</td>
      <td>UserDeactivation</td>
      <td>true</td>
      </tr>
      <tr>
      <td><b><a href="https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.27/#objectmeta-v1-meta">metadata</a></b></td>
      <td>object</td>
      <td>Refer to the Kubernetes API documentation for the fields of the `metadata` field.</td>
      <td>true</td>
      </tr><tr>
        <td><b><a href="#userdeactivationspec">spec</a></b></td>
        <td>object</td>
        <td>
          UserDeactivationSpec defines the desired state of UserDeactivation<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b><a href="#userdeactivationstatus">status</a></b></td>
        <td>object</td>
        <td>
          UserDeactivationStatus defines the observed state of UserDeactivation<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### UserDeactivation.spec
<sup><sup>[↩ Parent](#userdeactivation)</sup></sup>



UserDeactivationSpec defines the desired state of UserDeactivation

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
        <td><b>deactivatedBy</b></td>
        <td>string</td>
        <td>
          DeactivatedBy indicates who initiated the deactivation.<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>reason</b></td>
        <td>string</td>
        <td>
          Reason is the internal reason for deactivation.<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b><a href="#userdeactivationspecuserref">userRef</a></b></td>
        <td>object</td>
        <td>
          UserRef is a reference to the User being deactivated.
User is a cluster-scoped resource.<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>description</b></td>
        <td>string</td>
        <td>
          Description provides detailed internal description for the deactivation.<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### UserDeactivation.spec.userRef
<sup><sup>[↩ Parent](#userdeactivationspec)</sup></sup>



UserRef is a reference to the User being deactivated.
User is a cluster-scoped resource.

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
          Name is the name of the User being referenced.<br/>
        </td>
        <td>true</td>
      </tr></tbody>
</table>


### UserDeactivation.status
<sup><sup>[↩ Parent](#userdeactivation)</sup></sup>



UserDeactivationStatus defines the observed state of UserDeactivation

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
        <td><b><a href="#userdeactivationstatusconditionsindex">conditions</a></b></td>
        <td>[]object</td>
        <td>
          Conditions represent the latest available observations of an object's current state.<br/>
          <br/>
            <i>Default</i>: [map[lastTransitionTime:1970-01-01T00:00:00Z message:Waiting for control plane to reconcile reason:Unknown status:Unknown type:Ready]]<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### UserDeactivation.status.conditions[index]
<sup><sup>[↩ Parent](#userdeactivationstatus)</sup></sup>



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

## UserInvitation
<sup><sup>[↩ Parent](#iammiloapiscomv1alpha1 )</sup></sup>






UserInvitation is the Schema for the userinvitations API

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
      <td>iam.miloapis.com/v1alpha1</td>
      <td>true</td>
      </tr>
      <tr>
      <td><b>kind</b></td>
      <td>string</td>
      <td>UserInvitation</td>
      <td>true</td>
      </tr>
      <tr>
      <td><b><a href="https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.27/#objectmeta-v1-meta">metadata</a></b></td>
      <td>object</td>
      <td>Refer to the Kubernetes API documentation for the fields of the `metadata` field.</td>
      <td>true</td>
      </tr><tr>
        <td><b><a href="#userinvitationspec">spec</a></b></td>
        <td>object</td>
        <td>
          UserInvitationSpec defines the desired state of UserInvitation<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b><a href="#userinvitationstatus">status</a></b></td>
        <td>object</td>
        <td>
          UserInvitationStatus defines the observed state of UserInvitation<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### UserInvitation.spec
<sup><sup>[↩ Parent](#userinvitation)</sup></sup>



UserInvitationSpec defines the desired state of UserInvitation

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
        <td><b>email</b></td>
        <td>string</td>
        <td>
          The email of the user being invited.<br/>
          <br/>
            <i>Validations</i>:<li>type(oldSelf) == null_type || self == oldSelf: email type is immutable</li>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b><a href="#userinvitationspecorganizationref">organizationRef</a></b></td>
        <td>object</td>
        <td>
          OrganizationRef is a reference to the Organization that the user is invoted to.<br/>
          <br/>
            <i>Validations</i>:<li>type(oldSelf) == null_type || self == oldSelf: organizationRef type is immutable</li>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b><a href="#userinvitationspecrolesindex">roles</a></b></td>
        <td>[]object</td>
        <td>
          The roles that will be assigned to the user when they accept the invitation.<br/>
          <br/>
            <i>Validations</i>:<li>type(oldSelf) == null_type || self == oldSelf: roles type is immutable</li>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>state</b></td>
        <td>enum</td>
        <td>
          State is the state of the UserInvitation. In order to accept the invitation, the invited user
must set the state to Accepted.<br/>
          <br/>
            <i>Validations</i>:<li>type(oldSelf) == null_type || oldSelf == 'Pending' || self == oldSelf: state can only transition from Pending to another state and is immutable afterwards</li>
            <i>Enum</i>: Pending, Accepted, Declined<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>expirationDate</b></td>
        <td>string</td>
        <td>
          ExpirationDate is the date and time when the UserInvitation will expire.
If not specified, the UserInvitation will never expire.<br/>
          <br/>
            <i>Validations</i>:<li>type(oldSelf) == null_type || self == oldSelf: expirationDate type is immutable</li>
            <i>Format</i>: date-time<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>familyName</b></td>
        <td>string</td>
        <td>
          The last name of the user being invited.<br/>
          <br/>
            <i>Validations</i>:<li>type(oldSelf) == null_type || self == oldSelf: familyName type is immutable</li>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>givenName</b></td>
        <td>string</td>
        <td>
          The first name of the user being invited.<br/>
          <br/>
            <i>Validations</i>:<li>type(oldSelf) == null_type || self == oldSelf: givenName type is immutable</li>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b><a href="#userinvitationspecinvitedby">invitedBy</a></b></td>
        <td>object</td>
        <td>
          InvitedBy is the user who invited the user. A mutation webhook will default this field to the user who made the request.<br/>
          <br/>
            <i>Validations</i>:<li>type(oldSelf) == null_type || self == oldSelf: invitedBy type is immutable</li>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### UserInvitation.spec.organizationRef
<sup><sup>[↩ Parent](#userinvitationspec)</sup></sup>



OrganizationRef is a reference to the Organization that the user is invoted to.

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


### UserInvitation.spec.roles[index]
<sup><sup>[↩ Parent](#userinvitationspec)</sup></sup>



RoleReference contains information that points to the Role being used

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
      </tr><tr>
        <td><b>namespace</b></td>
        <td>string</td>
        <td>
          Namespace of the referenced Role. If empty, it is assumed to be in the PolicyBinding's namespace.<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### UserInvitation.spec.invitedBy
<sup><sup>[↩ Parent](#userinvitationspec)</sup></sup>



InvitedBy is the user who invited the user. A mutation webhook will default this field to the user who made the request.

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
          Name is the name of the User being referenced.<br/>
        </td>
        <td>true</td>
      </tr></tbody>
</table>


### UserInvitation.status
<sup><sup>[↩ Parent](#userinvitation)</sup></sup>



UserInvitationStatus defines the observed state of UserInvitation

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
        <td><b><a href="#userinvitationstatusconditionsindex">conditions</a></b></td>
        <td>[]object</td>
        <td>
          Conditions provide conditions that represent the current status of the UserInvitation.<br/>
          <br/>
            <i>Default</i>: [map[lastTransitionTime:1970-01-01T00:00:00Z message:Waiting for control plane to reconcile reason:Unknown status:Unknown type:Unknown]]<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b><a href="#userinvitationstatusinviteeuser">inviteeUser</a></b></td>
        <td>object</td>
        <td>
          InviteeUser contains information about the invitee user in the invitation.
This value may be nil if the invitee user has not been created yet.<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b><a href="#userinvitationstatusinviteruser">inviterUser</a></b></td>
        <td>object</td>
        <td>
          InviterUser contains information about the user who invited the user in the invitation.<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b><a href="#userinvitationstatusorganization">organization</a></b></td>
        <td>object</td>
        <td>
          Organization contains information about the organization in the invitation.<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### UserInvitation.status.conditions[index]
<sup><sup>[↩ Parent](#userinvitationstatus)</sup></sup>



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


### UserInvitation.status.inviteeUser
<sup><sup>[↩ Parent](#userinvitationstatus)</sup></sup>



InviteeUser contains information about the invitee user in the invitation.
This value may be nil if the invitee user has not been created yet.

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
          Name is the name of the invitee user in the invitation.
Name is a cluster-scoped resource, so Namespace is not needed.<br/>
        </td>
        <td>true</td>
      </tr></tbody>
</table>


### UserInvitation.status.inviterUser
<sup><sup>[↩ Parent](#userinvitationstatus)</sup></sup>



InviterUser contains information about the user who invited the user in the invitation.

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
          DisplayName is the display name of the user who invited the user in the invitation.<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>emailAddress</b></td>
        <td>string</td>
        <td>
          EmailAddress is the email address of the user who invited the user in the invitation.<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### UserInvitation.status.organization
<sup><sup>[↩ Parent](#userinvitationstatus)</sup></sup>



Organization contains information about the organization in the invitation.

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
          DisplayName is the display name of the organization in the invitation.<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>

## UserPreference
<sup><sup>[↩ Parent](#iammiloapiscomv1alpha1 )</sup></sup>






UserPreference is the Schema for the userpreferences API

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
      <td>iam.miloapis.com/v1alpha1</td>
      <td>true</td>
      </tr>
      <tr>
      <td><b>kind</b></td>
      <td>string</td>
      <td>UserPreference</td>
      <td>true</td>
      </tr>
      <tr>
      <td><b><a href="https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.27/#objectmeta-v1-meta">metadata</a></b></td>
      <td>object</td>
      <td>Refer to the Kubernetes API documentation for the fields of the `metadata` field.</td>
      <td>true</td>
      </tr><tr>
        <td><b><a href="#userpreferencespec">spec</a></b></td>
        <td>object</td>
        <td>
          UserPreferenceSpec defines the desired state of UserPreference<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b><a href="#userpreferencestatus">status</a></b></td>
        <td>object</td>
        <td>
          UserPreferenceStatus defines the observed state of UserPreference<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### UserPreference.spec
<sup><sup>[↩ Parent](#userpreference)</sup></sup>



UserPreferenceSpec defines the desired state of UserPreference

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
        <td><b><a href="#userpreferencespecuserref">userRef</a></b></td>
        <td>object</td>
        <td>
          Reference to the user these preferences belong to.<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>theme</b></td>
        <td>enum</td>
        <td>
          The user's theme preference.<br/>
          <br/>
            <i>Enum</i>: light, dark, system<br/>
            <i>Default</i>: system<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### UserPreference.spec.userRef
<sup><sup>[↩ Parent](#userpreferencespec)</sup></sup>



Reference to the user these preferences belong to.

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
          Name is the name of the User being referenced.<br/>
        </td>
        <td>true</td>
      </tr></tbody>
</table>


### UserPreference.status
<sup><sup>[↩ Parent](#userpreference)</sup></sup>



UserPreferenceStatus defines the observed state of UserPreference

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
        <td><b><a href="#userpreferencestatusconditionsindex">conditions</a></b></td>
        <td>[]object</td>
        <td>
          Conditions provide conditions that represent the current status of the UserPreference.<br/>
          <br/>
            <i>Default</i>: [map[lastTransitionTime:1970-01-01T00:00:00Z message:Waiting for control plane to reconcile reason:Unknown status:Unknown type:Ready]]<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### UserPreference.status.conditions[index]
<sup><sup>[↩ Parent](#userpreferencestatus)</sup></sup>



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

## User
<sup><sup>[↩ Parent](#iammiloapiscomv1alpha1 )</sup></sup>






User is the Schema for the users API

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
      <td>iam.miloapis.com/v1alpha1</td>
      <td>true</td>
      </tr>
      <tr>
      <td><b>kind</b></td>
      <td>string</td>
      <td>User</td>
      <td>true</td>
      </tr>
      <tr>
      <td><b><a href="https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.27/#objectmeta-v1-meta">metadata</a></b></td>
      <td>object</td>
      <td>Refer to the Kubernetes API documentation for the fields of the `metadata` field.</td>
      <td>true</td>
      </tr><tr>
        <td><b><a href="#userspec">spec</a></b></td>
        <td>object</td>
        <td>
          UserSpec defines the desired state of User<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b><a href="#userstatus">status</a></b></td>
        <td>object</td>
        <td>
          UserStatus defines the observed state of User<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### User.spec
<sup><sup>[↩ Parent](#user)</sup></sup>



UserSpec defines the desired state of User

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
        <td><b>email</b></td>
        <td>string</td>
        <td>
          The email of the user.<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>familyName</b></td>
        <td>string</td>
        <td>
          The last name of the user.<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>givenName</b></td>
        <td>string</td>
        <td>
          The first name of the user.<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### User.status
<sup><sup>[↩ Parent](#user)</sup></sup>



UserStatus defines the observed state of User

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
          AvatarURL points to the avatar image associated with the user. This value is
populated by the auth provider or any service that provides a user avatar URL.<br/>
          <br/>
            <i>Format</i>: uri<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b><a href="#userstatusconditionsindex">conditions</a></b></td>
        <td>[]object</td>
        <td>
          Conditions provide conditions that represent the current status of the User.<br/>
          <br/>
            <i>Default</i>: [map[lastTransitionTime:1970-01-01T00:00:00Z message:Waiting for control plane to reconcile reason:Unknown status:Unknown type:Ready]]<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>lastLoginProvider</b></td>
        <td>string</td>
        <td>
          LastLoginProvider records the identity provider that was most recently used by the
user to log in (e.g., "github" or "google"). This field is set by the auth provider
based on authentication events.<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>registrationApproval</b></td>
        <td>enum</td>
        <td>
          RegistrationApproval represents the administrator’s decision on the user’s registration request.
States:
  - Pending:  The user is awaiting review by an administrator.
  - Approved: The user registration has been approved.
  - Rejected: The user registration has been rejected.
The User resource is always created regardless of this value, but the
ability for the person to sign into the platform and access resources is
governed by this status: only *Approved* users are granted access, while
*Pending* and *Rejected* users are prevented for interacting with resources.<br/>
          <br/>
            <i>Enum</i>: Pending, Approved, Rejected<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>state</b></td>
        <td>enum</td>
        <td>
          State represents the current activation state of the user account from the
auth provider. This field is managed exclusively by the UserDeactivation CRD
and cannot be changed directly by the user. When a UserDeactivation resource
is created for the user, the user is deactivated in the auth provider; when
the UserDeactivation is deleted, the user is reactivated.
States:
  - Active: The user can be used to authenticate.
  - Inactive: The user is prohibited to be used to authenticate, and revokes all existing sessions.<br/>
          <br/>
            <i>Enum</i>: Active, Inactive<br/>
            <i>Default</i>: Active<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### User.status.conditions[index]
<sup><sup>[↩ Parent](#userstatus)</sup></sup>



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

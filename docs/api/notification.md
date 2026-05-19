# API Reference

Packages:

- [notification.miloapis.com/v1alpha1](#notificationmiloapiscomv1alpha1)

# notification.miloapis.com/v1alpha1

Resource Types:

- [ContactGroupEnrollmentPolicy](#contactgroupenrollmentpolicy)

- [ContactGroupMembershipRemoval](#contactgroupmembershipremoval)

- [ContactGroupMembership](#contactgroupmembership)

- [ContactGroup](#contactgroup)

- [Contact](#contact)

- [EmailBroadcast](#emailbroadcast)

- [Email](#email)

- [EmailTemplate](#emailtemplate)

- [Note](#note)




## ContactGroupEnrollmentPolicy
<sup><sup>[↩ Parent](#notificationmiloapiscomv1alpha1 )</sup></sup>






ContactGroupEnrollmentPolicy defines which ContactGroup a new Contact is automatically
enrolled in when a trigger condition is met.

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
      <td>notification.miloapis.com/v1alpha1</td>
      <td>true</td>
      </tr>
      <tr>
      <td><b>kind</b></td>
      <td>string</td>
      <td>ContactGroupEnrollmentPolicy</td>
      <td>true</td>
      </tr>
      <tr>
      <td><b><a href="https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.27/#objectmeta-v1-meta">metadata</a></b></td>
      <td>object</td>
      <td>Refer to the Kubernetes API documentation for the fields of the `metadata` field.</td>
      <td>true</td>
      </tr><tr>
        <td><b><a href="#contactgroupenrollmentpolicyspec">spec</a></b></td>
        <td>object</td>
        <td>
          ContactGroupEnrollmentPolicySpec defines the desired enrollment behavior.<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### ContactGroupEnrollmentPolicy.spec
<sup><sup>[↩ Parent](#contactgroupenrollmentpolicy)</sup></sup>



ContactGroupEnrollmentPolicySpec defines the desired enrollment behavior.

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
        <td><b><a href="#contactgroupenrollmentpolicyspeccontactgroupref">contactGroupRef</a></b></td>
        <td>object</td>
        <td>
          ContactGroupRef references the ContactGroup that matching Contacts are enrolled in.<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b><a href="#contactgroupenrollmentpolicyspectrigger">trigger</a></b></td>
        <td>object</td>
        <td>
          Trigger defines when enrollment happens.<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b><a href="#contactgroupenrollmentpolicyspeccontactselector">contactSelector</a></b></td>
        <td>object</td>
        <td>
          ContactSelector filters which Contacts this policy applies to.
If omitted, the policy applies to all Contacts.<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### ContactGroupEnrollmentPolicy.spec.contactGroupRef
<sup><sup>[↩ Parent](#contactgroupenrollmentpolicyspec)</sup></sup>



ContactGroupRef references the ContactGroup that matching Contacts are enrolled in.

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
          Name is the name of the ContactGroup.<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>namespace</b></td>
        <td>string</td>
        <td>
          Namespace is the namespace of the ContactGroup.<br/>
        </td>
        <td>true</td>
      </tr></tbody>
</table>


### ContactGroupEnrollmentPolicy.spec.trigger
<sup><sup>[↩ Parent](#contactgroupenrollmentpolicyspec)</sup></sup>



Trigger defines when enrollment happens.

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
          Type is the event that triggers enrollment.
ContactCreated fires when a new Contact resource is created.<br/>
          <br/>
            <i>Enum</i>: ContactCreated<br/>
        </td>
        <td>true</td>
      </tr></tbody>
</table>


### ContactGroupEnrollmentPolicy.spec.contactSelector
<sup><sup>[↩ Parent](#contactgroupenrollmentpolicyspec)</sup></sup>



ContactSelector filters which Contacts this policy applies to.
If omitted, the policy applies to all Contacts.

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
        <td><b>subjectKind</b></td>
        <td>enum</td>
        <td>
          SubjectKind restricts enrollment to Contacts whose SubjectRef.Kind matches this value.<br/>
          <br/>
            <i>Enum</i>: User<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>

## ContactGroupMembershipRemoval
<sup><sup>[↩ Parent](#notificationmiloapiscomv1alpha1 )</sup></sup>






ContactGroupMembershipRemoval is the Schema for the contactgroupmembershipremovals API.
It represents a removal of a Contact from a ContactGroup, it also prevents the Contact from being added to the ContactGroup.

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
      <td>notification.miloapis.com/v1alpha1</td>
      <td>true</td>
      </tr>
      <tr>
      <td><b>kind</b></td>
      <td>string</td>
      <td>ContactGroupMembershipRemoval</td>
      <td>true</td>
      </tr>
      <tr>
      <td><b><a href="https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.27/#objectmeta-v1-meta">metadata</a></b></td>
      <td>object</td>
      <td>Refer to the Kubernetes API documentation for the fields of the `metadata` field.</td>
      <td>true</td>
      </tr><tr>
        <td><b><a href="#contactgroupmembershipremovalspec">spec</a></b></td>
        <td>object</td>
        <td>
          <br/>
          <br/>
            <i>Validations</i>:<li>self == oldSelf: spec is immutable</li>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b><a href="#contactgroupmembershipremovalstatus">status</a></b></td>
        <td>object</td>
        <td>
          <br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### ContactGroupMembershipRemoval.spec
<sup><sup>[↩ Parent](#contactgroupmembershipremoval)</sup></sup>





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
        <td><b><a href="#contactgroupmembershipremovalspeccontactgroupref">contactGroupRef</a></b></td>
        <td>object</td>
        <td>
          ContactGroupRef is a reference to the ContactGroup that the Contact does not want to be a member of.<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b><a href="#contactgroupmembershipremovalspeccontactref">contactRef</a></b></td>
        <td>object</td>
        <td>
          ContactRef is a reference to the Contact that prevents the Contact from being part of the ContactGroup.<br/>
        </td>
        <td>true</td>
      </tr></tbody>
</table>


### ContactGroupMembershipRemoval.spec.contactGroupRef
<sup><sup>[↩ Parent](#contactgroupmembershipremovalspec)</sup></sup>



ContactGroupRef is a reference to the ContactGroup that the Contact does not want to be a member of.

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
          Name is the name of the ContactGroup being referenced.<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>namespace</b></td>
        <td>string</td>
        <td>
          Namespace is the namespace of the ContactGroup being referenced.<br/>
        </td>
        <td>true</td>
      </tr></tbody>
</table>


### ContactGroupMembershipRemoval.spec.contactRef
<sup><sup>[↩ Parent](#contactgroupmembershipremovalspec)</sup></sup>



ContactRef is a reference to the Contact that prevents the Contact from being part of the ContactGroup.

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
          Name is the name of the Contact being referenced.<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>namespace</b></td>
        <td>string</td>
        <td>
          Namespace is the namespace of the Contact being referenced.<br/>
        </td>
        <td>true</td>
      </tr></tbody>
</table>


### ContactGroupMembershipRemoval.status
<sup><sup>[↩ Parent](#contactgroupmembershipremoval)</sup></sup>





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
        <td><b><a href="#contactgroupmembershipremovalstatusconditionsindex">conditions</a></b></td>
        <td>[]object</td>
        <td>
          Conditions represent the latest available observations of an object's current state.
Standard condition is "Ready" which tracks contact group membership removal creation status.<br/>
          <br/>
            <i>Default</i>: [map[lastTransitionTime:1970-01-01T00:00:00Z message:Waiting for contact group membership removal to be created reason:CreatePending status:Unknown type:Ready]]<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>username</b></td>
        <td>string</td>
        <td>
          Username is the username of the user that owns the ContactGroupMembershipRemoval.
This is populated by the controller based on the referenced Contact's subject.<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### ContactGroupMembershipRemoval.status.conditions[index]
<sup><sup>[↩ Parent](#contactgroupmembershipremovalstatus)</sup></sup>



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

## ContactGroupMembership
<sup><sup>[↩ Parent](#notificationmiloapiscomv1alpha1 )</sup></sup>






ContactGroupMembership is the Schema for the contactgroupmemberships API.
It represents a membership of a Contact in a ContactGroup.

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
      <td>notification.miloapis.com/v1alpha1</td>
      <td>true</td>
      </tr>
      <tr>
      <td><b>kind</b></td>
      <td>string</td>
      <td>ContactGroupMembership</td>
      <td>true</td>
      </tr>
      <tr>
      <td><b><a href="https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.27/#objectmeta-v1-meta">metadata</a></b></td>
      <td>object</td>
      <td>Refer to the Kubernetes API documentation for the fields of the `metadata` field.</td>
      <td>true</td>
      </tr><tr>
        <td><b><a href="#contactgroupmembershipspec">spec</a></b></td>
        <td>object</td>
        <td>
          ContactGroupMembershipSpec defines the desired state of ContactGroupMembership.<br/>
          <br/>
            <i>Validations</i>:<li>self == oldSelf: spec is immutable</li>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b><a href="#contactgroupmembershipstatus">status</a></b></td>
        <td>object</td>
        <td>
          <br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### ContactGroupMembership.spec
<sup><sup>[↩ Parent](#contactgroupmembership)</sup></sup>



ContactGroupMembershipSpec defines the desired state of ContactGroupMembership.

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
        <td><b><a href="#contactgroupmembershipspeccontactgroupref">contactGroupRef</a></b></td>
        <td>object</td>
        <td>
          ContactGroupRef is a reference to the ContactGroup that the Contact is a member of.<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b><a href="#contactgroupmembershipspeccontactref">contactRef</a></b></td>
        <td>object</td>
        <td>
          ContactRef is a reference to the Contact that is a member of the ContactGroup.<br/>
        </td>
        <td>true</td>
      </tr></tbody>
</table>


### ContactGroupMembership.spec.contactGroupRef
<sup><sup>[↩ Parent](#contactgroupmembershipspec)</sup></sup>



ContactGroupRef is a reference to the ContactGroup that the Contact is a member of.

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
          Name is the name of the ContactGroup being referenced.<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>namespace</b></td>
        <td>string</td>
        <td>
          Namespace is the namespace of the ContactGroup being referenced.<br/>
        </td>
        <td>true</td>
      </tr></tbody>
</table>


### ContactGroupMembership.spec.contactRef
<sup><sup>[↩ Parent](#contactgroupmembershipspec)</sup></sup>



ContactRef is a reference to the Contact that is a member of the ContactGroup.

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
          Name is the name of the Contact being referenced.<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>namespace</b></td>
        <td>string</td>
        <td>
          Namespace is the namespace of the Contact being referenced.<br/>
        </td>
        <td>true</td>
      </tr></tbody>
</table>


### ContactGroupMembership.status
<sup><sup>[↩ Parent](#contactgroupmembership)</sup></sup>





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
        <td><b><a href="#contactgroupmembershipstatusconditionsindex">conditions</a></b></td>
        <td>[]object</td>
        <td>
          Conditions represent the latest available observations of an object's current state.
Standard condition is "Ready" which tracks contact group membership creation status and sync to the contact group membership provider.<br/>
          <br/>
            <i>Default</i>: [map[lastTransitionTime:1970-01-01T00:00:00Z message:Waiting for contact group membership to be created reason:CreatePending status:Unknown type:Ready]]<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>providerID</b></td>
        <td>string</td>
        <td>
          ProviderID is the identifier returned by the underlying contact provider
(e.g. Resend) when the membership is created in the associated audience. It is usually
used to track the contact-group membership creation status (e.g. provider webhooks).
Deprecated: Use Providers instead.<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b><a href="#contactgroupmembershipstatusprovidersindex">providers</a></b></td>
        <td>[]object</td>
        <td>
          Providers contains the per-provider status for this contact group membership.
This enables tracking multiple provider backends simultaneously.<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>username</b></td>
        <td>string</td>
        <td>
          Username is the username of the user that owns the ContactGroupMembership.
This is populated by the controller based on the referenced Contact's subject.<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### ContactGroupMembership.status.conditions[index]
<sup><sup>[↩ Parent](#contactgroupmembershipstatus)</sup></sup>



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


### ContactGroupMembership.status.providers[index]
<sup><sup>[↩ Parent](#contactgroupmembershipstatus)</sup></sup>



ContactProviderStatus represents status information for a single contact provider.
It allows tracking the provider name and the provider-specific identifier.

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
        <td><b>id</b></td>
        <td>string</td>
        <td>
          ID is the identifier returned by the specific contact provider for this contact.<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>name</b></td>
        <td>enum</td>
        <td>
          Name is the provider handling this contact.
Allowed values are Resend and Loops.<br/>
          <br/>
            <i>Enum</i>: Resend, Loops<br/>
        </td>
        <td>true</td>
      </tr></tbody>
</table>

## ContactGroup
<sup><sup>[↩ Parent](#notificationmiloapiscomv1alpha1 )</sup></sup>






ContactGroup is the Schema for the contactgroups API.
It represents a logical grouping of Contacts.

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
      <td>notification.miloapis.com/v1alpha1</td>
      <td>true</td>
      </tr>
      <tr>
      <td><b>kind</b></td>
      <td>string</td>
      <td>ContactGroup</td>
      <td>true</td>
      </tr>
      <tr>
      <td><b><a href="https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.27/#objectmeta-v1-meta">metadata</a></b></td>
      <td>object</td>
      <td>Refer to the Kubernetes API documentation for the fields of the `metadata` field.</td>
      <td>true</td>
      </tr><tr>
        <td><b><a href="#contactgroupspec">spec</a></b></td>
        <td>object</td>
        <td>
          ContactGroupSpec defines the desired state of ContactGroup.<br/>
          <br/>
            <i>Validations</i>:<li>!has(oldSelf.providers) || (has(self.providers) && oldSelf.providers.all(o, self.providers.exists(n, n.name == o.name)) && !self.providers.exists(n, oldSelf.providers.exists(o, o.name == n.name && o.id != n.id))): providers can only be added, not removed, and their IDs are immutable. In order to update or remove, delete the ContactGroup and create a new one.</li>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b><a href="#contactgroupstatus">status</a></b></td>
        <td>object</td>
        <td>
          <br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### ContactGroup.spec
<sup><sup>[↩ Parent](#contactgroup)</sup></sup>



ContactGroupSpec defines the desired state of ContactGroup.

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
          DisplayName is the display name of the contact group.<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>visibility</b></td>
        <td>enum</td>
        <td>
          Visibility determines whether members are allowed opt-in or opt-out of the contactgroup.
  • "public"  – members may leave via ContactGroupMembershipRemoval.
  • "private" – membership is enforced; opt-out requests are rejected.<br/>
          <br/>
            <i>Validations</i>:<li>type(oldSelf) == null_type || self == oldSelf: visibility type is immutable</li>
            <i>Enum</i>: public, private<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>description</b></td>
        <td>string</td>
        <td>
          Description is the description of the contact group.
Email providers (e.g. Loops) also have a description field for the contact group.
This value should be the same, as the provider will use it for showing the description on the opt-in/opt-out page
generated by themselves. Note that synchronization of this field is not supported.<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b><a href="#contactgroupspecprovidersindex">providers</a></b></td>
        <td>[]object</td>
        <td>
          Providers defines the providers this group should be synced to.<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### ContactGroup.spec.providers[index]
<sup><sup>[↩ Parent](#contactgroupspec)</sup></sup>



ContactGroupProviderSpec defines the desired state of a contact group in a specific provider.

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
        <td><b>id</b></td>
        <td>string</td>
        <td>
          ID is the identifier of the contact group in the external provider.
This field is used when a provider does not expose an API for creating mailing lists,
requiring an existing ContactList ID to be provided for synchronization purposes (e.g. Loops).
If not provided, a new group will be created if supported by the provider.<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>name</b></td>
        <td>enum</td>
        <td>
          Name is the provider handling this contact group.
Allowed values is Loops.<br/>
          <br/>
            <i>Enum</i>: Loops<br/>
        </td>
        <td>true</td>
      </tr></tbody>
</table>


### ContactGroup.status
<sup><sup>[↩ Parent](#contactgroup)</sup></sup>





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
        <td><b><a href="#contactgroupstatusconditionsindex">conditions</a></b></td>
        <td>[]object</td>
        <td>
          Conditions represent the latest available observations of an object's current state.
Standard condition is "Ready" which tracks contact group creation status and sync to the contact group provider.<br/>
          <br/>
            <i>Default</i>: [map[lastTransitionTime:1970-01-01T00:00:00Z message:Waiting for contact group to be created reason:CreatePending status:Unknown type:Ready]]<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>providerID</b></td>
        <td>string</td>
        <td>
          ProviderID is the identifier returned by the underlying contact groupprovider
(e.g. Resend) when the contact groupis created. It is usually
used to track the contact creation status (e.g. provider webhooks).
Deprecated: Use Providers instead.<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b><a href="#contactgroupstatusprovidersindex">providers</a></b></td>
        <td>[]object</td>
        <td>
          Providers contains the per-provider status for this contact group.
This enables tracking multiple provider backends simultaneously.<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### ContactGroup.status.conditions[index]
<sup><sup>[↩ Parent](#contactgroupstatus)</sup></sup>



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


### ContactGroup.status.providers[index]
<sup><sup>[↩ Parent](#contactgroupstatus)</sup></sup>



ContactProviderStatus represents status information for a single contact provider.
It allows tracking the provider name and the provider-specific identifier.

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
        <td><b>id</b></td>
        <td>string</td>
        <td>
          ID is the identifier returned by the specific contact provider for this contact.<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>name</b></td>
        <td>enum</td>
        <td>
          Name is the provider handling this contact.
Allowed values are Resend and Loops.<br/>
          <br/>
            <i>Enum</i>: Resend, Loops<br/>
        </td>
        <td>true</td>
      </tr></tbody>
</table>

## Contact
<sup><sup>[↩ Parent](#notificationmiloapiscomv1alpha1 )</sup></sup>






Contact is the Schema for the contacts API.
It represents a contact for a user.

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
      <td>notification.miloapis.com/v1alpha1</td>
      <td>true</td>
      </tr>
      <tr>
      <td><b>kind</b></td>
      <td>string</td>
      <td>Contact</td>
      <td>true</td>
      </tr>
      <tr>
      <td><b><a href="https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.27/#objectmeta-v1-meta">metadata</a></b></td>
      <td>object</td>
      <td>Refer to the Kubernetes API documentation for the fields of the `metadata` field.</td>
      <td>true</td>
      </tr><tr>
        <td><b><a href="#contactspec">spec</a></b></td>
        <td>object</td>
        <td>
          ContactSpec defines the desired state of Contact.<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b><a href="#contactstatus">status</a></b></td>
        <td>object</td>
        <td>
          <br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### Contact.spec
<sup><sup>[↩ Parent](#contact)</sup></sup>



ContactSpec defines the desired state of Contact.

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
          <br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>familyName</b></td>
        <td>string</td>
        <td>
          <br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>givenName</b></td>
        <td>string</td>
        <td>
          <br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b><a href="#contactspecsubject">subject</a></b></td>
        <td>object</td>
        <td>
          Subject is a reference to the subject of the contact.<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### Contact.spec.subject
<sup><sup>[↩ Parent](#contactspec)</sup></sup>



Subject is a reference to the subject of the contact.

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
        <td><b>apiGroup</b></td>
        <td>enum</td>
        <td>
          APIGroup is the group for the resource being referenced.<br/>
          <br/>
            <i>Enum</i>: iam.miloapis.com<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>kind</b></td>
        <td>enum</td>
        <td>
          Kind is the type of resource being referenced.<br/>
          <br/>
            <i>Enum</i>: User<br/>
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
        <td><b>namespace</b></td>
        <td>string</td>
        <td>
          Namespace is the namespace of resource being referenced.
Required for namespace-scoped resources. Omitted for cluster-scoped resources.<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### Contact.status
<sup><sup>[↩ Parent](#contact)</sup></sup>





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
        <td><b><a href="#contactstatusconditionsindex">conditions</a></b></td>
        <td>[]object</td>
        <td>
          Conditions represent the latest available observations of an object's current state.
Standard condition is "Ready" which tracks contact creation status and sync to the contact provider.<br/>
          <br/>
            <i>Default</i>: [map[lastTransitionTime:1970-01-01T00:00:00Z message:Waiting for contact to be created reason:CreatePending status:Unknown type:Ready]]<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>providerID</b></td>
        <td>string</td>
        <td>
          ProviderID is the identifier returned by the underlying contact provider
(e.g. Resend) when the contact is created. It is usually
used to track the contact creation status (e.g. provider webhooks).
Deprecated: Use Providers instead.<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b><a href="#contactstatusprovidersindex">providers</a></b></td>
        <td>[]object</td>
        <td>
          Providers contains the per-provider status for this contact.
This enables tracking multiple provider backends simultaneously.<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### Contact.status.conditions[index]
<sup><sup>[↩ Parent](#contactstatus)</sup></sup>



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


### Contact.status.providers[index]
<sup><sup>[↩ Parent](#contactstatus)</sup></sup>



ContactProviderStatus represents status information for a single contact provider.
It allows tracking the provider name and the provider-specific identifier.

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
        <td><b>id</b></td>
        <td>string</td>
        <td>
          ID is the identifier returned by the specific contact provider for this contact.<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>name</b></td>
        <td>enum</td>
        <td>
          Name is the provider handling this contact.
Allowed values are Resend and Loops.<br/>
          <br/>
            <i>Enum</i>: Resend, Loops<br/>
        </td>
        <td>true</td>
      </tr></tbody>
</table>

## EmailBroadcast
<sup><sup>[↩ Parent](#notificationmiloapiscomv1alpha1 )</sup></sup>






EmailBroadcast is the Schema for the emailbroadcasts API.
It represents a broadcast of an email to a set of contacts (ContactGroup).
If the broadcast needs to be updated, delete and recreate the resource.

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
      <td>notification.miloapis.com/v1alpha1</td>
      <td>true</td>
      </tr>
      <tr>
      <td><b>kind</b></td>
      <td>string</td>
      <td>EmailBroadcast</td>
      <td>true</td>
      </tr>
      <tr>
      <td><b><a href="https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.27/#objectmeta-v1-meta">metadata</a></b></td>
      <td>object</td>
      <td>Refer to the Kubernetes API documentation for the fields of the `metadata` field.</td>
      <td>true</td>
      </tr><tr>
        <td><b><a href="#emailbroadcastspec">spec</a></b></td>
        <td>object</td>
        <td>
          EmailBroadcastSpec defines the desired state of EmailBroadcast.<br/>
          <br/>
            <i>Validations</i>:<li>self == oldSelf: spec is immutable</li>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b><a href="#emailbroadcaststatus">status</a></b></td>
        <td>object</td>
        <td>
          <br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### EmailBroadcast.spec
<sup><sup>[↩ Parent](#emailbroadcast)</sup></sup>



EmailBroadcastSpec defines the desired state of EmailBroadcast.

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
        <td><b><a href="#emailbroadcastspeccontactgroupref">contactGroupRef</a></b></td>
        <td>object</td>
        <td>
          ContactGroupRef is a reference to the ContactGroup that the email broadcast is for.<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b><a href="#emailbroadcastspectemplateref">templateRef</a></b></td>
        <td>object</td>
        <td>
          TemplateRef references the EmailTemplate to render the broadcast message.
When using the Resend provider you can include the following placeholders
in HTMLBody or TextBody; they will be substituted by the provider at send time:
  {{{FIRST_NAME}}} {{{LAST_NAME}}} {{{EMAIL}}}<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>displayName</b></td>
        <td>string</td>
        <td>
          DisplayName is the display name of the email broadcast.<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>scheduledAt</b></td>
        <td>string</td>
        <td>
          ScheduledAt optionally specifies the time at which the broadcast should be executed.
If omitted, the message is sent as soon as the controller reconciles the resource.
Example: "2024-08-05T11:52:01.858Z"<br/>
          <br/>
            <i>Format</i>: date-time<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### EmailBroadcast.spec.contactGroupRef
<sup><sup>[↩ Parent](#emailbroadcastspec)</sup></sup>



ContactGroupRef is a reference to the ContactGroup that the email broadcast is for.

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
          Name is the name of the ContactGroup being referenced.<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>namespace</b></td>
        <td>string</td>
        <td>
          Namespace is the namespace of the ContactGroup being referenced.<br/>
        </td>
        <td>true</td>
      </tr></tbody>
</table>


### EmailBroadcast.spec.templateRef
<sup><sup>[↩ Parent](#emailbroadcastspec)</sup></sup>



TemplateRef references the EmailTemplate to render the broadcast message.
When using the Resend provider you can include the following placeholders
in HTMLBody or TextBody; they will be substituted by the provider at send time:
  {{{FIRST_NAME}}} {{{LAST_NAME}}} {{{EMAIL}}}

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
          Name is the name of the EmailTemplate being referenced.<br/>
        </td>
        <td>true</td>
      </tr></tbody>
</table>


### EmailBroadcast.status
<sup><sup>[↩ Parent](#emailbroadcast)</sup></sup>





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
        <td><b><a href="#emailbroadcaststatusconditionsindex">conditions</a></b></td>
        <td>[]object</td>
        <td>
          Conditions represent the latest available observations of an object's current state.
Standard condition is "Ready" which tracks email broadcast status and sync to the email broadcast provider.<br/>
          <br/>
            <i>Default</i>: [map[lastTransitionTime:1970-01-01T00:00:00Z message:Waiting for email broadcast to be created reason:CreatePending status:Unknown type:Ready]]<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>providerID</b></td>
        <td>string</td>
        <td>
          ProviderID is the identifier returned by the underlying email broadcast provider
(e.g. Resend) when the email broadcast is created. It is usually
used to track the email broadcast creation status (e.g. provider webhooks).<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### EmailBroadcast.status.conditions[index]
<sup><sup>[↩ Parent](#emailbroadcaststatus)</sup></sup>



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

## Email
<sup><sup>[↩ Parent](#notificationmiloapiscomv1alpha1 )</sup></sup>






Email is the Schema for the emails API.
It represents a concrete e-mail that should be sent to the referenced users.
For idempotency purposes, controllers can use metadata.uid as a unique identifier
to prevent duplicate email delivery, since it's guaranteed to be unique per resource instance.

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
      <td>notification.miloapis.com/v1alpha1</td>
      <td>true</td>
      </tr>
      <tr>
      <td><b>kind</b></td>
      <td>string</td>
      <td>Email</td>
      <td>true</td>
      </tr>
      <tr>
      <td><b><a href="https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.27/#objectmeta-v1-meta">metadata</a></b></td>
      <td>object</td>
      <td>Refer to the Kubernetes API documentation for the fields of the `metadata` field.</td>
      <td>true</td>
      </tr><tr>
        <td><b><a href="#emailspec">spec</a></b></td>
        <td>object</td>
        <td>
          EmailSpec defines the desired state of Email.
It references a template, recipients, and any variables required to render the final message.<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b><a href="#emailstatus">status</a></b></td>
        <td>object</td>
        <td>
          EmailStatus captures the observed state of an Email.
Uses standard Kubernetes conditions to track both processing and delivery state.<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### Email.spec
<sup><sup>[↩ Parent](#email)</sup></sup>



EmailSpec defines the desired state of Email.
It references a template, recipients, and any variables required to render the final message.

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
        <td><b><a href="#emailspecrecipient">recipient</a></b></td>
        <td>object</td>
        <td>
          Recipient contain the recipient of the email.<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b><a href="#emailspectemplateref">templateRef</a></b></td>
        <td>object</td>
        <td>
          TemplateRef references the EmailTemplate that should be rendered.<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>bcc</b></td>
        <td>[]string</td>
        <td>
          BCC contains e-mail addresses that will receive a blind-carbon copy of the message.
Maximum 10 addresses.<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>cc</b></td>
        <td>[]string</td>
        <td>
          CC contains additional e-mail addresses that will receive a carbon copy of the message.
Maximum 10 addresses.<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>priority</b></td>
        <td>enum</td>
        <td>
          Priority influences the order in which pending e-mails are processed.<br/>
          <br/>
            <i>Enum</i>: low, normal, high<br/>
            <i>Default</i>: normal<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b><a href="#emailspecvariablesindex">variables</a></b></td>
        <td>[]object</td>
        <td>
          Variables supplies the values that will be substituted in the template.<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### Email.spec.recipient
<sup><sup>[↩ Parent](#emailspec)</sup></sup>



Recipient contain the recipient of the email.

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
        <td><b>emailAddress</b></td>
        <td>string</td>
        <td>
          EmailAddress allows specifying a literal e-mail address for the recipient instead of referencing a User resource.
It is mutually exclusive with UserRef: exactly one of them must be specified.<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b><a href="#emailspecrecipientuserref">userRef</a></b></td>
        <td>object</td>
        <td>
          UserRef references the User resource that will receive the message.
It is mutually exclusive with EmailAddress: exactly one of them must be specified.<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### Email.spec.recipient.userRef
<sup><sup>[↩ Parent](#emailspecrecipient)</sup></sup>



UserRef references the User resource that will receive the message.
It is mutually exclusive with EmailAddress: exactly one of them must be specified.

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
          Name contain the name of the User resource that will receive the email.<br/>
        </td>
        <td>true</td>
      </tr></tbody>
</table>


### Email.spec.templateRef
<sup><sup>[↩ Parent](#emailspec)</sup></sup>



TemplateRef references the EmailTemplate that should be rendered.

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
          Name is the name of the EmailTemplate being referenced.<br/>
        </td>
        <td>true</td>
      </tr></tbody>
</table>


### Email.spec.variables[index]
<sup><sup>[↩ Parent](#emailspec)</sup></sup>



EmailVariable represents a name/value pair that will be injected into the template.

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
          Name of the variable as declared in the associated EmailTemplate.<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>value</b></td>
        <td>string</td>
        <td>
          Value provided for this variable.<br/>
        </td>
        <td>true</td>
      </tr></tbody>
</table>


### Email.status
<sup><sup>[↩ Parent](#email)</sup></sup>



EmailStatus captures the observed state of an Email.
Uses standard Kubernetes conditions to track both processing and delivery state.

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
        <td><b><a href="#emailstatusconditionsindex">conditions</a></b></td>
        <td>[]object</td>
        <td>
          Conditions represent the latest available observations of an object's current state.
Standard condition is "Delivered" which tracks email delivery status.<br/>
          <br/>
            <i>Default</i>: [map[lastTransitionTime:1970-01-01T00:00:00Z message:Waiting for email delivery reason:DeliveryPending status:Unknown type:Delivered]]<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>emailAddress</b></td>
        <td>string</td>
        <td>
          EmailAddress stores the final recipient address used for delivery,
after resolving any referenced User.<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>htmlBody</b></td>
        <td>string</td>
        <td>
          HTMLBody stores the rendered HTML content of the e-mail.<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>providerID</b></td>
        <td>string</td>
        <td>
          ProviderID is the identifier returned by the underlying email provider
(e.g. Resend) when the e-mail is accepted for delivery. It is usually
used to track the email delivery status (e.g. provider webhooks).<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>subject</b></td>
        <td>string</td>
        <td>
          Subject stores the subject line used for the e-mail.<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>textBody</b></td>
        <td>string</td>
        <td>
          TextBody stores the rendered plain-text content of the e-mail.<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### Email.status.conditions[index]
<sup><sup>[↩ Parent](#emailstatus)</sup></sup>



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

## EmailTemplate
<sup><sup>[↩ Parent](#notificationmiloapiscomv1alpha1 )</sup></sup>






EmailTemplate is the Schema for the email templates API.
It represents a reusable e-mail template that can be rendered by substituting
the declared variables.

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
      <td>notification.miloapis.com/v1alpha1</td>
      <td>true</td>
      </tr>
      <tr>
      <td><b>kind</b></td>
      <td>string</td>
      <td>EmailTemplate</td>
      <td>true</td>
      </tr>
      <tr>
      <td><b><a href="https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.27/#objectmeta-v1-meta">metadata</a></b></td>
      <td>object</td>
      <td>Refer to the Kubernetes API documentation for the fields of the `metadata` field.</td>
      <td>true</td>
      </tr><tr>
        <td><b><a href="#emailtemplatespec">spec</a></b></td>
        <td>object</td>
        <td>
          EmailTemplateSpec defines the desired state of EmailTemplate.
It contains the subject, content, and declared variables.<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b><a href="#emailtemplatestatus">status</a></b></td>
        <td>object</td>
        <td>
          EmailTemplateStatus captures the observed state of an EmailTemplate.
Right now we only expose standard Kubernetes conditions so callers can
determine whether the template is ready for use.<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### EmailTemplate.spec
<sup><sup>[↩ Parent](#emailtemplate)</sup></sup>



EmailTemplateSpec defines the desired state of EmailTemplate.
It contains the subject, content, and declared variables.

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
        <td><b>htmlBody</b></td>
        <td>string</td>
        <td>
          HTMLBody is the string for the HTML representation of the message.<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>subject</b></td>
        <td>string</td>
        <td>
          Subject is the string that composes the email subject line.<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>textBody</b></td>
        <td>string</td>
        <td>
          TextBody is the Go template string for the plain-text representation of the message.<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b><a href="#emailtemplatespecvariablesindex">variables</a></b></td>
        <td>[]object</td>
        <td>
          Variables enumerates all variables that can be referenced inside the template expressions.<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### EmailTemplate.spec.variables[index]
<sup><sup>[↩ Parent](#emailtemplatespec)</sup></sup>



TemplateVariable declares a variable that can be referenced in the template body or subject.
Each variable must be listed here so that callers know which parameters are expected.

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
          Name is the identifier of the variable as it appears inside the Go template (e.g. {{.UserName}}).<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>required</b></td>
        <td>boolean</td>
        <td>
          Required indicates whether the variable must be provided when rendering the template.<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>type</b></td>
        <td>enum</td>
        <td>
          Type provides a hint about the expected value of this variable (e.g. plain string or URL).<br/>
          <br/>
            <i>Enum</i>: string, url<br/>
        </td>
        <td>true</td>
      </tr></tbody>
</table>


### EmailTemplate.status
<sup><sup>[↩ Parent](#emailtemplate)</sup></sup>



EmailTemplateStatus captures the observed state of an EmailTemplate.
Right now we only expose standard Kubernetes conditions so callers can
determine whether the template is ready for use.

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
        <td><b><a href="#emailtemplatestatusconditionsindex">conditions</a></b></td>
        <td>[]object</td>
        <td>
          Conditions represent the latest available observations of an object's current state.<br/>
          <br/>
            <i>Default</i>: [map[lastTransitionTime:1970-01-01T00:00:00Z message:Waiting for control plane to reconcile reason:Unknown status:Unknown type:Ready]]<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### EmailTemplate.status.conditions[index]
<sup><sup>[↩ Parent](#emailtemplatestatus)</sup></sup>



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

## Note
<sup><sup>[↩ Parent](#notificationmiloapiscomv1alpha1 )</sup></sup>






Note is the Schema for the notes API.
It represents a note attached to a contact.

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
      <td>notification.miloapis.com/v1alpha1</td>
      <td>true</td>
      </tr>
      <tr>
      <td><b>kind</b></td>
      <td>string</td>
      <td>Note</td>
      <td>true</td>
      </tr>
      <tr>
      <td><b><a href="https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.27/#objectmeta-v1-meta">metadata</a></b></td>
      <td>object</td>
      <td>Refer to the Kubernetes API documentation for the fields of the `metadata` field.</td>
      <td>true</td>
      </tr><tr>
        <td><b><a href="#notespec">spec</a></b></td>
        <td>object</td>
        <td>
          NoteSpec defines the desired state of Note.<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b><a href="#notestatus">status</a></b></td>
        <td>object</td>
        <td>
          NoteStatus defines the observed state of Note.<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### Note.spec
<sup><sup>[↩ Parent](#note)</sup></sup>



NoteSpec defines the desired state of Note.

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
        <td><b>contactRef</b></td>
        <td>string</td>
        <td>
          ContactRef is the name of the Contact this note is attached to.<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>content</b></td>
        <td>string</td>
        <td>
          Content is the text content of the note.<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>action</b></td>
        <td>string</td>
        <td>
          Action is an optional follow-up action.<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>actionTime</b></td>
        <td>string</td>
        <td>
          ActionTime is the timestamp for the follow-up action.<br/>
          <br/>
            <i>Format</i>: date-time<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>interactionTime</b></td>
        <td>string</td>
        <td>
          InteractionTime is the timestamp of the interaction.
If not specified, it defaults to the creation timestamp of the note.<br/>
          <br/>
            <i>Format</i>: date-time<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### Note.status
<sup><sup>[↩ Parent](#note)</sup></sup>



NoteStatus defines the observed state of Note.

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
        <td><b><a href="#notestatusconditionsindex">conditions</a></b></td>
        <td>[]object</td>
        <td>
          Conditions represent the latest available observations of an object's state<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### Note.status.conditions[index]
<sup><sup>[↩ Parent](#notestatus)</sup></sup>



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

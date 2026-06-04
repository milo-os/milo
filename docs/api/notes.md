# API Reference

Packages:

- [notes.miloapis.com/v1alpha1](#notesmiloapiscomv1alpha1)

# notes.miloapis.com/v1alpha1

Resource Types:

- [ClusterNote](#clusternote)

- [Note](#note)




## ClusterNote
<sup><sup>[↩ Parent](#notesmiloapiscomv1alpha1 )</sup></sup>






ClusterNote is the Schema for the cluster-scoped notes API.
It represents a note attached to a cluster-scoped subject resource.

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
      <td>notes.miloapis.com/v1alpha1</td>
      <td>true</td>
      </tr>
      <tr>
      <td><b>kind</b></td>
      <td>string</td>
      <td>ClusterNote</td>
      <td>true</td>
      </tr>
      <tr>
      <td><b><a href="https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.27/#objectmeta-v1-meta">metadata</a></b></td>
      <td>object</td>
      <td>Refer to the Kubernetes API documentation for the fields of the `metadata` field.</td>
      <td>true</td>
      </tr><tr>
        <td><b><a href="#clusternotespec">spec</a></b></td>
        <td>object</td>
        <td>
          NoteSpec defines the desired state of Note.<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b><a href="#clusternotestatus">status</a></b></td>
        <td>object</td>
        <td>
          NoteStatus defines the observed state of Note<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### ClusterNote.spec
<sup><sup>[↩ Parent](#clusternote)</sup></sup>



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
        <td><b>content</b></td>
        <td>string</td>
        <td>
          Content is the text content of the note.<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b><a href="#clusternotespecsubjectref">subjectRef</a></b></td>
        <td>object</td>
        <td>
          Subject is a reference to the subject of the note.<br/>
          <br/>
            <i>Validations</i>:<li>type(oldSelf) == null_type || self == oldSelf: subject type is immutable</li>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b><a href="#clusternotespeccreatorref">creatorRef</a></b></td>
        <td>object</td>
        <td>
          CreatorRef is a reference to the user that created the note.
Defaults to the user that created the note.<br/>
          <br/>
            <i>Validations</i>:<li>type(oldSelf) == null_type || self == oldSelf: creatorRef type is immutable</li>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>followUp</b></td>
        <td>boolean</td>
        <td>
          FollowUp indicates whether this note requires follow-up.
When true, the note is being actively tracked for further action.<br/>
          <br/>
            <i>Default</i>: false<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>interactionTime</b></td>
        <td>string</td>
        <td>
          InteractionTime is the timestamp of the interaction with the subject.<br/>
          <br/>
            <i>Format</i>: date-time<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>nextAction</b></td>
        <td>string</td>
        <td>
          NextAction is an optional follow-up action.<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>nextActionTime</b></td>
        <td>string</td>
        <td>
          NextActionTime is the timestamp for the follow-up action.<br/>
          <br/>
            <i>Format</i>: date-time<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### ClusterNote.spec.subjectRef
<sup><sup>[↩ Parent](#clusternotespec)</sup></sup>



Subject is a reference to the subject of the note.

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
        <td>string</td>
        <td>
          APIGroup is the group for the resource being referenced.<br/>
        </td>
        <td>true</td>
      </tr><tr>
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
        <td><b>namespace</b></td>
        <td>string</td>
        <td>
          Namespace is the namespace of resource being referenced.
Required for namespace-scoped resources. Omitted for cluster-scoped resources.<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### ClusterNote.spec.creatorRef
<sup><sup>[↩ Parent](#clusternotespec)</sup></sup>



CreatorRef is a reference to the user that created the note.
Defaults to the user that created the note.

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


### ClusterNote.status
<sup><sup>[↩ Parent](#clusternote)</sup></sup>



NoteStatus defines the observed state of Note

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
        <td><b><a href="#clusternotestatusconditionsindex">conditions</a></b></td>
        <td>[]object</td>
        <td>
          Conditions provide conditions that represent the current status of the Note.<br/>
          <br/>
            <i>Default</i>: [map[lastTransitionTime:1970-01-01T00:00:00Z message:Waiting for control plane to reconcile reason:Unknown status:Unknown type:Ready]]<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>createdBy</b></td>
        <td>string</td>
        <td>
          CreatedBy is the email of the user that created the note.<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### ClusterNote.status.conditions[index]
<sup><sup>[↩ Parent](#clusternotestatus)</sup></sup>



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
<sup><sup>[↩ Parent](#notesmiloapiscomv1alpha1 )</sup></sup>






Note is the Schema for the notes API.
It represents a namespaced note attached to a subject resource.

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
      <td>notes.miloapis.com/v1alpha1</td>
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
          NoteStatus defines the observed state of Note<br/>
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
        <td><b>content</b></td>
        <td>string</td>
        <td>
          Content is the text content of the note.<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b><a href="#notespecsubjectref">subjectRef</a></b></td>
        <td>object</td>
        <td>
          Subject is a reference to the subject of the note.<br/>
          <br/>
            <i>Validations</i>:<li>type(oldSelf) == null_type || self == oldSelf: subject type is immutable</li>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b><a href="#notespeccreatorref">creatorRef</a></b></td>
        <td>object</td>
        <td>
          CreatorRef is a reference to the user that created the note.
Defaults to the user that created the note.<br/>
          <br/>
            <i>Validations</i>:<li>type(oldSelf) == null_type || self == oldSelf: creatorRef type is immutable</li>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>followUp</b></td>
        <td>boolean</td>
        <td>
          FollowUp indicates whether this note requires follow-up.
When true, the note is being actively tracked for further action.<br/>
          <br/>
            <i>Default</i>: false<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>interactionTime</b></td>
        <td>string</td>
        <td>
          InteractionTime is the timestamp of the interaction with the subject.<br/>
          <br/>
            <i>Format</i>: date-time<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>nextAction</b></td>
        <td>string</td>
        <td>
          NextAction is an optional follow-up action.<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>nextActionTime</b></td>
        <td>string</td>
        <td>
          NextActionTime is the timestamp for the follow-up action.<br/>
          <br/>
            <i>Format</i>: date-time<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### Note.spec.subjectRef
<sup><sup>[↩ Parent](#notespec)</sup></sup>



Subject is a reference to the subject of the note.

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
        <td>string</td>
        <td>
          APIGroup is the group for the resource being referenced.<br/>
        </td>
        <td>true</td>
      </tr><tr>
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
        <td><b>namespace</b></td>
        <td>string</td>
        <td>
          Namespace is the namespace of resource being referenced.
Required for namespace-scoped resources. Omitted for cluster-scoped resources.<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### Note.spec.creatorRef
<sup><sup>[↩ Parent](#notespec)</sup></sup>



CreatorRef is a reference to the user that created the note.
Defaults to the user that created the note.

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


### Note.status
<sup><sup>[↩ Parent](#note)</sup></sup>



NoteStatus defines the observed state of Note

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
          Conditions provide conditions that represent the current status of the Note.<br/>
          <br/>
            <i>Default</i>: [map[lastTransitionTime:1970-01-01T00:00:00Z message:Waiting for control plane to reconcile reason:Unknown status:Unknown type:Ready]]<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>createdBy</b></td>
        <td>string</td>
        <td>
          CreatedBy is the email of the user that created the note.<br/>
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

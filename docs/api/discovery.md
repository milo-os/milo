# API Reference

Packages:

- [discovery.miloapis.com/v1alpha1](#discoverymiloapiscomv1alpha1)

# discovery.miloapis.com/v1alpha1

Resource Types:

- [DiscoveryContextPolicy](#discoverycontextpolicy)




## DiscoveryContextPolicy
<sup><sup>[↩ Parent](#discoverymiloapiscomv1alpha1 )</sup></sup>






DiscoveryContextPolicy defines the parent contexts in which API resources are visible
in discovery responses.

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
      <td>discovery.miloapis.com/v1alpha1</td>
      <td>true</td>
      </tr>
      <tr>
      <td><b>kind</b></td>
      <td>string</td>
      <td>DiscoveryContextPolicy</td>
      <td>true</td>
      </tr>
      <tr>
      <td><b><a href="https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.27/#objectmeta-v1-meta">metadata</a></b></td>
      <td>object</td>
      <td>Refer to the Kubernetes API documentation for the fields of the `metadata` field.</td>
      <td>true</td>
      </tr><tr>
        <td><b><a href="#discoverycontextpolicyspec">spec</a></b></td>
        <td>object</td>
        <td>
          DiscoveryContextPolicySpec defines the desired state of DiscoveryContextPolicy<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b><a href="#discoverycontextpolicystatus">status</a></b></td>
        <td>object</td>
        <td>
          DiscoveryContextPolicyStatus defines the observed state of DiscoveryContextPolicy<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### DiscoveryContextPolicy.spec
<sup><sup>[↩ Parent](#discoverycontextpolicy)</sup></sup>



DiscoveryContextPolicySpec defines the desired state of DiscoveryContextPolicy

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
        <td><b><a href="#discoverycontextpolicyspecrulesindex">rules</a></b></td>
        <td>[]object</td>
        <td>
          Rules define which resources are visible in which parent contexts.<br/>
        </td>
        <td>true</td>
      </tr></tbody>
</table>


### DiscoveryContextPolicy.spec.rules[index]
<sup><sup>[↩ Parent](#discoverycontextpolicyspec)</sup></sup>



DiscoveryContextPolicyRule defines context visibility for a set of resources in a group.

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
        <td><b>contexts</b></td>
        <td>[]enum</td>
        <td>
          Contexts lists the parent contexts where these resources are visible.
Valid values are Platform, Organization, Project, and User.<br/>
          <br/>
            <i>Enum</i>: Platform, Organization, Project, User<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>group</b></td>
        <td>string</td>
        <td>
          Group is the API group. Empty string means the core group. Use "*" to match all groups.<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>resources</b></td>
        <td>[]string</td>
        <td>
          Resources is the list of resource plural names. Use ["*"] to match all resources in the group.<br/>
        </td>
        <td>true</td>
      </tr></tbody>
</table>


### DiscoveryContextPolicy.status
<sup><sup>[↩ Parent](#discoverycontextpolicy)</sup></sup>



DiscoveryContextPolicyStatus defines the observed state of DiscoveryContextPolicy

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
        <td><b><a href="#discoverycontextpolicystatusconditionsindex">conditions</a></b></td>
        <td>[]object</td>
        <td>
          <br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>observedGeneration</b></td>
        <td>integer</td>
        <td>
          <br/>
          <br/>
            <i>Format</i>: int64<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### DiscoveryContextPolicy.status.conditions[index]
<sup><sup>[↩ Parent](#discoverycontextpolicystatus)</sup></sup>



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

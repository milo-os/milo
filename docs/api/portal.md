# API Reference

Packages:

- [portal.miloapis.com/v1alpha1](#portalmiloapiscomv1alpha1)

# portal.miloapis.com/v1alpha1

Resource Types:

- [ConsumerPortalPlugin](#consumerportalplugin)

- [ProviderPortalPlugin](#providerportalplugin)




## ConsumerPortalPlugin
<sup><sup>[↩ Parent](#portalmiloapiscomv1alpha1 )</sup></sup>






ConsumerPortalPlugin registers a service's portal plugin for cloud-portal,
the customer-facing portal. Service teams do not create these directly —
they are fanned out by the services-operator from a ServiceConfiguration's
spec.userInterface.consumer block.

### How It Works
- A service team sets spec.userInterface.consumer on their ServiceConfiguration
- The services-operator fans that out into a ConsumerPortalPlugin here
- cloud-portal watches ConsumerPortalPlugin, fetches the manifest at
  spec.assets, and writes back Status reporting what it found
- Extensions declared in the manifest (portal.nav/project, portal.page/project,
  portal.card/project-home) render inside a project, gated by spec.visibility

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
      <td>portal.miloapis.com/v1alpha1</td>
      <td>true</td>
      </tr>
      <tr>
      <td><b>kind</b></td>
      <td>string</td>
      <td>ConsumerPortalPlugin</td>
      <td>true</td>
      </tr>
      <tr>
      <td><b><a href="https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.27/#objectmeta-v1-meta">metadata</a></b></td>
      <td>object</td>
      <td>Refer to the Kubernetes API documentation for the fields of the `metadata` field.</td>
      <td>true</td>
      </tr><tr>
        <td><b><a href="#consumerportalpluginspec">spec</a></b></td>
        <td>object</td>
        <td>
          ConsumerPortalPluginSpec defines the desired state of ConsumerPortalPlugin.<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b><a href="#consumerportalpluginstatus">status</a></b></td>
        <td>object</td>
        <td>
          ConsumerPortalPluginStatus reports the portal's most recent manifest
resolution for this plugin. Written by cloud-portal (the consuming
host), not by the services-operator that writes Spec.<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### ConsumerPortalPlugin.spec
<sup><sup>[↩ Parent](#consumerportalplugin)</sup></sup>



ConsumerPortalPluginSpec defines the desired state of ConsumerPortalPlugin.

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
        <td><b><a href="#consumerportalpluginspecassets">assets</a></b></td>
        <td>object</td>
        <td>
          Assets locates the plugin's built Module Federation bundle.<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>displayName</b></td>
        <td>string</td>
        <td>
          DisplayName is the human-readable name shown in the portal UI (e.g. a
"dev plugin" badge, error states).<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>slug</b></td>
        <td>string</td>
        <td>
          Slug is the unique DNS label identifying this plugin. It is the URL
segment and the same-origin asset-proxy segment
(/api/plugins/<slug>/...). Immutable after creation.<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b><a href="#consumerportalpluginspecvisibility">visibility</a></b></td>
        <td>object</td>
        <td>
          Visibility gates whether a project sees this plugin's extensions.<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>deprecated</b></td>
        <td>boolean</td>
        <td>
          Deprecated marks the winning ServiceConfiguration as deprecated. The
portal may use this to warn operators without hiding the plugin.<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>suspend</b></td>
        <td>boolean</td>
        <td>
          Suspend is a platform-operator kill switch. A suspended plugin is
never served, regardless of manifest health.<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### ConsumerPortalPlugin.spec.assets
<sup><sup>[↩ Parent](#consumerportalpluginspec)</sup></sup>



Assets locates the plugin's built Module Federation bundle.

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
        <td><b>baseURL</b></td>
        <td>string</td>
        <td>
          BaseURL is the HTTPS origin, operated by the service team, serving the
plugin's built assets (remoteEntry.js, chunks, and the manifest at
ManifestPath).<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>caBundle</b></td>
        <td>string</td>
        <td>
          CABundle is an optional PEM-encoded CA certificate bundle for an
internal CA fronting BaseURL. Server-side only — never sent to the
browser.<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>manifestPath</b></td>
        <td>string</td>
        <td>
          ManifestPath is the path to plugin-manifest.json under BaseURL.
Defaults to "/plugin-manifest.json".<br/>
          <br/>
            <i>Default</i>: /plugin-manifest.json<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### ConsumerPortalPlugin.spec.visibility
<sup><sup>[↩ Parent](#consumerportalpluginspec)</sup></sup>



Visibility gates whether a project sees this plugin's extensions.

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
        <td><b>entitlement</b></td>
        <td>enum</td>
        <td>
          Entitlement controls project-level gating. See PluginEntitlementRequirement.<br/>
          <br/>
            <i>Enum</i>: Required, None<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>featureFlag</b></td>
        <td>string</td>
        <td>
          FeatureFlag, when set, additionally gates visibility on an OpenFeature
flag key evaluated by cloud-portal.<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### ConsumerPortalPlugin.status
<sup><sup>[↩ Parent](#consumerportalplugin)</sup></sup>



ConsumerPortalPluginStatus reports the portal's most recent manifest
resolution for this plugin. Written by cloud-portal (the consuming
host), not by the services-operator that writes Spec.

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
        <td><b><a href="#consumerportalpluginstatusconditionsindex">conditions</a></b></td>
        <td>[]object</td>
        <td>
          Conditions communicate manifest health. See PluginDiscovered,
PluginCompatible, PluginReady.<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b><a href="#consumerportalpluginstatusmanifest">manifest</a></b></td>
        <td>object</td>
        <td>
          Manifest is a snapshot of the most recently resolved manifest, when
discovery has succeeded at least once.<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>observedGeneration</b></td>
        <td>integer</td>
        <td>
          ObservedGeneration is the most recent spec generation the writing
portal has processed.<br/>
          <br/>
            <i>Format</i>: int64<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### ConsumerPortalPlugin.status.conditions[index]
<sup><sup>[↩ Parent](#consumerportalpluginstatus)</sup></sup>



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


### ConsumerPortalPlugin.status.manifest
<sup><sup>[↩ Parent](#consumerportalpluginstatus)</sup></sup>



Manifest is a snapshot of the most recently resolved manifest, when
discovery has succeeded at least once.

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
        <td><b>digest</b></td>
        <td>string</td>
        <td>
          Digest is a "sha256:..." digest of the fetched manifest bytes.<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>fetchedAt</b></td>
        <td>string</td>
        <td>
          FetchedAt is when the portal last fetched this manifest.<br/>
          <br/>
            <i>Format</i>: date-time<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>sdkRange</b></td>
        <td>string</td>
        <td>
          SDKRange is the manifest's declared sdk.range.<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>version</b></td>
        <td>string</td>
        <td>
          Version is the manifest's own declared version.<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>extensions</b></td>
        <td>map[string]integer</td>
        <td>
          Extensions counts declared extensions by type, e.g. {"portal.nav/project": 1}.<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>

## ProviderPortalPlugin
<sup><sup>[↩ Parent](#portalmiloapiscomv1alpha1 )</sup></sup>






ProviderPortalPlugin registers a service's portal plugin for staff-portal,
the internal operator portal. Service teams do not create these directly —
they are fanned out by the services-operator from a ServiceConfiguration's
spec.userInterface.provider block.

### How It Works
- A service team sets spec.userInterface.provider on their ServiceConfiguration
- The services-operator fans that out into a ProviderPortalPlugin here
- staff-portal watches ProviderPortalPlugin, fetches the manifest at
  spec.assets, and writes back Status reporting what it found
- Extensions declared in the manifest render platform-wide, with no
  project/organization scoping: portal.nav/platform (a top-level nav
  item), portal.page/platform (a platform-wide routed page), or
  portal.resource/platform (a resource type staff-portal's own Resources
  list queries and renders itself — no plugin code runs to produce those
  rows)

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
      <td>portal.miloapis.com/v1alpha1</td>
      <td>true</td>
      </tr>
      <tr>
      <td><b>kind</b></td>
      <td>string</td>
      <td>ProviderPortalPlugin</td>
      <td>true</td>
      </tr>
      <tr>
      <td><b><a href="https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.27/#objectmeta-v1-meta">metadata</a></b></td>
      <td>object</td>
      <td>Refer to the Kubernetes API documentation for the fields of the `metadata` field.</td>
      <td>true</td>
      </tr><tr>
        <td><b><a href="#providerportalpluginspec">spec</a></b></td>
        <td>object</td>
        <td>
          ProviderPortalPluginSpec defines the desired state of ProviderPortalPlugin.<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b><a href="#providerportalpluginstatus">status</a></b></td>
        <td>object</td>
        <td>
          ProviderPortalPluginStatus reports the portal's most recent manifest
resolution for this plugin. Written by staff-portal (the consuming
host), not by the services-operator that writes Spec. Shape mirrors
ConsumerPortalPluginStatus — see PluginDiscovered/PluginCompatible/
PluginReady and PluginManifestSnapshot in consumerportalplugin_types.go.<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### ProviderPortalPlugin.spec
<sup><sup>[↩ Parent](#providerportalplugin)</sup></sup>



ProviderPortalPluginSpec defines the desired state of ProviderPortalPlugin.

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
        <td><b><a href="#providerportalpluginspecassets">assets</a></b></td>
        <td>object</td>
        <td>
          Assets locates the plugin's built Module Federation bundle.<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>displayName</b></td>
        <td>string</td>
        <td>
          DisplayName is the human-readable name shown in the portal UI (e.g. a
"dev plugin" badge, error states).<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>slug</b></td>
        <td>string</td>
        <td>
          Slug is the unique DNS label identifying this plugin. It is the URL
segment and the same-origin asset-proxy segment
(/api/plugins/<slug>/...). Immutable after creation.<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>deprecated</b></td>
        <td>boolean</td>
        <td>
          Deprecated marks the winning ServiceConfiguration as deprecated. The
portal may use this to warn operators without hiding the plugin.<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>suspend</b></td>
        <td>boolean</td>
        <td>
          Suspend is a platform-operator kill switch. A suspended plugin is
never served, regardless of manifest health.<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### ProviderPortalPlugin.spec.assets
<sup><sup>[↩ Parent](#providerportalpluginspec)</sup></sup>



Assets locates the plugin's built Module Federation bundle.

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
        <td><b>baseURL</b></td>
        <td>string</td>
        <td>
          BaseURL is the HTTPS origin, operated by the service team, serving the
plugin's built assets (remoteEntry.js, chunks, and the manifest at
ManifestPath).<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>caBundle</b></td>
        <td>string</td>
        <td>
          CABundle is an optional PEM-encoded CA certificate bundle for an
internal CA fronting BaseURL. Server-side only — never sent to the
browser.<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>manifestPath</b></td>
        <td>string</td>
        <td>
          ManifestPath is the path to plugin-manifest.json under BaseURL.
Defaults to "/plugin-manifest.json".<br/>
          <br/>
            <i>Default</i>: /plugin-manifest.json<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### ProviderPortalPlugin.status
<sup><sup>[↩ Parent](#providerportalplugin)</sup></sup>



ProviderPortalPluginStatus reports the portal's most recent manifest
resolution for this plugin. Written by staff-portal (the consuming
host), not by the services-operator that writes Spec. Shape mirrors
ConsumerPortalPluginStatus — see PluginDiscovered/PluginCompatible/
PluginReady and PluginManifestSnapshot in consumerportalplugin_types.go.

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
        <td><b><a href="#providerportalpluginstatusconditionsindex">conditions</a></b></td>
        <td>[]object</td>
        <td>
          <br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b><a href="#providerportalpluginstatusmanifest">manifest</a></b></td>
        <td>object</td>
        <td>
          PluginManifestSnapshot is a portal-resolved snapshot of a live
plugin-manifest.json.<br/>
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


### ProviderPortalPlugin.status.conditions[index]
<sup><sup>[↩ Parent](#providerportalpluginstatus)</sup></sup>



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


### ProviderPortalPlugin.status.manifest
<sup><sup>[↩ Parent](#providerportalpluginstatus)</sup></sup>



PluginManifestSnapshot is a portal-resolved snapshot of a live
plugin-manifest.json.

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
        <td><b>digest</b></td>
        <td>string</td>
        <td>
          Digest is a "sha256:..." digest of the fetched manifest bytes.<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>fetchedAt</b></td>
        <td>string</td>
        <td>
          FetchedAt is when the portal last fetched this manifest.<br/>
          <br/>
            <i>Format</i>: date-time<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>sdkRange</b></td>
        <td>string</td>
        <td>
          SDKRange is the manifest's declared sdk.range.<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>version</b></td>
        <td>string</td>
        <td>
          Version is the manifest's own declared version.<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>extensions</b></td>
        <td>map[string]integer</td>
        <td>
          Extensions counts declared extensions by type, e.g. {"portal.nav/project": 1}.<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>

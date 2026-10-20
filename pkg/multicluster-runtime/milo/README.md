# Milo Provider Discovery Modes

The Milo provider supports two distinct discovery modes for connecting to
project clusters, each optimized for different deployment scenarios and
operational requirements.

## ProjectControlPlane

ProjectControlPlane mode uses internal service discovery within Milo's
infrastructure control plane to establish direct connections to project
clusters.

### How it Works

- Watches `ProjectControlPlane` resources in the infrastructure control plane
- Establishes connections using internal Kubernetes service addresses
- Connects directly to `milo-apiserver.project-{uid}.svc.cluster.local:6443`
- Requires the `ControlPlaneReady` condition to be true before engagement

### When to Use

- When the operator runs within the same Kubernetes cluster as the
  infrastructure control plane
- For internal operations and system-level controllers
- When network policies allow internal service-to-service communication
- Optimal performance due to direct internal networking

### Architecture

```mermaid
flowchart TD
  operator[Multi-Cluster Operator]


  subgraph infrastructureControlPlane[Infrastructure Control Plane]
    datumProjectControlPlanes[ProjectControlPlanes]@{ shape: procs }
  end

  subgraph "Milo APIs"
    subgraph "Core"
      datumProjects[Projects]@{ shape: procs }
    end

    subgraph "Projects"
      datumProjectAControlPlane[Project A Control Plane]
      datumProjectBControlPlane[Project B Control Plane]
    end
  end

  datumProjectAControlPlane <--> operator
  datumProjectBControlPlane <--> operator
  datumProjectControlPlanes <--project discovery--> operator


```

## Project

Project mode discovers clusters through the external Milo APIs and connects via
public endpoints.

### How it Works

- Watches `Project` resources through the core Milo APIs
- Constructs external API endpoints using the pattern:
  `/apis/resourcemanager.miloapis.com/v1alpha1/projects/{name}/control-plane`
- Requires the `Ready` condition to be true before engagement
- Uses external authentication and networking

### When to Use

- When the operator runs outside the infrastructure control plane
- For third-party integrations and external operators
- When connecting from remote locations or different clusters
- For customer-facing operators that need project isolation

### Architecture

```mermaid
flowchart TD
  operator[Multi-Cluster Operator]

  subgraph infrastructureControlPlane[Infrastructure Control Plane]
    datumProjectControlPlanes[ProjectControlPlanes]@{ shape: procs }
  end

  subgraph "Milo APIs"
    subgraph "Core"
      datumProjects[Projects]@{ shape: procs }
    end

    subgraph "Projects"
      datumProjectAControlPlane[Project A Control Plane]
      datumProjectBControlPlane[Project B Control Plane]
    end
  end

  datumProjectAControlPlane <--> operator
  datumProjectBControlPlane <--> operator
  datumProjects <--project discovery--> operator
```

## Configuration

The discovery mode is controlled by the `InternalServiceDiscovery` option:

```go
opts := milo.Options{
    InternalServiceDiscovery: true,  // Use ProjectControlPlane mode
    // InternalServiceDiscovery: false, // Use Project mode (default)
}
```

## Metrics

The provider registers these metrics with controller-runtime's registry, so
they appear on the consuming manager's `/metrics` endpoint with no extra
wiring. Every series carries a `provider` label set to the provider's
controller name (`ControllerName`, default `projectcontrolplane`).

| Metric | Type | Meaning |
|---|---|---|
| `milo_cluster_provider_engaged_projects` | gauge | Projects engaged with the multicluster manager |
| `milo_cluster_provider_initial_sync_complete` | gauge | `1` once every project in the initial list is processed; `0` from provider start until then |
| `milo_cluster_provider_initial_sync_duration_seconds` | gauge | Seconds from provider start until the initial list was processed |
| `milo_cluster_provider_registration_failures_total` | counter | Failed project registrations, by `stage`: `create_cluster`, `index`, `cache_sync`, `engage` |

`milo_cluster_provider_initial_sync_complete` staying at `0` well past the
usual initial sync duration means the provider is not getting through its
projects, for example because engagement is blocked. Alert on that rather than
on the engaged count, which legitimately changes as projects come and go.

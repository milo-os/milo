# Getting Started with Milo

This guide will help you set up a complete Milo development environment on your local machine.

## Prerequisites

Before you begin, ensure you have the following tools installed:

- **Docker** (20.10+): Container runtime for building images
  - [Installation guide](https://docs.docker.com/get-docker/)
- **Kind** (0.20+): Kubernetes in Docker for local clusters
  - [Installation guide](https://kind.sigs.k8s.io/docs/user/quick-start/#installation)
- **kubectl** (1.28+): Kubernetes command-line tool
  - [Installation guide](https://kubernetes.io/docs/tasks/tools/)
- **Task** (3.31+): Task runner for automation
  - [Installation guide](https://taskfile.dev/installation/)

Verify your installations:
```bash
docker --version
kind --version
kubectl version --client
task --version
```

## Quick Setup

The fastest way to get Milo running:

```bash
# Clone the repository
git clone https://github.com/milo-os/milo.git
cd milo

# Enable remote task files to be used
export TASK_X_REMOTE_TASKFILES=1
# Deploy everything with a single command
task dev:setup
```

This command orchestrates the entire setup process:
1. Creates a Kind cluster named `test-infra`
2. Installs required Kubernetes components (cert-manager, Gateway API)
3. Builds the Milo container image
4. Deploys etcd for storage
5. Deploys the Milo API server
6. Deploys the Milo controller manager
7. Installs all Custom Resource Definitions (CRDs)
8. Configures authentication and networking

The process typically takes 3-5 minutes depending on your system.

## Verifying Installation

### Check Milo Version

You can verify your Milo installation and get detailed version information:

```bash
# From a running container (if you built one)
docker run --rm ghcr.io/milo-os/milo:dev version

# Multiple output formats available
milo version                    # Default detailed output
milo version --output json      # JSON format for scripting
milo version --output yaml      # YAML format
milo version --output short     # Short version only
```

The version command displays:
- **Git version**: Semantic version with commit hash (e.g., `v0.0.0-master+0133af2`)
- **Git commit**: Full commit hash and tree state (clean/dirty)
- **Build date**: When the binary was compiled (ISO 8601 format)
- **Go version**: Go compiler version used
- **Platform**: Target architecture (e.g., `linux/amd64`, `darwin/arm64`)

This information is useful for:
- Troubleshooting issues (include in bug reports)
- Verifying you're running the expected version
- Confirming successful builds and deployments

### Check API Server Status

Verify the Milo API server is responding:

```bash
# Check API server health
task kubectl -- get --raw /healthz

# List available API resources (should show Milo custom resources)
task kubectl -- api-resources | grep miloapis
```

## Optional: Enable Observability Stack

The test infrastructure includes an optional observability stack with metrics, logs, and tracing:

```bash
# Deploy observability stack (Victoria Metrics, Loki, Tempo, Grafana)
task test-infra:install-observability
```

This provides:
- **Grafana**: Web UI for dashboards and visualization at `http://localhost:3000`
- **Victoria Metrics**: Prometheus-compatible metrics storage
- **Loki**: Log aggregation for centralized logging
- **Tempo**: Distributed tracing backend

The observability stack is optional but recommended for development to monitor Milo's performance and troubleshoot issues.

## Accessing Milo

### Using kubectl

The deployment creates a pre-configured kubeconfig file at `.milo/kubeconfig`. You can use kubectl in two ways:

#### Method 1: Using the task kubectl command (Recommended)

```bash
# List available API resources (Milo custom resources only)
task kubectl -- api-resources | grep miloapis

# Get API server version info
task kubectl -- version
```

#### Method 2: Setting KUBECONFIG directly

```bash
# Set the kubeconfig
export KUBECONFIG=.milo/kubeconfig

# List available API resources (Milo custom resources only)
kubectl api-resources | grep miloapis

# Get API server version info
kubectl version
```

### Understanding the Two kubectl Commands

Milo uses two different kubectl commands for different purposes:

- **`task kubectl --`**: Interacts with the **Milo API server** (your custom resources like organizations, projects, users)
- **`task test-infra:kubectl --`**: Interacts with the **test-infra Kubernetes cluster** (infrastructure components like pods, services, deployments)

Use `task kubectl --` when working with Milo resources:
```bash
task kubectl -- get organizations
task kubectl -- get projects
task kubectl -- get users
```

Use `task test-infra:kubectl --` when managing infrastructure:
```bash
task test-infra:kubectl -- get pods -n milo-system
task test-infra:kubectl -- logs -n milo-system -l app.kubernetes.io/name=milo-apiserver
```

### API Endpoints

- **Gateway URL**: `https://localhost:30443`
- **Direct API**: `https://localhost:30443/apis/`

### Authentication

Two authentication tokens are pre-configured with corresponding Milo User resources:

1. **Admin User** (`admin`)
   - Token: `test-admin-token`
   - Full cluster admin access
   - Member of `system:masters` group
   - Email: admin@test.local

2. **Test User** (`test-user`)
   - Token: `test-user-token`
   - Standard authenticated user
   - Member of `system:authenticated` group
   - Email: test-user@test.local

Example using curl with admin token:
```bash
curl -k -H "Authorization: Bearer test-admin-token" \
  https://localhost:30443/apis/resourcemanager.miloapis.com/v1alpha1/organizations
```

## Creating Resources

Milo includes sample resources to help you get started. These are located in `config/samples/`:

### Apply Sample Resources

```bash
# Create a sample organization
task kubectl -- apply -f config/samples/resourcemanager/v1alpha1/organization.yaml

# Create a sample project (requires the organization to exist first)
task kubectl -- apply -f config/samples/resourcemanager/v1alpha1/project.yaml

# Create a sample user
task kubectl -- apply -f config/samples/iam/v1alpha1/user.yaml

# Apply all samples in a directory
task kubectl -- apply -f config/samples/resourcemanager/v1alpha1/
task kubectl -- apply -f config/samples/iam/v1alpha1/
```

### Sample Resource Examples

The sample files demonstrate proper resource structure:

- **Organizations** (`config/samples/resourcemanager/v1alpha1/organization.yaml`): Top-level business entities
- **Projects** (`config/samples/resourcemanager/v1alpha1/project.yaml`): Resource groupings within organizations
- **Users** (`config/samples/iam/v1alpha1/user.yaml`): Identity management
- **Groups** (`config/samples/iam/v1alpha1/group.yaml`): User groupings for access control
- **Roles** (`config/samples/iam/v1alpha1/role.yaml`): Permission definitions
- **PolicyBindings** (`config/samples/iam/v1alpha1/policybinding.yaml`): Role assignments

## Viewing Resources

```bash
# List all organizations
task kubectl -- get organizations

# Get detailed organization info
task kubectl -- describe organization acme-corp

# List all projects across namespaces
task kubectl -- get projects -A

# View users
task kubectl -- get users

# Check organization memberships
task kubectl -- get organizationmemberships -n organization-acme-corp
```

## Development Workflow

### Rebuilding and Redeploying

When you make code changes:

```bash
# Quick rebuild and redeploy
task dev:redeploy
```

This rebuilds the image and restarts the deployments.

### Viewing Logs

```bash
# API Server logs (use test-infra kubectl for infrastructure cluster)
task test-infra:kubectl -- logs -n milo-system -l app.kubernetes.io/name=milo-apiserver -f

# Controller Manager logs (use test-infra kubectl for infrastructure cluster)
task test-infra:kubectl -- logs -n milo-system -l app.kubernetes.io/name=milo-controller-manager -f

# etcd logs (use test-infra kubectl for infrastructure cluster)
task test-infra:kubectl -- logs -n milo-system -l app.kubernetes.io/component=etcd
```

### Observability and Monitoring

If you deployed the observability stack, you can:

```bash
# Access Grafana dashboards
open http://localhost:3000

# View centralized logs in Grafana
# Navigate to Explore > Loki data source

# Monitor metrics and performance
# Use pre-configured dashboards for Kubernetes and Milo components
```

The observability stack automatically collects:
- **Metrics**: CPU, memory, request rates from Milo components
- **Logs**: Centralized logs from all pods in structured format
- **Traces**: Request tracing across Milo API calls (if enabled)
- **Dashboards**: Pre-configured views for system health

### Running Tests

```bash
# Run unit tests
task test:unit

# Run end-to-end tests (requires running cluster)
task test:end-to-end
```

## Troubleshooting

### Common Issues

#### Version mismatch or build issues
```bash
# Check if you're running the expected version
docker run --rm ghcr.io/milo-os/milo:dev version

# Rebuild with fresh version information
task dev:build

# Check version after rebuild
docker run --rm ghcr.io/milo-os/milo:dev version --output short
```

#### Cluster won't start
```bash
# Check if Docker is running
docker ps

# Remove existing cluster and retry
kind delete cluster --name test-infra
task dev:setup
```

#### API server not responding
```bash
# Check pod status (use test-infra kubectl for infrastructure cluster)
task test-infra:kubectl -- get pods -n milo-system

# Check API server logs (use test-infra kubectl for infrastructure cluster)
task test-infra:kubectl -- logs -n milo-system -l app.kubernetes.io/name=milo-apiserver --tail=50
```

#### Resources not being created
```bash
# Check controller manager logs (use test-infra kubectl for infrastructure cluster)
task test-infra:kubectl -- logs -n milo-system -l app.kubernetes.io/name=milo-controller-manager --tail=50

# Verify CRDs are installed in Milo API server
# Note: 'get crd' won't work - CRDs are not exposed in custom API servers
# Instead, check if the API resources are available:
task kubectl -- api-resources | grep miloapis
```

### Cleanup

To completely remove the test environment:

```bash
# Delete the Kind cluster (using test-infra task)
task test-infra:cluster-down

# Clean up generated files
rm -rf .task .test-infra
```

## Next Steps

- 📚 Browse the [API Reference](api/)

## Getting Help

- 🐛 [Report Issues](https://github.com/milo-os/milo/issues)
- 📧 Contact the [team on slack](https://slack.datum.net)

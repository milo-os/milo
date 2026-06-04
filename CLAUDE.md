# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with
code in this repository.

## Project Overview

Milo is a "business operating system" for product-led B2B companies built on
Kubernetes. It provides a comprehensive system of record for organizations,
projects, users, audit logs, and other business operations through a
Kubernetes-like API experience.

## Core Architecture

Milo is built on the Kubernetes API server library and provides its own
dedicated API server with custom resource definitions (CRDs). The system follows
standard Kubernetes controller patterns with multi-cluster capabilities.

### Key Components

- **API Server**: Built on k8s.io/apiserver library, provides the main API
  interface
- **Controllers**: Standard Kubernetes controllers that reconcile custom
  resources
- **Multi-cluster Runtime**: Cross-cluster resource management using
  sigs.k8s.io/multicluster-runtime
- **Provider Framework**: Extensible integration system for third-party services

### Resource Hierarchy

1. **Organizations** (cluster-scoped): Top-level business entities with types
   (Personal/Standard/Business)
2. **Projects** (namespaced): Resource organization units within organizations
3. **Users & Groups** (IAM): Identity management with RBAC
4. **OrganizationMemberships**: Link users to organizations
5. **ProjectControlPlanes**: Cross-cluster project provisioning

### API Structure

Custom resources are organized under three main API groups:
- `resourcemanager.miloapis.com/v1alpha1`: Organizations, Projects,
  OrganizationMemberships
- `iam.miloapis.com/v1alpha1`: Users, Groups, Roles, PolicyBindings
- `infrastructure.miloapis.com/v1alpha1`: ProjectControlPlanes

## Development Commands

### Code Generation
```bash
# Generate deepcopy, CRDs, webhooks, and RBAC
task generate

# Generate API documentation
task api-docs
```

### Development Certificates
```bash
# Generate self-signed certs for local webhook development
task generate-dev-certs
```

### Build and Run
```bash
# Build container image (preferred approach)
task dev:build

# For local development, use the complete development setup
task dev:setup    # Sets up test infrastructure + deploys Milo

# Access Milo API server (instead of running locally)
task kubectl -- get organizations
task kubectl -- apply -f config/samples/resourcemanager/v1alpha1/organization.yaml
```

### Testing
```bash
# Run unit tests
task test:unit

# Run end-to-end tests with Chainsaw
task test:end-to-end

# Individual test directories contain chainsaw-test.yaml files
# Use task commands instead of running chainsaw directly
```

### Development Environment
```bash
# PREFERRED: Use complete Task-based development setup
task dev:setup    # Creates test infrastructure + deploys Milo

# Alternative: Start local development dependencies only
docker-compose up -d    # Start OpenFGA, Zitadel, Mailhog

# Use Task commands for deployment instead of direct kubectl
task kubectl -- apply -k config/dev
```

### Test Infrastructure
```bash
# Deploy complete test infrastructure with etcd, API server, and controller manager
task dev:setup

# Deploy test infrastructure with OpenFGA authorization provider
task dev:setup:openfga

# Deploy only (assumes cluster already exists)
task dev:deploy           # Standard deployment
task dev:deploy:openfga   # With OpenFGA provider

# Switch to use Milo API server
export KUBECONFIG=.milo/kubeconfig

# Then test with Task wrapper (automatically uses .milo/kubeconfig):
task kubectl -- get organizations
task kubectl -- get projects  
task kubectl -- get users

# Task kubectl wrapper handles kubeconfig automatically
```

### Test Infrastructure Management
The Milo taskfile includes remote access to the `datum-cloud/test-infra` repository for managing test environments. This uses Go Task's experimental remote taskfiles feature.

```bash
# Direct access to test-infra commands (requires TASK_X_REMOTE_TASKFILES=1)
task test-infra:cluster-up        # Start a new test cluster
task test-infra:cluster-down      # Tear down the cluster
task test-infra:install-tools     # Install required tools
task test-infra:status            # Check infrastructure status

# Convenience wrapper commands (automatically set environment variable)
task test-infra-cluster           # Start cluster (wrapper)
task test-infra-cluster-down      # Stop cluster (wrapper)
task test-infra-install-tools     # Install tools (wrapper)
task test-infra-status            # Status check (wrapper)
task test-infra-clean             # Clean up resources (wrapper)
```

**Note**: Remote taskfiles are an experimental feature. The environment variable `TASK_X_REMOTE_TASKFILES=1` is automatically set in the taskfile configuration.

### Authentication
The test-infra deployment includes pre-configured authentication:
- **Kubeconfig**: `.milo/kubeconfig` (standard location, committed to repo)
- **Admin Token**: `test-admin-token` (system:masters group)
- **User Token**: `test-user-token` (system:authenticated group)
- **API Endpoint**: `http://localhost:8080` (via Envoy Gateway)

## Code Structure

### Controllers
- Located in `internal/controllers/`
- Follow standard controller-runtime patterns
- Support both single-cluster and multi-cluster operations
- Use finalizers for proper cleanup

### API Types
- Located in `pkg/apis/`
- Follow Kubernetes API conventions
- Use kubebuilder markers for code generation
- Include validation and printer columns

### Multi-cluster Support
- `pkg/multicluster-runtime/` contains cross-cluster abstractions
- Controllers can manage resources across multiple clusters
- Infrastructure cluster client separate from control plane client

### Webhooks
- Located in `internal/webhooks/`
- Provide validation and mutation for custom resources
- Generated manifests in `config/webhook/`

## Important Patterns

### Organization Namespacing
Organizations automatically get associated namespaces named
`organization-{name}`. The organization controller sets owner references to
manage namespace lifecycle.

### Project Control Planes
Projects can provision dedicated control planes in infrastructure clusters
through ProjectControlPlane resources. This enables true multi-tenancy and
resource isolation.

### Provider Integration
The system uses a provider pattern for integrating with external services like
authentication (Zitadel), authorization (OpenFGA), and future integrations for
billing, support, etc.

### Cross-cluster Resource Management
Controllers can watch and manage resources across multiple Kubernetes clusters
using the multicluster-runtime framework.

## Configuration Management

- Kustomize overlays in `config/` directory
- Base configurations in `config/*/base/`
- Environment-specific overlays in `config/*/overlays/`
- Sample resources in `config/samples/`
- Protected resources (system defaults) in `config/protected-resources/`

## Task Automation

**IMPORTANT: This project uses Taskfile.yaml for ALL automation and development commands. Always use `task` commands instead of running tools directly.**

### Core Development Tasks
```bash
# See all available tasks
task --list

# Code generation (REQUIRED after API changes)
task generate              # Generate deepcopy, CRDs, webhooks, RBAC
task generate:docs         # Generate API documentation with crdoc

# Development environment
task dev:setup             # Complete test infrastructure setup
task dev:deploy            # Deploy Milo to existing cluster
task dev:redeploy          # Quick rebuild and redeploy during development

# Build and container management
task dev:build             # Build Milo container image
task dev:load              # Load image into kind cluster

# Testing
task test:unit             # Run Go unit tests
task test:end-to-end       # Run Chainsaw e2e tests against Milo API

# Milo API server access (use instead of direct kubectl)
task kubectl -- get organizations    # Access Milo API server
task kubectl -- get projects         # Using .milo/kubeconfig automatically
task kubectl -- apply -f resource.yaml
```

### Test Infrastructure Management
```bash
# Test infrastructure (remote taskfile integration)
task test-infra-cluster          # Start test cluster
task test-infra-cluster-down     # Stop and cleanup cluster
task test-infra-status           # Check infrastructure status
task test-infra-install-tools    # Install required development tools

# Direct test-infra access (requires TASK_X_REMOTE_TASKFILES=1)
task test-infra:kubectl -- get pods -A    # Access infrastructure cluster
```

**Rule: NEVER run tools like `controller-gen`, `chainsaw`, `kubectl` directly. Always use the appropriate `task` command.**

## Claude Agent Specializations

This repository includes specialized Claude agent configurations in `.claude/agents/` to provide domain-specific expertise:

### Development Agents
- **kubernetes-controller-specialist**: Controller development, reconciliation logic, RBAC, multi-cluster operations
- **kubernetes-api-designer**: CRD creation, API validation, webhook implementation, schema design
- **milo-devops-automation**: Deployment workflows, Taskfile management, infrastructure automation

### Quality & Security Agents  
- **chainsaw-test-specialist**: End-to-end testing with Chainsaw framework, test scenario design
- **milo-security-auditor**: Security analysis, RBAC auditing, vulnerability assessment
- **milo-architecture-documenter**: API documentation generation, architectural decision records

### Agent Activation
Agents activate automatically based on:
- **File paths**: `internal/controllers/` → controller-specialist, `pkg/apis/` → api-designer
- **Keywords**: "reconcile", "CRD", "deploy", "test", "security", "docs"
- **Tasks**: Controller development, API changes, deployment issues, testing, security reviews

See `.claude/agents/README.md` for complete agent documentation and collaboration patterns.

## Orchestration and Subagent Usage

**IMPORTANT**: Always use specialized subagents (via the Task tool) for complex
work to manage context effectively and provide focused expertise.

### When to Use Subagents

- **Investigation tasks**: Use `datum-platform:sre` for cluster debugging,
  `Explore` for codebase exploration
- **Implementation tasks**: Use `datum-platform:api-dev` for Go backend work,
  `datum-platform:frontend-dev` for UI changes
- **Review tasks**: Use `datum-platform:code-reviewer` after implementation
- **Testing tasks**: Use `datum-platform:test-engineer` for writing tests

### Orchestration Pattern

1. **Analyze the request** and determine which specialized agent(s) to invoke
2. **Launch subagents** with clear, focused prompts describing the specific task
3. **Monitor progress** and provide high-level status updates to the user
4. **Synthesize results** from subagents into concise summaries
5. **Ask for feedback** when subagents surface multiple approaches or need
   clarification

### Subagent Communication

Subagents should report back with:

- **Findings**: What was discovered or accomplished
- **Status**: Success, failure, or blocked (with reason)
- **Recommendations**: Suggested next steps or decisions needed
- **Artifacts**: File paths, code changes, or outputs produced

The orchestrating agent then:

- Provides high-level summaries to the user
- Synthesizes information across multiple subagents
- Escalates decisions that require user input
- Coordinates handoffs between specialized agents

### Benefits

- **Context management**: Each subagent focuses on its domain without consuming
  main conversation context
- **Expertise**: Specialized agents have domain-specific knowledge and patterns
- **Parallelism**: Multiple subagents can work simultaneously on independent
  tasks
- **Quality**: Dedicated review and testing agents ensure implementation
  quality

# User Rules

## Kubernetes API Conventions - use when writing APIs and operators with kubebuilder / controller-runtime

### Kubernetes API conventions — distilled cheatsheet

Use this as background context when writing or reviewing Kubernetes‑style APIs,
CRDs, or controllers.  (Each bullet is a rule‑of‑thumb; details and corner‑cases
exist in the full SIG‑Architecture document.)

---

#### 1. Object hierarchy & vocabulary

* **Resource** = REST endpoint (`/pods`, `/deployments`).
* **Kind / Type** = schema name inside `apiVersion` & `kind` fields (e.g.
  `Pod`). Grouped into: Objects (persistent), Lists, and Aux/Simple types.
  ([GitHub][1])
* **Sub‑resources** (e.g. `/status`, `/scale`, `/binding`) expose limited views
  or verbs on the parent resource.

---

#### 2. Standard top‑level fields

```yaml
apiVersion: group/majorVersion
kind: Kind
metadata: …   # identity & bookkeeping
spec: …       # desired state (set by users/controllers)
status: …     # observed state (set by controllers only)
```

Spec ↔ Status separation is mandatory; controllers must not mutate `spec` and
users must not write `status`. ([Kubernetes][2], [Kubernetes][3])

---

#### 3. `metadata` essentials

* **name**: DNS‑label (≤ 253 chars); immutable once created.
* **namespace**: optional; cluster‑scoped kinds omit it.
* **uid**: system‑assigned UUID; never reused.
* **resourceVersion**: for optimistic concurrency & watch bookmarks.
  ([GitHub][4])
* **generation / observedGeneration**: bump on `spec` changes; controllers copy
  to `status.observedGeneration`.
* **labels** = indexed, 63‑char segments; **annotations** = unindexed, arbitrary
  size.
* **ownerReferences** + **finalizers** for garbage‑collection & ordered
  deletion.

---

#### 4. Field design rules

* **Names**: `camelCase` for JSON/YAML, GoStruct `CamelCase`.
* **Primitives**: use `int32` for counts/ports, `int64` for resources;
  quantities go through `resource.Quantity`.
* **Booleans**: use `*bool` when “unset” differs from “false”.
* **Lists**: stateful sets should be strictly ordered; otherwise treat as sets
  (unique items).
* **Maps**: keys are strings (DNS‑label or label‑key form).
* **Enums**: `PascalCase` strings; avoid magic integers.

---

#### 5. Defaulting & validation

* Provide API‑server defaults via admission.
* Mark required fields with `+kubebuilder:validation:Required` (or similar).
* Never change the meaning of an existing default.

---

#### 6. API versioning & compatibility

* Minor/patch changes must be **backward‑compatible** within a given `v1` group.
* **No field deletions or rename breaks** once GA.
* Behaviors may be extended only in opt‑in way (feature gates or new fields).
* Deprecate in `vNbetaX` first, remove no sooner than **2 stable releases**
  later.

---

#### 7. Concurrency & consistency patterns

* Use `resourceVersion` preconditions (`metadata.resourceVersion` in PUT/PATCH)
  for CAS updates. ([pulumi][5])
* Controllers reconcile desired vs. observed; must tolerate stale reads
  (level‑based logic).

---

#### 8. Sub‑resource conventions

* `/status` : PATCH/PUT only status fields.
* `/scale` : `spec.replicas`, `status.replicas`.
* `/finalize`, `/eviction`, `/binding`, `/proxy`, etc. follow reserved
  semantics.

---

#### 9. Error & status objects

`Status{ status:"Failure", reason:"Invalid", message:"...", code:400 }` is the
canonical error envelope.

---

#### 10. Label/selector patterns

* Labels map to field selectors & set‑based selectors (`matchLabels`,
  `matchExpressions`).
* Never assume global uniqueness—always pair `namespace` + `name`.

---

**Remember:** this is a terse reference; the full SIG‑Architecture *API
Conventions* doc covers edge‑cases, historical context, and nuanced exceptions.
([iximiuz.com][6])

[1]:
    https://github.com/zecke/Kubernetes/blob/master/docs/devel/api-conventions.md?utm_source=chatgpt.com
    "Kubernetes/docs/devel/api-conventions.md at master - GitHub"
[2]:
    https://kubernetes.io/docs/concepts/overview/working-with-objects/?utm_source=chatgpt.com
    "Objects In Kubernetes"
[3]:
    https://kubernetes.io/docs/concepts/extend-kubernetes/api-extension/custom-resources/?utm_source=chatgpt.com
    "Custom Resources - Kubernetes"
[4]:
    https://github.com/kubernetes-client/ruby/blob/master/kubernetes/docs/V1ListMeta.md?utm_source=chatgpt.com
    "V1ListMeta.md - kubernetes-client/ruby - GitHub"
[5]:
    https://www.pulumi.com/registry/packages/kubernetes/api-docs/events/v1beta1/event/?utm_source=chatgpt.com
    "kubernetes.events.k8s.io.v1beta1.Event | Pulumi Registry"
[6]:
    https://iximiuz.com/en/posts/kubernetes-api-structure-and-terminology/?utm_source=chatgpt.com
    "Kubernetes API Basics - Resources, Kinds, and Objects"


### Technical Writing Guidance

### Core Principles

Technical writing prioritizes **clarity above all other considerations**. Every
choice should serve the goal of making information clearer, more accessible, and
more actionable for your target audience.

---

### 1. Words and Terminology

#### Use Terms Consistently

- **MUST**: Define abbreviations and acronyms on first use: "Application
  Programming Interface (API)"
- **MUST**: Use the same term throughout a document (don't vary between
  "directory" and "folder")
- **SHOULD**: Only define acronyms that are significantly shorter and appear
  multiple times
- **SHOULD NOT**: Define acronyms used only once or twice

#### Choose Strong, Specific Verbs

- **PREFER**: Precise, strong verbs over weak, generic ones
- **AVOID**: Overusing forms of "be" (is, are, was, were)
- **AVOID**: Generic verbs like "occur" and "happen"

**Examples:**

- ❌ "The exception occurs when dividing by zero"
- ✅ "Dividing by zero raises the exception"
- ❌ "We are very careful to ensure..."
- ✅ "We carefully ensure..."

#### Avoid Ambiguous Pronouns

- **MUST**: Place pronouns close to their referring nouns (within 5 words)
- **MUST**: Make clear what "it," "they," "this," and "that" refer to
- **SHOULD**: Replace ambiguous pronouns with specific nouns
- **SHOULD**: Add clarifying nouns after "this" and "that"

**Examples:**

- ❌ "Running the process configures permissions and generates a user ID. This
  lets users authenticate."
- ✅ "Running the process configures permissions and generates a user ID. This
  user ID lets users authenticate."

---

### 2. Voice and Sentence Structure

#### Use Active Voice

- **PREFER**: Active voice over passive voice
- **FORMULA**: Active = actor + verb + target
- **RECOGNIZE**: Passive = form of "be" + past participle verb

**Examples:**

- ❌ "The code is compiled by the system"
- ✅ "The system compiles the code"
- ❌ "Mistakes were made"
- ✅ "The team made mistakes"

#### Write Clear, Focused Sentences

- **MUST**: Focus each sentence on a single idea
- **SHOULD**: Convert long sentences to lists when appropriate
- **SHOULD**: Eliminate unnecessary words

#### Reduce "There is/There are" Constructions

- **AVOID**: Starting sentences with "There is" or "There are"
- **REPLACE**: With actual subjects and verbs

**Examples:**

- ❌ "There is a variable called `met_trick` that stores the current accuracy"
- ✅ "The `met_trick` variable stores the current accuracy"

---

### 3. Lists and Structure

#### Use Appropriate List Types

- **USE**: Numbered lists when order matters (procedures, rankings)
- **USE**: Bulleted lists when order doesn't matter
- **MUST**: Keep list items parallel in structure
- **SHOULD**: Start numbered list items with imperative verbs

#### Format Lists Properly

- **MUST**: Introduce lists with appropriate lead-in text
- **SHOULD**: Use parallel structure across all list items
- **CONSIDER**: Breaking long paragraphs into lists for scannability

---

### 4. Paragraphs and Document Structure

#### Focus Paragraphs

- **MUST**: Focus each paragraph on a single topic
- **MUST**: Start paragraphs with strong opening sentences
- **SHOULD**: State the paragraph's main point early

#### Organize Documents

- **MUST**: State key points at the start of the document
- **MUST**: Define your document's scope and audience upfront
- **SHOULD**: Break long topics into appropriate sections
- **SHOULD**: Use clear, descriptive headings

---

### 5. Audience and Accessibility

#### Know Your Audience

- **MUST**: Identify your target audience explicitly
- **MUST**: Determine what your audience already knows
- **MUST**: Determine what your audience needs to learn
- **AVOID**: The "curse of knowledge" (assuming readers know what you know)

#### Write for Accessibility

- **MUST**: Provide alt text for all images
- **MUST**: Ensure sufficient color contrast
- **MUST**: Use descriptive link text (not "click here")
- **AVOID**: Relying solely on visual indicators
- **USE**: Inclusive language that doesn't exclude groups

#### Alt Text Guidelines

- **MUST**: Describe the purpose and context of images
- **SHOULD**: Be concise but informative
- **FOCUS**: On what readers need to understand from the image

---

### 6. Visual Elements and Formatting

#### Illustrations and Diagrams

- **WRITE**: Captions before creating illustrations
- **LIMIT**: Information in single diagrams (avoid visual clutter)
- **USE**: Callouts to focus attention on key elements
- **ITERATE**: Revise illustrations for clarity

#### Code and Technical Elements

- **FORMAT**: Code-related text in code font
- **PLACE**: Code examples close to explanatory text
- **ENSURE**: Code examples are:
  - Useful and accurate
  - Concise but complete
  - Well-commented
  - Demonstrating appropriate complexity range

---

### 7. Error Messages and Instructions

#### Error Message Principles

Great error messages answer two questions:

1. **What went wrong?**
2. **How does the user fix it?**

#### Error Message Requirements

- **IDENTIFY**: The specific cause of the error
- **SPECIFY**: Invalid inputs when applicable
- **EXPLAIN**: Requirements and constraints
- **PROVIDE**: Clear solution steps
- **INCLUDE**: Examples when helpful

#### Error Message Writing

- **BE**: Concise but not cryptic
- **AVOID**: Double negatives
- **USE**: Consistent terminology
- **SET**: Positive, helpful tone
- **DON'T**: Be overly apologetic

---

### 8. Style and Tone

#### General Style

- **WRITE**: In second person ("you") for instructions
- **PLACE**: Conditions before instructions
- **USE**: Present tense when possible
- **AVOID**: Idioms and colloquialisms

#### Punctuation Guidelines

- **USE**: Serial/Oxford commas in lists
- **USE**: Parentheses for brief clarifications
- **USE**: Em dashes for longer explanations
- **USE**: Colons to introduce lists or explanations

#### Formatting Standards

- **FORMAT**: UI elements consistently
- **USE**: Bold for emphasis (sparingly)
- **USE**: Code formatting for technical terms
- **MAINTAIN**: Consistent heading structure

---

### 9. Editing and Review

#### Self-Editing Process

- **ADOPT**: A consistent style guide
- **READ**: Drafts aloud to check flow
- **STEP**: Away from writing and return with fresh eyes
- **CHANGE**: Context (print, different font) for review
- **SEEK**: Peer editor feedback

#### Review Checklist

- [ ] Is the purpose clear?
- [ ] Is the audience appropriate?
- [ ] Are terms used consistently?
- [ ] Is active voice used where appropriate?
- [ ] Are sentences focused and clear?
- [ ] Are lists properly formatted?
- [ ] Is the content accessible?
- [ ] Are error messages helpful and actionable?

---

### 10. Documentation Types and Approach

#### Different Documentation Needs

- **TUTORIALS**: Step-by-step guidance for beginners
- **REFERENCES**: Comprehensive technical details
- **GUIDES**: Task-oriented instructions
- **OVERVIEWS**: High-level conceptual information

#### Writing Process

1. **IDENTIFY**: Your audience and their needs
2. **DEFINE**: Your document's scope and goals
3. **STRUCTURE**: Information logically
4. **WRITE**: First draft focusing on content
5. **EDIT**: For clarity, accuracy, and style
6. **REVIEW**: With target audience perspective
7. **ITERATE**: Based on feedback

---

### Application in Code Documentation

When writing code documentation, API references, or technical specifications:

- **DOCUMENT**: Functions with clear purpose, parameters, and return values
- **PROVIDE**: Usage examples for complex APIs
- **EXPLAIN**: Edge cases and error conditions
- **MAINTAIN**: Consistency with codebase conventions
- **UPDATE**: Documentation with code changes

---

*This guidance prioritizes clarity, accessibility, and user focus in all
technical communication. Apply these principles consistently across
documentation, comments, error messages, and user-facing content.*

# Build stage
# Use $BUILDPLATFORM so the builder runs natively on the runner's architecture
# and cross-compiles to $TARGETOS/$TARGETARCH. This makes multi-arch builds
# (linux/amd64, linux/arm64) fast under buildx without requiring QEMU emulation
# for the build itself.
FROM --platform=$BUILDPLATFORM golang:1.25 AS builder

# Provided automatically by BuildKit when using buildx with --platform.
ARG TARGETOS
ARG TARGETARCH

WORKDIR /app

# Copy go.mod and go.sum files first for better layer caching
COPY go.mod go.sum ./

# Download dependencies (cached when go.mod/go.sum don't change)
RUN go mod download

# Copy the rest of the application source code
COPY . .

# Build the application with optimizations and version information
# -ldflags="-w -s" strips debug info, reducing binary size by ~30%
# -trimpath removes file system paths from the binary for reproducible builds
#
# Version information is injected via ldflags into both
# k8s.io/component-base/version (what the API server serves on /version) and
# go.miloapis.com/milo/pkg/version (the Milo release, shown by `milo version`).
#
# The value served as gitVersion MUST parse as a semantic version: kubectl and
# datumctl run ParseSemantic on it, and the API server itself derives its
# feature-gate binary version from it. Milo's own release tags carry major
# version 0 (v0.32.5), which would make the derived emulation version fail
# ComponentGlobalsRegistry validation, so gitVersion reports the Kubernetes API
# level this build implements and carries the Milo release in the pre-release
# segment:
#
#   v1.<k8s-minor>.<k8s-patch>-milo.<milo-version>+<git-commit>
#
# The Kubernetes level is derived from the k8s.io/component-base requirement in
# go.mod so it cannot drift from the vendored libraries.
ARG VERSION=v0.0.0-master+dev
ARG GIT_COMMIT=unknown
ARG GIT_TREE_STATE=dirty
ARG BUILD_DATE=unknown
RUN set -eu; \
    KUBE_MOD="$(awk '$1 == "k8s.io/component-base" { print $2; exit }' go.mod)"; \
    KUBE_MOD="${KUBE_MOD#v}"; \
    KUBE_MINOR="$(printf '%s' "${KUBE_MOD}" | cut -d. -f2)"; \
    KUBE_PATCH="$(printf '%s' "${KUBE_MOD}" | cut -d. -f3 | cut -d- -f1)"; \
    MILO_ID="${VERSION#v}"; \
    MILO_ID="${MILO_ID%%+*}"; \
    MILO_ID="$(printf '%s' "${MILO_ID}" | tr -c '0-9A-Za-z.-' '-' | sed -e 's/\.\{2,\}/./g' -e 's/^[.-]*//' -e 's/[.-]*$//')"; \
    [ -n "${MILO_ID}" ] || MILO_ID="dev"; \
    GIT_VERSION="v1.${KUBE_MINOR}.${KUBE_PATCH}-milo.${MILO_ID}+${GIT_COMMIT}"; \
    echo "Building milo ${VERSION} (serving gitVersion ${GIT_VERSION})"; \
    CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-amd64} \
    go build -trimpath -o milo \
      -ldflags="-w -s \
        -X k8s.io/component-base/version.gitVersion=${GIT_VERSION} \
        -X k8s.io/component-base/version.gitMajor=1 \
        -X k8s.io/component-base/version.gitMinor=${KUBE_MINOR} \
        -X k8s.io/component-base/version.gitCommit=${GIT_COMMIT} \
        -X k8s.io/component-base/version.gitTreeState=${GIT_TREE_STATE} \
        -X k8s.io/component-base/version.buildDate=${BUILD_DATE} \
        -X go.miloapis.com/milo/pkg/version.version=${VERSION} \
        -X go.miloapis.com/milo/pkg/version.gitCommit=${GIT_COMMIT} \
        -X go.miloapis.com/milo/pkg/version.gitTreeState=${GIT_TREE_STATE} \
        -X go.miloapis.com/milo/pkg/version.buildDate=${BUILD_DATE}" \
      ./cmd/milo

# Final stage: minimal runtime image
FROM gcr.io/distroless/static

# Copy the binary from builder
WORKDIR /
COPY --from=builder /app/milo .

# Run as nobody user (65534) for better security
# Note: We'll use CAP_NET_BIND_SERVICE capability to allow binding to port 6443
USER 65534

ENTRYPOINT ["/milo"]

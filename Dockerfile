# Build stage
# Use $BUILDPLATFORM so the builder runs natively on the runner's architecture
# and cross-compiles to $TARGETOS/$TARGETARCH. This makes multi-arch builds
# (linux/amd64, linux/arm64) fast under buildx without requiring QEMU emulation
# for the build itself.
FROM --platform=$BUILDPLATFORM golang:1.26 AS builder

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
# Version information is injected via ldflags into k8s.io/component-base/version
ARG VERSION=v0.0.0-master+dev
ARG GIT_COMMIT=unknown
ARG GIT_TREE_STATE=dirty
ARG BUILD_DATE=unknown
RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-amd64} \
    go build -trimpath -o milo ./cmd/milo

# Final stage: minimal runtime image
FROM gcr.io/distroless/static

# Copy the binary from builder
WORKDIR /
COPY --from=builder /app/milo .

# Run as nobody user (65534) for better security
# Note: We'll use CAP_NET_BIND_SERVICE capability to allow binding to port 6443
USER 65534

ENTRYPOINT ["/milo"]

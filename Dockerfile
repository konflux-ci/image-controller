# Build the manager binary
# For more details and updates, refer to
# https://catalog.redhat.com/software/containers/ubi9/go-toolset/61e5c00b4ec9945c18787690
FROM registry.access.redhat.com/ubi9/go-toolset:1.23.9 AS builder
ARG TARGETOS
ARG TARGETARCH

USER 1001

WORKDIR /workspace
# Copy the Go Modules manifests
COPY --chown=1001:0 go.mod go.mod
COPY --chown=1001:0 go.sum go.sum
# cache deps before building and copying source so that we don't need to re-download as much
# and so that source changes don't invalidate our downloaded layer
RUN go mod download

# Copy the go source
COPY --chown=1001:0 . .

# Build
# the GOARCH has not a default value to allow the binary be built according to the host where the command
# was called. For example, if we call make docker-build in a local env which has the Apple Silicon M1 SO
# the docker BUILDPLATFORM arg will be linux/arm64 when for Apple x86 it will be linux/amd64. Therefore,
# by leaving it empty we can ensure that the container and binary shipped on it will have the same platform.
RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH} go build -a -o manager cmd/main.go

# Use ubi-minimal as minimal base image to package the manager binary
# For more details and updates, refer to
# https://catalog.redhat.com/software/containers/ubi8/ubi-minimal/5c359a62bed8bd75a2c3fba8
FROM registry.access.redhat.com/ubi8/ubi-minimal:8.10-1295.1749680713
WORKDIR /
COPY --from=builder /workspace/manager .
USER 65532:65532

LABEL name="image-controller"
LABEL description="Konflux Image Controller operator"
LABEL summary="Konflux Image Service"
LABEL io.k8s.description="Konflux Image Controller operator"
LABEL io.k8s.display-name="image-controller-operator"
LABEL io.openshift.tags="konflux"
LABEL com.redhat.component="image-controller-operator"

ENTRYPOINT ["/manager"]

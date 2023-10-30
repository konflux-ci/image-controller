# Build the manager binary
# For more details and updates, refer to
# https://catalog.redhat.com/software/containers/ubi9/go-toolset/61e5c00b4ec9945c18787690
FROM registry.access.redhat.com/ubi9/go-toolset:1.19.13-4.1697647145 as builder

# Copy the Go Modules manifests
COPY go.mod go.mod
COPY go.sum go.sum
# cache deps before building and copying source so that we don't need to re-download as much
# and so that source changes don't invalidate our downloaded layer
RUN go mod download

# Copy the go source
COPY main.go main.go
COPY api api
COPY pkg pkg
COPY controllers/ controllers/

# Build
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -a -o manager main.go

# Use ubi-minimal as minimal base image to package the manager binary
# For more details and updates, refer to
# https://catalog.redhat.com/software/containers/ubi9/ubi-minimal/615bd9b4075b022acc111bf5
FROM registry.access.redhat.com/ubi9/ubi-minimal:9.2-750.1697625013
WORKDIR /
COPY --from=builder /opt/app-root/src/manager /
USER 65532:65532

LABEL description="RHTAP Image Controller operator"
LABEL io.k8s.description="RHTAP Image Controller operator"
LABEL io.k8s.display-name="image-controller-operator"
LABEL io.openshift.tags="rhtap"
LABEL summary="RHTAP Image Service"

ENTRYPOINT ["/manager"]

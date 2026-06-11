This file provides guidance on the Konflux Image Controller operator.
The operator is a part of Konflux project and is responsible for management of OCI image repositories based on `ImageRepository` objects in cluster.
Currently it supports only Quay compatible OCI registries: quay.io and self-hosted instances.

## High-Level Project Structure

- `cmd/main.go` operator entrypoint, contains startup logic and global settings.
- `internal/controller` controllers, main reconcile logic.
- `pkg/quay` quay API client implementation.
- `pkg/metrics` metrics and availability probes.
- `api` owned CRDs definitions.
- `*_unit_test.go` unit tests.
- `internal/controller/suite_util_test.go` common utilities used in integration tests (envtest).
- `e2e-tests` e2e tests, separate go project, require a cluster with installed Konflux to run.
   Agents shouldn't run those.

## Controllers

### Imagerepository controller

The main controller of the operator.
Reconciles `ImageRepository` objects and manages image repository and its lifecycle:
- Creation and removal of actual OCI image repository in the remote registry.
- The image repository settings (public / private, etc.)
- Management of push / pull k8s secrets (per repository and namespace wide), including credentials rotation on demand.
- If marked for Component via `appstudio.redhat.com/component` label and `image-controller.appstudio.redhat.com/update-component-image` annotation, updates `Component` resource by setting its `spec.containerImage` based on the provisioned image repository.
- Notifications management (Quay specific).

### Component image controller (deprecated)

Watches `Component` resources and checks for `image.redhat.com/generate` annotation.
If it is present, creates `ImageRepository` resource for the Component.
It's done for backward compatibility as currently recommended way is to define `ImageRepository` resource in GitOps or Konflux UI would do it automatically.

### Application controller (deprecated)

Manages k8s secret per Konflux Application that has pull access to all image repositories within the Konflux Application.
The per Application approach pull secret is replaced with per namespace pull secret named `components-namespace-pull`.

## Owned CRDs

The operator owns `ImageRepository` CRD which is defined in `api/vialpha1/imagerepository_types.go`.

Typical `ImageRepository` in k8s or Openshift cluster:
```yaml
apiVersion: appstudio.redhat.com/v1alpha1
kind: ImageRepository
metadata:
  labels:
    appstudio.redhat.com/application: image-controller
    appstudio.redhat.com/component: image-controller
spec:
  image:
    name: image-controller-tenant/image-controller
    visibility: public
status:
  credentials:
    generationTimestamp: '2026-02-13T19:30:22Z'
    pull-robot-account: image_controller_tenant_image_controller_ddffda7318_pull
    pull-secret: imagerepository-for-image-controller-image-pull
    push-robot-account: image_controller_tenant_image_controller_56f4bae305
    push-secret: imagerepository-for-image-controller-image-push
  image:
    url: quay.io/quay-organization/image-controller-tenant/image-controller
    visibility: public
  state: ready
```
Note, `spec.name` should always have tenant (user namespace in cluster) prefix.
That is done to prevent accessing different tenants from current one.

## Development

After making code changes, always make sure that:
- unit and integration tests (envtest) pass (run with `make test` after all changes made).
- all linters pass (run with `make lint`).

If linters are not installed, install them:
- `make golangci-lint` to install golangci-lint.
- `make envtest` to install envtest binaries needed for integration tests.

To verify single file, run:
```sh
./bin/golangci-lint run path/to/file.go
```

After changing any reconciler permissions, update the operator [role](config/rbac/role.yaml) by running `make manifests`.

## Usage

Check [README.md](README.md) for installation and usage details.

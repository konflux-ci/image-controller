# E2E Tests

This directory contains the end-to-end (E2E) test suite and CI infrastructure for `image-controller`.

## Directory Structure

```
e2e-tests/
├── pipelines/      Tekton Pipeline definitions for CI
├── scripts/        Shell scripts executed by Tekton Tasks
├── tasks/          Tekton Task definitions
├── tests/          Go test files (Ginkgo test suite)
├── go.mod          Go module for the test suite
└── go.sum
```

### `pipelines/`

A single parameterized Tekton Pipeline (`konflux-e2e-tests.yaml`) that provisions a Kind cluster on AWS, deploys Konflux, runs image-controller E2E tests, collects artifacts, and deprovisions the cluster.

### `tasks/`

Tekton Task definitions used by the pipelines. The `image-controller-e2e` task clones this repository, sets up the test environment, and runs the Ginkgo test suite.

### `scripts/`

Shell scripts invoked by the Tekton Tasks to configure the environment, load secrets, and execute the test runner.

### `tests/`

Ginkgo-based E2E tests (31 specs, GitHub-only) that validate image-controller functionality against a running Konflux cluster.

Tests cover:
- **Private visibility component**: ImageRepository creation, Quay repo and robot account provisioning, private visibility enforcement, cleanup on component deletion
- **Public visibility component**: ImageRepository creation, public visibility, image tag updates, pruning labels (`quay.expires-after`)
- **Visibility changes**: Switching from public to private without retriggering pipelines
- **Component removal**: Image repo and robot account cleanup

Tests consume shared utilities from [`github.com/konflux-ci/e2e-tests`](https://github.com/konflux-ci/e2e-tests).

## Prerequisites

To run the E2E tests you need:

1. **A running Konflux cluster** — either a shared staging environment or a local Kind cluster with Konflux deployed. See [konflux-ci/konflux-ci](https://github.com/konflux-ci/konflux-ci) for setup instructions.
2. **`KUBECONFIG`** pointing at the target cluster.
3. **Quay.io organization** that supports private repository creation (the `DEFAULT_QUAY_ORG` env var, defaults to `redhat-appstudio-qe`).
4. **GitHub token** with repo access exported as `GITHUB_TOKEN`, and a GitHub App configured for Pipelines-as-Code:
   - `E2E_PAC_GITHUB_APP_ID`
   - `E2E_PAC_GITHUB_APP_PRIVATE_KEY`
   - `PAC_GITHUB_APP_WEBHOOK_SECRET`
   - `SMEE_CHANNEL`
5. **Quay credentials**:
   - `DEFAULT_QUAY_ORG_TOKEN`
   - `QUAY_TOKEN`
   - `QUAY_OAUTH_USER` / `QUAY_OAUTH_TOKEN`

## Running Tests Locally

From the repository root:

```bash
make test/e2e
```

Or directly:

```bash
cd e2e-tests
go run github.com/onsi/ginkgo/v2/ginkgo@latest -v --label-filter="image-controller" ./tests/
```

## Debugging

- **Dry-run** to list all specs without executing:
  ```bash
  cd e2e-tests
  go run github.com/onsi/ginkgo/v2/ginkgo@latest --dry-run --label-filter="image-controller" -v ./tests/
  ```
- **Run a single test** by name:
  ```bash
  go run github.com/onsi/ginkgo/v2/ginkgo@latest -v --label-filter="image-controller" --focus="triggers a PipelineRun" ./tests/
  ```
- **Artifacts** are written to the `ARTIFACT_DIR` directory (defaults to `/workspace/artifact-dir` in CI). Includes `e2e-tests.log` and `e2e-report.xml` (JUnit).

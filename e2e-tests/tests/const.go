package e2e

import (
	"github.com/konflux-ci/e2e-tests/pkg/constants"
	"github.com/konflux-ci/e2e-tests/pkg/utils"
)

const (
	helloWorldComponentGitSourceRepoName = "devfile-sample-hello-world"
	helloWorldComponentDefaultBranch     = "default"
	helloWorldComponentRevision          = "d2d03e69de912e3827c29b4c5b71ffe8bcb5dad8"
	githubUrlFormat                      = "https://github.com/%s/%s"
	buildStatusAnnotationValueLoggingFormat = "build status annotation value: %s\n"
)

var (
	githubOrg = utils.GetEnv(constants.GITHUB_E2E_ORGANIZATION_ENV, "redhat-appstudio-qe")
)

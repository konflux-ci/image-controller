package e2e

import (
	"fmt"

	"github.com/devfile/library/v2/pkg/util"
	. "github.com/onsi/gomega" //nolint:staticcheck

	"github.com/konflux-ci/e2e-tests/pkg/clients/git"
	"github.com/konflux-ci/e2e-tests/pkg/framework"
)

func setupGitProvider(f *framework.Framework) (git.Client, string, string) {
	gitClient := git.NewGitHubClient(f.AsKubeAdmin.CommonController.Github)
	repoName := helloWorldComponentGitSourceRepoName + "-" + util.GenerateRandomString(6)
	err := gitClient.ForkRepository(helloWorldComponentGitSourceRepoName, repoName)
	Expect(err).ShouldNot(HaveOccurred())
	repoURL := fmt.Sprintf(githubUrlFormat, githubOrg, repoName)
	return gitClient, repoURL, repoName
}

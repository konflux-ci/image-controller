/*
Copyright 2024 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

	http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/
package controllers

import (
	"fmt"

	"github.com/konflux-ci/image-controller/pkg/quay"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/types"
)

var _ = Describe("Users config map controller", func() {
	Context("Users config map creation, update, removal, namespace doesn't start with number", func() {
		var (
			configTestNamespace = "config-map-namespace-test"
			usersConfigMapKey   = types.NamespacedName{Name: additionalUsersConfigMapName, Namespace: configTestNamespace}
			pacRouteKey         = types.NamespacedName{Name: pipelinesAsCodeRouteName, Namespace: pipelinesAsCodeNamespace}
		)
		expectedTeamName := "configxmapxnamespacextestxteam"
		imageRepositoryName1 := fmt.Sprintf("%s/some1/image1", configTestNamespace)
		imageRepositoryName2 := fmt.Sprintf("%s/other2/image2", configTestNamespace)

		BeforeEach(func() {
			quay.ResetTestQuayClientToFails()
		})

		It("should prepare environment", func() {
			createNamespace(configTestNamespace)
			createNamespace(pipelinesAsCodeNamespace)
			createRoute(pacRouteKey, "pac.host.domain.com")
		})

		It("team doesn't exist, requested 2 users, imageRepositories don't exist, create team with and add 2 users", func() {
			isEnsureTeamInvoked := false
			isListRepositoryPermissionsForTeamInvoked := false
			countAddUserToTeamInvoked := 0
			isDeleteTeamInvoked := false

			quay.EnsureTeamFunc = func(organization, teamName string) ([]quay.Member, error) {
				defer GinkgoRecover()
				Expect(organization).To(Equal(quay.TestQuayOrg))
				Expect(teamName).To(Equal(expectedTeamName))
				isEnsureTeamInvoked = true
				return []quay.Member{}, nil
			}
			quay.ListRepositoryPermissionsForTeamFunc = func(organization, teamName string) ([]quay.TeamPermission, error) {
				defer GinkgoRecover()
				Expect(organization).To(Equal(quay.TestQuayOrg))
				Expect(teamName).To(Equal(expectedTeamName))
				isListRepositoryPermissionsForTeamInvoked = true
				return []quay.TeamPermission{}, nil
			}
			quay.AddUserToTeamFunc = func(organization, teamName, userName string) (bool, error) {
				defer GinkgoRecover()
				Expect(organization).To(Equal(quay.TestQuayOrg))
				Expect(teamName).To(Equal(expectedTeamName))
				Expect(userName).To(BeElementOf([]string{"user1", "user2"}))
				countAddUserToTeamInvoked++
				return false, nil
			}

			createUsersConfigMap(usersConfigMapKey, []string{"user1", "user2"})
			waitQuayTeamUsersFinalizerOnConfigMap(usersConfigMapKey)

			Eventually(func() bool { return isEnsureTeamInvoked }, timeout, interval).Should(BeTrue())
			Eventually(func() bool { return isListRepositoryPermissionsForTeamInvoked }, timeout, interval).Should(BeTrue())
			Eventually(func() int { return countAddUserToTeamInvoked }, timeout, interval).Should(Equal(2))

			quay.DeleteTeamFunc = func(organization, teamName string) error {
				defer GinkgoRecover()
				Expect(organization).To(Equal(quay.TestQuayOrg))
				Expect(teamName).To(Equal(expectedTeamName))
				isDeleteTeamInvoked = true
				return nil
			}
			deleteUsersConfigMap(usersConfigMapKey)
			Eventually(func() bool { return isDeleteTeamInvoked }, timeout, interval).Should(BeTrue())
		})

		It("team exists and has already 1 of requested users, add 1 more user to the team, imageRepositories don't exist", func() {
			isEnsureTeamInvoked := false
			isListRepositoryPermissionsForTeamInvoked := false
			countAddUserToTeamInvoked := 0
			isDeleteTeamInvoked := false

			quay.EnsureTeamFunc = func(organization, teamName string) ([]quay.Member, error) {
				defer GinkgoRecover()
				Expect(organization).To(Equal(quay.TestQuayOrg))
				Expect(teamName).To(Equal(expectedTeamName))
				isEnsureTeamInvoked = true
				return []quay.Member{{Name: "user1", Kind: "user", IsRobot: false, Invited: false}}, nil
			}
			quay.ListRepositoryPermissionsForTeamFunc = func(organization, teamName string) ([]quay.TeamPermission, error) {
				defer GinkgoRecover()
				Expect(organization).To(Equal(quay.TestQuayOrg))
				Expect(teamName).To(Equal(expectedTeamName))
				isListRepositoryPermissionsForTeamInvoked = true
				return []quay.TeamPermission{}, nil
			}
			quay.AddUserToTeamFunc = func(organization, teamName, userName string) (bool, error) {
				defer GinkgoRecover()
				Expect(organization).To(Equal(quay.TestQuayOrg))
				Expect(teamName).To(Equal(expectedTeamName))
				Expect(userName).To(Equal("user2"))
				countAddUserToTeamInvoked++
				return false, nil
			}

			createUsersConfigMap(usersConfigMapKey, []string{"user1", "user2"})
			waitQuayTeamUsersFinalizerOnConfigMap(usersConfigMapKey)

			Eventually(func() bool { return isEnsureTeamInvoked }, timeout, interval).Should(BeTrue())
			Eventually(func() bool { return isListRepositoryPermissionsForTeamInvoked }, timeout, interval).Should(BeTrue())
			Eventually(func() int { return countAddUserToTeamInvoked }, timeout, interval).Should(Equal(1))

			quay.DeleteTeamFunc = func(organization, teamName string) error {
				defer GinkgoRecover()
				Expect(organization).To(Equal(quay.TestQuayOrg))
				Expect(teamName).To(Equal(expectedTeamName))
				isDeleteTeamInvoked = true
				return nil
			}
			deleteUsersConfigMap(usersConfigMapKey)
			Eventually(func() bool { return isDeleteTeamInvoked }, timeout, interval).Should(BeTrue())
		})

		It("team exists and has already 2 users which weren't requested, add 2 users to the team, remove 2 users from team, imageRepositories don't exist", func() {
			isEnsureTeamInvoked := false
			isListRepositoryPermissionsForTeamInvoked := false
			countAddUserToTeamInvoked := 0
			countRemoveUserFromTeamInvoked := 0

			quay.EnsureTeamFunc = func(organization, teamName string) ([]quay.Member, error) {
				defer GinkgoRecover()
				Expect(organization).To(Equal(quay.TestQuayOrg))
				Expect(teamName).To(Equal(expectedTeamName))
				isEnsureTeamInvoked = true
				return []quay.Member{
					{Name: "not-requested-user1", Kind: "user", IsRobot: false, Invited: false},
					{Name: "not-requested-user2", Kind: "user", IsRobot: false, Invited: false}}, nil
			}
			quay.ListRepositoryPermissionsForTeamFunc = func(organization, teamName string) ([]quay.TeamPermission, error) {
				defer GinkgoRecover()
				Expect(organization).To(Equal(quay.TestQuayOrg))
				Expect(teamName).To(Equal(expectedTeamName))
				isListRepositoryPermissionsForTeamInvoked = true
				return []quay.TeamPermission{}, nil
			}
			quay.AddUserToTeamFunc = func(organization, teamName, userName string) (bool, error) {
				defer GinkgoRecover()
				Expect(organization).To(Equal(quay.TestQuayOrg))
				Expect(teamName).To(Equal(expectedTeamName))
				Expect(userName).To(BeElementOf([]string{"user1", "user2"}))
				countAddUserToTeamInvoked++
				return false, nil
			}
			quay.RemoveUserFromTeamFunc = func(organization, teamName, userName string) error {
				defer GinkgoRecover()
				Expect(organization).To(Equal(quay.TestQuayOrg))
				Expect(teamName).To(Equal(expectedTeamName))
				Expect(userName).To(BeElementOf([]string{"not-requested-user1", "not-requested-user2"}))
				countRemoveUserFromTeamInvoked++
				return nil
			}

			createUsersConfigMap(usersConfigMapKey, []string{"user1", "user2"})
			waitQuayTeamUsersFinalizerOnConfigMap(usersConfigMapKey)

			Eventually(func() bool { return isEnsureTeamInvoked }, timeout, interval).Should(BeTrue())
			Eventually(func() bool { return isListRepositoryPermissionsForTeamInvoked }, timeout, interval).Should(BeTrue())
			Eventually(func() int { return countAddUserToTeamInvoked }, timeout, interval).Should(Equal(2))
			Eventually(func() int { return countRemoveUserFromTeamInvoked }, timeout, interval).Should(Equal(2))
		})

		It("config map was updated, 2 new users added, team exists and has already 2 users, add 1 new user to the team, imageRepositories don't exist", func() {
			isEnsureTeamInvoked := false
			isListRepositoryPermissionsForTeamInvoked := false
			countAddUserToTeamInvoked := 0
			isDeleteTeamInvoked := false

			quay.EnsureTeamFunc = func(organization, teamName string) ([]quay.Member, error) {
				defer GinkgoRecover()
				Expect(organization).To(Equal(quay.TestQuayOrg))
				Expect(teamName).To(Equal(expectedTeamName))
				isEnsureTeamInvoked = true
				return []quay.Member{
					{Name: "user1", Kind: "user", IsRobot: false, Invited: false},
					{Name: "user2", Kind: "user", IsRobot: false, Invited: false}}, nil
			}
			quay.ListRepositoryPermissionsForTeamFunc = func(organization, teamName string) ([]quay.TeamPermission, error) {
				defer GinkgoRecover()
				Expect(organization).To(Equal(quay.TestQuayOrg))
				Expect(teamName).To(Equal(expectedTeamName))
				isListRepositoryPermissionsForTeamInvoked = true
				return []quay.TeamPermission{}, nil
			}
			quay.AddUserToTeamFunc = func(organization, teamName, userName string) (bool, error) {
				defer GinkgoRecover()
				Expect(organization).To(Equal(quay.TestQuayOrg))
				Expect(teamName).To(Equal(expectedTeamName))
				Expect(userName).To(BeElementOf([]string{"user3", "user4"}))
				countAddUserToTeamInvoked++
				return false, nil
			}

			addUsersToUsersConfigMap(usersConfigMapKey, []string{"user3", "user4"})

			Eventually(func() bool { return isEnsureTeamInvoked }, timeout, interval).Should(BeTrue())
			Eventually(func() bool { return isListRepositoryPermissionsForTeamInvoked }, timeout, interval).Should(BeTrue())
			Eventually(func() int { return countAddUserToTeamInvoked }, timeout, interval).Should(Equal(2))

			quay.DeleteTeamFunc = func(organization, teamName string) error {
				defer GinkgoRecover()
				Expect(organization).To(Equal(quay.TestQuayOrg))
				Expect(teamName).To(Equal(expectedTeamName))
				isDeleteTeamInvoked = true
				return nil
			}
			deleteUsersConfigMap(usersConfigMapKey)
			Eventually(func() bool { return isDeleteTeamInvoked }, timeout, interval).Should(BeTrue())
		})

		It("create image repositories", func() {
			quay.ResetTestQuayClient()

			imageRepository1 := types.NamespacedName{Name: "imagerepository1", Namespace: configTestNamespace}
			imageRepository2 := types.NamespacedName{Name: "imagerepository2", Namespace: configTestNamespace}
			createImageRepository(imageRepositoryConfig{ImageName: imageRepositoryName1, ResourceKey: &imageRepository1})
			waitImageRepositoryFinalizerOnImageRepository(imageRepository1)
			createImageRepository(imageRepositoryConfig{ImageName: imageRepositoryName2, ResourceKey: &imageRepository2})
			waitImageRepositoryFinalizerOnImageRepository(imageRepository2)
		})

		It("should create team with 2 users, team doesn't exist, imageRepositories exist and add permissions to the team", func() {
			isEnsureTeamInvoked := false
			isListRepositoryPermissionsForTeamInvoked := false
			countAddUserToTeamInvoked := 0
			countAddReadPermissionsForRepositoryToTeamInvoked := 0
			isDeleteTeamInvoked := false

			quay.CreateRepositoryFunc = func(repository quay.RepositoryRequest) (*quay.Repository, error) {
				defer GinkgoRecover()
				return &quay.Repository{}, nil
			}
			quay.CreateRobotAccountFunc = func(organization, robotName string) (*quay.RobotAccount, error) {
				return &quay.RobotAccount{}, nil
			}

			quay.EnsureTeamFunc = func(organization, teamName string) ([]quay.Member, error) {
				defer GinkgoRecover()
				Expect(organization).To(Equal(quay.TestQuayOrg))
				Expect(teamName).To(Equal(expectedTeamName))
				isEnsureTeamInvoked = true
				return []quay.Member{}, nil
			}
			quay.ListRepositoryPermissionsForTeamFunc = func(organization, teamName string) ([]quay.TeamPermission, error) {
				defer GinkgoRecover()
				Expect(organization).To(Equal(quay.TestQuayOrg))
				Expect(teamName).To(Equal(expectedTeamName))
				isListRepositoryPermissionsForTeamInvoked = true
				return []quay.TeamPermission{}, nil
			}

			quay.AddUserToTeamFunc = func(organization, teamName, userName string) (bool, error) {
				defer GinkgoRecover()
				Expect(organization).To(Equal(quay.TestQuayOrg))
				Expect(teamName).To(Equal(expectedTeamName))
				Expect(userName).To(BeElementOf([]string{"user1", "user2"}))
				countAddUserToTeamInvoked++
				return false, nil
			}
			quay.AddReadPermissionsForRepositoryToTeamFunc = func(organization, imageRepository, teamName string) error {
				defer GinkgoRecover()
				Expect(organization).To(Equal(quay.TestQuayOrg))
				Expect(imageRepository).To(BeElementOf([]string{imageRepositoryName1, imageRepositoryName2}))
				Expect(teamName).To(Equal(expectedTeamName))
				countAddReadPermissionsForRepositoryToTeamInvoked++
				return nil
			}
			createUsersConfigMap(usersConfigMapKey, []string{"user1", "user2"})
			waitQuayTeamUsersFinalizerOnConfigMap(usersConfigMapKey)

			Eventually(func() bool { return isEnsureTeamInvoked }, timeout, interval).Should(BeTrue())
			Eventually(func() bool { return isListRepositoryPermissionsForTeamInvoked }, timeout, interval).Should(BeTrue())
			Eventually(func() int { return countAddUserToTeamInvoked }, timeout, interval).Should(Equal(2))
			Eventually(func() int { return countAddReadPermissionsForRepositoryToTeamInvoked }, timeout, interval).Should(Equal(2))

			quay.DeleteTeamFunc = func(organization, teamName string) error {
				defer GinkgoRecover()
				Expect(organization).To(Equal(quay.TestQuayOrg))
				Expect(teamName).To(Equal(expectedTeamName))
				isDeleteTeamInvoked = true
				return nil
			}
			deleteUsersConfigMap(usersConfigMapKey)
			Eventually(func() bool { return isDeleteTeamInvoked }, timeout, interval).Should(BeTrue())
		})

		It("should cleanup environment", func() {
			deleteNamespace(configTestNamespace)
		})

	})

	Context("Users config map creation, namespace starts with number", func() {
		var configTestNamespace = "1config-map-namespace-test"
		var usersConfigMapKey = types.NamespacedName{Name: additionalUsersConfigMapName, Namespace: configTestNamespace}
		expectedTeamName := "x1configxmapxnamespacextestxteam"

		BeforeEach(func() {
			quay.ResetTestQuayClientToFails()
		})

		It("should prepare environment", func() {
			createNamespace(configTestNamespace)
		})

		It("team doesn't exist, requested 2 users, imageRepositories don't exist, create team with and add 2 users", func() {
			isEnsureTeamInvoked := false
			isListRepositoryPermissionsForTeamInvoked := false
			countAddUserToTeamInvoked := 0
			isDeleteTeamInvoked := false

			quay.EnsureTeamFunc = func(organization, teamName string) ([]quay.Member, error) {
				defer GinkgoRecover()
				Expect(organization).To(Equal(quay.TestQuayOrg))
				Expect(teamName).To(Equal(expectedTeamName))
				isEnsureTeamInvoked = true
				return []quay.Member{}, nil
			}
			quay.ListRepositoryPermissionsForTeamFunc = func(organization, teamName string) ([]quay.TeamPermission, error) {
				defer GinkgoRecover()
				Expect(organization).To(Equal(quay.TestQuayOrg))
				Expect(teamName).To(Equal(expectedTeamName))
				isListRepositoryPermissionsForTeamInvoked = true
				return []quay.TeamPermission{}, nil
			}
			quay.AddUserToTeamFunc = func(organization, teamName, userName string) (bool, error) {
				defer GinkgoRecover()
				Expect(organization).To(Equal(quay.TestQuayOrg))
				Expect(teamName).To(Equal(expectedTeamName))
				Expect(userName).To(BeElementOf([]string{"user1", "user2"}))
				countAddUserToTeamInvoked++
				return false, nil
			}

			createUsersConfigMap(usersConfigMapKey, []string{"user1", "user2"})
			waitQuayTeamUsersFinalizerOnConfigMap(usersConfigMapKey)

			Eventually(func() bool { return isEnsureTeamInvoked }, timeout, interval).Should(BeTrue())
			Eventually(func() bool { return isListRepositoryPermissionsForTeamInvoked }, timeout, interval).Should(BeTrue())
			Eventually(func() int { return countAddUserToTeamInvoked }, timeout, interval).Should(Equal(2))

			quay.DeleteTeamFunc = func(organization, teamName string) error {
				defer GinkgoRecover()
				Expect(organization).To(Equal(quay.TestQuayOrg))
				Expect(teamName).To(Equal(expectedTeamName))
				isDeleteTeamInvoked = true
				return nil
			}
			deleteUsersConfigMap(usersConfigMapKey)
			Eventually(func() bool { return isDeleteTeamInvoked }, timeout, interval).Should(BeTrue())
		})

		It("should cleanup environment", func() {
			deleteNamespace(configTestNamespace)
		})
	})
})

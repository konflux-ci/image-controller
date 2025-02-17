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
	"context"
	"fmt"
	"strings"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	imagerepositoryv1alpha1 "github.com/konflux-ci/image-controller/api/v1alpha1"
	l "github.com/konflux-ci/image-controller/pkg/logs"
	"github.com/konflux-ci/image-controller/pkg/quay"
)

const (
	ConfigMapFinalizer = "appstudio.openshift.io/quay-team-users"
)

// QuayUsersConfigMapReconciler reconciles a ConfigMap object with users
type QuayUsersConfigMapReconciler struct {
	client.Client
	Scheme *runtime.Scheme

	QuayClient       quay.QuayService
	BuildQuayClient  func(logr.Logger) quay.QuayService
	QuayOrganization string
}

// SetupWithManager sets up the controller with the Manager.
func (r *QuayUsersConfigMapReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&corev1.ConfigMap{}, builder.WithPredicates(predicate.Funcs{
			CreateFunc: func(e event.CreateEvent) bool {
				new, ok := e.Object.(*corev1.ConfigMap)
				if !ok {
					return false
				}
				return IsAdditionalUsersConfigMap(new)
			},
			UpdateFunc: func(e event.UpdateEvent) bool {
				new, ok := e.ObjectNew.(*corev1.ConfigMap)
				if !ok {
					return false
				}
				return IsAdditionalUsersConfigMap(new)
			},
			DeleteFunc: func(e event.DeleteEvent) bool {
				new, ok := e.Object.(*corev1.ConfigMap)
				if !ok {
					return false
				}
				return IsAdditionalUsersConfigMap(new)
			},
			GenericFunc: func(e event.GenericEvent) bool {
				new, ok := e.Object.(*corev1.ConfigMap)
				if !ok {
					return false
				}
				return IsAdditionalUsersConfigMap(new)
			},
		})).
		Complete(r)
}

func IsAdditionalUsersConfigMap(configMap *corev1.ConfigMap) bool {
	return configMap.Name == additionalUsersConfigMapName
}

//+kubebuilder:rbac:groups=appstudio.redhat.com,resources=imagerepositories,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=core,resources=configmaps,verbs=get;list;watch;update

func (r *QuayUsersConfigMapReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := ctrllog.FromContext(ctx).WithName("QuayUsersConfigMap")
	ctx = ctrllog.IntoContext(ctx, log)

	// fetch the config map instance
	configMap := &corev1.ConfigMap{}
	if err := r.Client.Get(ctx, req.NamespacedName, configMap); err != nil {
		if errors.IsNotFound(err) {
			// The object is deleted, nothing to do
			return ctrl.Result{}, nil
		}
		log.Error(err, "failed to get config map", l.Action, l.ActionView)
		return ctrl.Result{}, err
	}

	if !controllerutil.ContainsFinalizer(configMap, ConfigMapFinalizer) {
		controllerutil.AddFinalizer(configMap, ConfigMapFinalizer)

		if err := r.Client.Update(ctx, configMap); err != nil {
			log.Error(err, "failed to add finalizer to config map", "ConfigMapName", additionalUsersConfigMapName, "Finalizer", ConfigMapFinalizer, l.Action, l.ActionUpdate)
			return ctrl.Result{}, err
		}
		log.Info("finalizer added to configmap")
		// leave all other steps to reconcile from update action
		return ctrl.Result{}, nil
	}

	teamName := getQuayTeamName(configMap.Namespace)
	removeTeam := false

	if !configMap.DeletionTimestamp.IsZero() {
		removeTeam = true
		log.Info("Config map with additional users is being removed, will delete team", "TeamName", teamName)
	}

	var additionalUsers []string
	if !removeTeam {
		// get additional users from config map
		additionalUsersStr, usersExist := configMap.Data[additionalUsersConfigMapKey]
		if !usersExist {
			log.Info("Config map with additional users doesn't have the key", "ConfigMapName", additionalUsersConfigMapName, "ConfigMapKey", additionalUsersConfigMapKey, l.Action, l.ActionView)
			removeTeam = true
		} else {
			additionalUsers = strings.Fields(strings.TrimSpace(additionalUsersStr))
			log.Info("Additional users configured in config map", "AdditionalUsers", additionalUsers)
		}
	}

	// reread quay token
	r.QuayClient = r.BuildQuayClient(log)

	// remove team if config map is being removed, or doesn't contain key additionalUsersConfigMapKey
	if removeTeam {
		log.Info("Will remove team", "TeamName", teamName)
		if err := r.QuayClient.DeleteTeam(r.QuayOrganization, teamName); err != nil {
			log.Error(err, "failed to remove team", "TeamName", teamName, l.Action, l.ActionDelete)
			return ctrl.Result{}, err
		}

		// remove finalizer after team is removed
		if !configMap.DeletionTimestamp.IsZero() {
			if controllerutil.ContainsFinalizer(configMap, ConfigMapFinalizer) {
				controllerutil.RemoveFinalizer(configMap, ConfigMapFinalizer)
				if err := r.Client.Update(ctx, configMap); err != nil {
					log.Error(err, "failed to remove config map finalizer", "ConfigMapName", additionalUsersConfigMapName, "Finalizer", ConfigMapFinalizer, l.Action, l.ActionUpdate)
					return ctrl.Result{}, err
				}
				log.Info("finalizer removed from config map", l.Action, l.ActionDelete)
			}
		}

		return ctrl.Result{}, nil
	}

	allImageRepos, err := r.getAllImageRepositoryNames(ctx, configMap.Namespace)
	if err != nil {
		return ctrl.Result{}, err
	}

	// get team members, if team doesn't exist it will be created
	log.Info("Ensure team", "TeamName", teamName)
	teamMembers, err := r.QuayClient.EnsureTeam(r.QuayOrganization, teamName)
	if err != nil {
		log.Error(err, "failed to get team members", "TeamName", teamName, l.Action, l.ActionView)
		return ctrl.Result{}, err
	}

	// get team permissions
	teamPermissions, err := r.QuayClient.ListRepositoryPermissionsForTeam(r.QuayOrganization, teamName)
	if err != nil {
		log.Error(err, "failed to get team permissions", "TeamName", teamName, l.Action, l.ActionView)
		return ctrl.Result{}, err
	}
	// get repositories for which team has permissions
	imageReposTeamHasPermissions := []string{}
	for _, repoPermission := range teamPermissions {
		imageReposTeamHasPermissions = append(imageReposTeamHasPermissions, repoPermission.Repository.Name)
	}
	log.Info("Team has repository permissions", "TeamName", teamName, "Repositories", imageReposTeamHasPermissions)

	// grant repo permissions to the team
	imageReposToAddToTeam := filterListDifference(allImageRepos, imageReposTeamHasPermissions)

	for _, repoToUpdate := range imageReposToAddToTeam {
		log.Info("Grant repository permission to the team", "TeamName", teamName, "RepositoryName", repoToUpdate)
		if err := r.QuayClient.AddReadPermissionsForRepositoryToTeam(r.QuayOrganization, repoToUpdate, teamName); err != nil {
			log.Error(err, "failed to grant repo permission to the team", "TeamName", teamName, "RepositoryName", repoToUpdate, l.Action, l.ActionAdd)
			return ctrl.Result{}, err
		}
	}

	// get users in the team
	usersInTeam := []string{}
	for _, user := range teamMembers {
		usersInTeam = append(usersInTeam, user.Name)
	}
	log.Info("Users in the team", "TeamName", teamName, "Users", usersInTeam)

	// add users to the team
	usersToAdd := filterListDifference(additionalUsers, usersInTeam)
	for _, userToAdd := range usersToAdd {
		log.Info("Add user to the team", "TeamName", teamName, "UserName", userToAdd)
		if permanentError, err := r.QuayClient.AddUserToTeam(r.QuayOrganization, teamName, userToAdd); err != nil {
			if !permanentError {
				log.Error(err, "failed to add user to the team", "TeamName", teamName, "UserName", userToAdd, l.Action, l.ActionAdd)
				return ctrl.Result{}, err
			}
			// if user doesn't exist just log we don't have to fail because of that
			log.Info(err.Error())
		}
	}

	// remove users from the team
	usersToRemove := filterListDifference(usersInTeam, additionalUsers)
	for _, userToRemove := range usersToRemove {
		log.Info("Remove user from the team", "TeamName", teamName, "UserName", userToRemove)
		if err := r.QuayClient.RemoveUserFromTeam(r.QuayOrganization, teamName, userToRemove); err != nil {
			log.Error(err, "failed to remove user from the team", "TeamName", teamName, "UserName", userToRemove, l.Action, l.ActionDelete)
			return ctrl.Result{}, err
		}
	}

	return ctrl.Result{}, nil
}

func (r *QuayUsersConfigMapReconciler) getAllImageRepositoryNames(ctx context.Context, namespaceName string) ([]string, error) {
	log := ctrllog.FromContext(ctx)

	// fetch image repositories in the namespace
	imageRepositoryList := &imagerepositoryv1alpha1.ImageRepositoryList{}
	if err := r.Client.List(ctx, imageRepositoryList, &client.ListOptions{Namespace: namespaceName}); err != nil {
		log.Error(err, "failed to list ImageRepositories", l.Action, l.ActionView)
		return nil, err
	}

	// get image repositories names
	allImageRepos := []string{}
	for _, repository := range imageRepositoryList.Items {
		allImageRepos = append(allImageRepos, repository.Spec.Image.Name)
	}
	return allImageRepos, nil
}

// getQuayTeamName returns team name based on sanitized namespace
func getQuayTeamName(namespace string) string {
	// quay team allowed chars are : ^[a-z][a-z0-9]+$.
	// namespace allowed chars are : ^[a-z0-9]([-a-z0-9]*[a-z0-9])?$
	validNamespaceString := strings.ReplaceAll(namespace, "-", "x")

	if validNamespaceString[0] >= '0' && validNamespaceString[0] <= '9' {
		validNamespaceString = fmt.Sprintf("x%s", validNamespaceString)
	}

	return fmt.Sprintf("%sxteam", validNamespaceString)
}

// filterListDifference returns list with values which are in the 1st list, but aren't in 2nd list
func filterListDifference(firstList, secondList []string) []string {
	filteredList := []string{}
	for _, itemToAdd := range firstList {
		shouldAdd := true
		for _, itemInList := range secondList {
			if itemToAdd == itemInList {
				shouldAdd = false
				break
			}
		}
		if shouldAdd {
			filteredList = append(filteredList, itemToAdd)
		}
	}
	return filteredList
}

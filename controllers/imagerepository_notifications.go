package controllers

import (
	"context"
	"strings"

	imagerepositoryv1alpha1 "github.com/konflux-ci/image-controller/api/v1alpha1"
	"github.com/konflux-ci/image-controller/pkg/quay"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"
)

func (r *ImageRepositoryReconciler) AddNotification(notification imagerepositoryv1alpha1.Notifications, imageRepository *imagerepositoryv1alpha1.ImageRepository) (imagerepositoryv1alpha1.NotificationStatus, error) {
	notificationStatus := imagerepositoryv1alpha1.NotificationStatus{}
	quayNotification, err := r.QuayClient.CreateNotification(
		r.QuayOrganization,
		imageRepository.Spec.Image.Name,
		quay.Notification{
			Title:  notification.Title,
			Event:  string(notification.Event),
			Method: string(notification.Method),
			Config: quay.NotificationConfig{
				Url: notification.Config.Url,
			},
			EventConfig: quay.NotificationEventConfig{},
		})

	if err != nil {
		return notificationStatus, err
	}
	return imagerepositoryv1alpha1.NotificationStatus{
		UUID:  quayNotification.UUID,
		Title: notification.Title,
	}, nil
}

func (r *ImageRepositoryReconciler) checkNotificationChangesExists(imageRepository *imagerepositoryv1alpha1.ImageRepository, allNotifications []quay.Notification) bool {
	if len(allNotifications) != len(imageRepository.Spec.Notifications) {
		return true
	}
	for _, quayNotification := range allNotifications {
		existsAndHasntChanged := false
		for _, notification := range imageRepository.Spec.Notifications {
			if quayNotification.Title == notification.Title && !isNotificationChanged(notification, quayNotification) {
				existsAndHasntChanged = true
				break
			}
		}
		if !existsAndHasntChanged {
			return true
		}
	}

	return false
}

func (r *ImageRepositoryReconciler) HandleNotifications(ctx context.Context, imageRepository *imagerepositoryv1alpha1.ImageRepository) error {
	log := ctrllog.FromContext(ctx).WithName("HandleNotifications")

	if imageRepository.Status.Notifications == nil && imageRepository.Spec.Notifications == nil {
		// No status notifications to check
		return nil
	}
	allNotifications, err := r.QuayClient.GetNotifications(r.QuayOrganization, imageRepository.Spec.Image.Name)
	if err != nil {
		return r.handleError(ctx, imageRepository, err, "Couldn't retrieve all Quay notifications")
	}
	if !r.checkNotificationChangesExists(imageRepository, allNotifications) {
		return nil
	}

	log.Info("Starting to handle notifications")
	for _, notification := range imageRepository.Spec.Notifications {
		existsInStatus := false
		for index := 0; index < len(imageRepository.Status.Notifications); index++ {
			statusNotification := &imageRepository.Status.Notifications[index] // This way we can update the UUID if notification gets updated
			if notification.Title == statusNotification.Title {
				existsInStatus = true
				quayNotification, err := r.notificationExistsInQuayByUUID(statusNotification.UUID, imageRepository)
				if err != nil {
					return r.handleError(ctx, imageRepository, err, "Couldn't retrieve all Quay notifications")
				}
				if isNotificationChanged(notification, quayNotification) {
					// Updated item in Spec.Notifications: update notification in Quay
					log.Info("Updating notification in Quay", "Title", notification.Title, "Event", notification.Event, "Method", notification.Method, "UUID", quayNotification.UUID)
					updatedNotification, err := r.QuayClient.UpdateNotification(
						r.QuayOrganization,
						imageRepository.Spec.Image.Name,
						statusNotification.UUID,
						quay.Notification{
							Title:  notification.Title,
							Event:  string(notification.Event),
							Method: string(notification.Method),
							Config: quay.NotificationConfig{
								Url: notification.Config.Url,
							},
							EventConfig: quay.NotificationEventConfig{},
						})
					if err != nil {
						log.Error(err, "failed to update notification", "Title", statusNotification.Title, "UUID", statusNotification.UUID)
						return r.handleError(ctx, imageRepository, err, "Error while updating notification ("+notification.Title+")")
					}
					statusNotification.UUID = updatedNotification.UUID
					statusNotification.Title = updatedNotification.Title
					break
				}
			}
		}
		if !existsInStatus {
			log.Info("Adding new notification to Quay", "Title", notification.Title, "Event", notification.Event, "Method", notification.Method)
			resStatusNotification, err := r.AddNotification(notification, imageRepository)
			if err != nil {
				log.Error(err, "failed to add notification", "Title", notification.Title, "Event", notification.Event, "Method", notification.Method)
				return r.handleError(ctx, imageRepository, err, "Error while adding a notification ("+notification.Title+") to Quay")
			}
			alreadyInStatus := false
			for _, statusNotificationAux := range imageRepository.Status.Notifications {
				if resStatusNotification.UUID == statusNotificationAux.UUID {
					alreadyInStatus = true
					break
				}
			}
			if !alreadyInStatus {
				imageRepository.Status.Notifications = append(imageRepository.Status.Notifications, resStatusNotification)
			}
		}
	}
	if len(imageRepository.Status.Notifications) > len(imageRepository.Spec.Notifications) {
		// There are notifications to be deleted
		for index, statusNotification := range imageRepository.Status.Notifications {
			existsInSpec := false
			for _, notification := range imageRepository.Spec.Notifications {
				if notification.Title == statusNotification.Title {
					existsInSpec = true
					break
				}
			}
			if !existsInSpec {
				log.Info("Deleting notification in Quay", "Title", statusNotification.Title, "UUID", statusNotification.UUID)
				_, err := r.QuayClient.DeleteNotification(
					r.QuayOrganization,
					imageRepository.Spec.Image.Name,
					statusNotification.UUID)
				if err != nil {
					log.Error(err, "failed to delete notification", "Title", statusNotification.Title, "UUID", statusNotification.UUID)
					return r.handleError(ctx, imageRepository, err, "Error while deleting a notification ("+statusNotification.Title+") to Quay")
				}
				// Remove notification from CR status
				imageRepository.Status.Notifications = append(imageRepository.Status.Notifications[:index], imageRepository.Status.Notifications[index+1:]...)
			}
		}
	}

	return nil
}

// This function adds all Spec.Notifications to Quay and overwrites all existing notifications in ImageRepository Status
func (r *ImageRepositoryReconciler) SetNotifications(ctx context.Context, imageRepository *imagerepositoryv1alpha1.ImageRepository) ([]imagerepositoryv1alpha1.NotificationStatus, error) {
	log := ctrllog.FromContext(ctx).WithName("ConfigureNotifications")

	if imageRepository.Spec.Notifications == nil {
		// No notifications to configure
		return nil, nil
	}

	log.Info("Adding notifications")
	notificationStatus := []imagerepositoryv1alpha1.NotificationStatus{}

	for _, notification := range imageRepository.Spec.Notifications {
		log.Info("Creating notification in Quay", "Title", notification.Title, "Event", notification.Event, "Method", notification.Method)
		notificationStatusRes, err := r.AddNotification(notification, imageRepository)
		if err != nil {
			log.Error(err, "failed to create notification", "Title", notification.Title, "Event", notification.Event, "Method", notification.Method)
			resErr := r.handleError(ctx, imageRepository, err, "Error while adding a notification ("+notification.Title+") to Quay")
			return nil, resErr
		}
		notificationStatus = append(notificationStatus, notificationStatusRes)
		log.Info("Notification added",
			"Title", notification.Title,
			"Event", notification.Event,
			"Method", notification.Method,
			"QuayNotification", notificationStatusRes)
	}
	return notificationStatus, nil
}

func (r *ImageRepositoryReconciler) notificationExistsInQuayByUUID(UUID string, imageRepository *imagerepositoryv1alpha1.ImageRepository) (quay.Notification, error) {
	notification := quay.Notification{}
	allNotifications, err := r.QuayClient.GetNotifications(r.QuayOrganization, imageRepository.Spec.Image.Name)
	if err != nil {
		return notification, nil
	}
	for _, quayNotification := range allNotifications {
		if quayNotification.UUID == UUID {
			notification = quayNotification
			break
		}
	}

	return notification, nil
}

func isNotificationChanged(notification imagerepositoryv1alpha1.Notifications, quayNotification quay.Notification) bool {
	return quayNotification.UUID != "" && (quayNotification.Title != notification.Title || quayNotification.Event != string(notification.Event) || quayNotification.Method != string(notification.Method) || quayNotification.Config.Url != notification.Config.Url)
}

func (r *ImageRepositoryReconciler) handleError(ctx context.Context, imageRepository *imagerepositoryv1alpha1.ImageRepository, err error, errorStatusMessage string) error {
	if strings.Contains(err.Error(), "400") || strings.Contains(err.Error(), "404") {
		if err = r.UpdateImageRepositoryStatusMessage(ctx, imageRepository, errorStatusMessage); err != nil {
			return err
		}
		return nil
	} else {
		return err
	}
}

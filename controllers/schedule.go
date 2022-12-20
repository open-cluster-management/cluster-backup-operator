/*
Copyright 2021.

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
	"sort"
	"strings"

	hivev1 "github.com/openshift/hive/apis/hive/v1"
	"github.com/robfig/cron/v3"
	v1beta1 "github.com/stolostron/cluster-backup-operator/api/v1beta1"
	veleroapi "github.com/vmware-tanzu/velero/pkg/apis/velero/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/selection"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

const (
	// FailedPhaseMsg for when Velero schedule initialization failed
	FailedPhaseMsg string = "Velero schedules initialization failed"
	// NewPhaseMsg for when Velero schedule initialization succeeded
	NewPhaseMsg string = "Velero schedules are initialized"
	// EnabledPhaseMsg for when Velero schedules are processed by velero and enabled
	EnabledPhaseMsg string = "Velero schedules are enabled"
	// UnknownPhaseMsg for when some Velero schedules are not enabled
	UnknownPhaseMsg string = "Some Velero schedules are not enabled. " +
		"If the status doesn't change check the velero pod is running and " +
		"that you have created a Velero resource as documented in the install guide."
	// BackupCollisionPhaseMsg when another cluster is creating backups at the same storage location
	BackupCollisionPhaseMsg string = "Backup %s, from cluster with id [%s] is using the same storage location." +
		" This is a backup collision with current cluster [%s] backup." +
		" Review and resolve the collision then create a new BackupSchedule resource to " +
		" resume backups from this cluster."
	update_msg = "Updated secret %s in ns %s"
)

func updateScheduleStatus(
	ctx context.Context,
	veleroSchedule *veleroapi.Schedule,
	backupSchedule *v1beta1.BackupSchedule,
) {
	scheduleLogger := log.FromContext(ctx)

	scheduleLogger.Info(
		"Updating status with a copy of velero schedule",
		"name", veleroSchedule.Name,
		"namespace", veleroSchedule.Namespace,
	)

	for key, value := range veleroScheduleNames {
		if veleroSchedule.Name == value {
			// set veleroSchedule in backupSchedule status
			setVeleroScheduleInStatus(key, veleroSchedule, backupSchedule)
		}
	}
}

func setVeleroScheduleInStatus(
	resourceType ResourceType,
	veleroSchedule *veleroapi.Schedule,
	backupSchedule *v1beta1.BackupSchedule,
) {
	switch resourceType {
	case ManagedClusters:
		backupSchedule.Status.VeleroScheduleManagedClusters = veleroSchedule.DeepCopy()
	case Credentials:
		backupSchedule.Status.VeleroScheduleCredentials = veleroSchedule.DeepCopy()
	case Resources:
		backupSchedule.Status.VeleroScheduleResources = veleroSchedule.DeepCopy()
	}
}

// set cumulative status of schedules
func setSchedulePhase(
	schedules *veleroapi.ScheduleList,
	backupSchedule *v1beta1.BackupSchedule,
) {
	if backupSchedule.Status.Phase == v1beta1.SchedulePhaseBackupCollision {
		return
	}

	if schedules == nil || len(schedules.Items) <= 0 {
		backupSchedule.Status.Phase = v1beta1.SchedulePhaseNew
		backupSchedule.Status.LastMessage = NewPhaseMsg
		return
	}

	// get all schedules and check status for each
	for i := range schedules.Items {
		veleroSchedule := &schedules.Items[i]
		if veleroSchedule.Status.Phase == "" {
			backupSchedule.Status.Phase = v1beta1.SchedulePhaseUnknown
			backupSchedule.Status.LastMessage = UnknownPhaseMsg
			return
		}
		if veleroSchedule.Status.Phase == veleroapi.SchedulePhaseNew {
			backupSchedule.Status.Phase = v1beta1.SchedulePhaseNew
			backupSchedule.Status.LastMessage = NewPhaseMsg
			return
		}
		if veleroSchedule.Status.Phase == veleroapi.SchedulePhaseFailedValidation {
			backupSchedule.Status.Phase = v1beta1.SchedulePhaseFailedValidation
			backupSchedule.Status.LastMessage = FailedPhaseMsg
			return
		}
	}

	// if no velero schedule with FailedValidation, New or empty status, they are all enabled
	backupSchedule.Status.Phase = v1beta1.SchedulePhaseEnabled
	backupSchedule.Status.LastMessage = EnabledPhaseMsg
}

func isScheduleSpecUpdated(
	schedules *veleroapi.ScheduleList,
	backupSchedule *v1beta1.BackupSchedule,
) bool {

	if schedules == nil || len(schedules.Items) <= 0 {
		return false
	}

	for i := range schedules.Items {
		veleroSchedule := &schedules.Items[i]

		if veleroSchedule.Name != veleroScheduleNames[ValidationSchedule] &&
			veleroSchedule.Spec.Template.TTL.Duration != backupSchedule.Spec.VeleroTTL.Duration {
			// validation backup TTL should be ignored here
			// since that one is using the schedule's cron job interval
			return true
		}
		if veleroSchedule.Spec.Schedule != backupSchedule.Spec.VeleroSchedule {
			return true
		}
	}

	return false
}

func parseCronSchedule(
	ctx context.Context,
	backupSchedule *v1beta1.BackupSchedule,
) []string {
	var validationErrors []string

	// cron.Parse panics if schedule is empty
	if len(backupSchedule.Spec.VeleroSchedule) == 0 {
		validationErrors = append(
			validationErrors,
			"Schedule must be a non-empty valid Cron expression",
		)
		return validationErrors
	}

	scheduleLogger := log.FromContext(ctx)

	// adding a recover() around cron.Parse because it panics on empty string and is possible
	// that it panics under other scenarios as well.
	func() {
		defer func() {
			if r := recover(); r != nil {
				scheduleLogger.Info(
					"Panic parsing schedule",
					"schedule", backupSchedule.Spec.VeleroSchedule,
				)
				validationErrors = append(validationErrors, fmt.Sprintf("invalid schedule: %v", r))
			}
		}()

		if _, err := cron.ParseStandard(backupSchedule.Spec.VeleroSchedule); err != nil {
			scheduleLogger.Error(
				err,
				"Error parsing schedule",
				"schedule", backupSchedule.Spec.VeleroSchedule,
			)
			validationErrors = append(validationErrors, fmt.Sprintf("invalid schedule: %v", err))
		}
	}()

	if len(validationErrors) > 0 {
		return validationErrors
	}

	return nil
}

// returns true if this schedule has generated the latest backups in the
// storage location
func (r *BackupScheduleReconciler) scheduleOwnsLatestStorageBackups(
	ctx context.Context,
	backupSchedule *veleroapi.Schedule,
) (bool, *veleroapi.Backup) {

	logger := log.FromContext(ctx)

	backups := veleroapi.BackupList{}
	if err := r.List(ctx, &backups,
		client.MatchingLabels{"velero.io/schedule-name": veleroScheduleNames[Resources]}); err != nil {
		logger.Info(err.Error())
		return true, nil
	}
	// get only acm resources backups and not in deleting state
	// which are backups starting with acm-resources-schedule
	sliceBackups := filterBackups(backups.Items[:], func(bkp veleroapi.Backup) bool {
		return bkp.Status.Phase != veleroapi.BackupPhaseDeleting
	})

	// sort backups
	sort.Slice(sliceBackups, func(i, j int) bool {
		var timeA int64
		var timeB int64
		if sliceBackups[i].Status.StartTimestamp != nil {
			timeA = sliceBackups[i].Status.StartTimestamp.Time.Unix()
		}
		if sliceBackups[j].Status.StartTimestamp != nil {
			timeB = sliceBackups[j].Status.StartTimestamp.Time.Unix()
		}
		return timeA < timeB
	})

	if len(sliceBackups) == 0 {
		return true, nil
	}
	lastBackup := sliceBackups[len(sliceBackups)-1]

	if lastBackup.Labels[BackupScheduleClusterLabel] != backupSchedule.GetLabels()[BackupScheduleClusterLabel] {
		return false, &lastBackup
	}

	return true, nil
}

// prepare resources before backing up
func prepareForBackup(ctx context.Context,
	c client.Client,
) {
	logger := log.FromContext(ctx)
	// update secrets for clusterDeployments created by cluster claims
	clusterDeployments := &hivev1.ClusterDeploymentList{}
	if err := c.List(ctx, clusterDeployments, &client.ListOptions{}); err == nil {
		for i := range clusterDeployments.Items {
			clusterDeployment := clusterDeployments.Items[i]
			if clusterDeployment.Spec.ClusterPoolRef != nil {
				secrets := &corev1.SecretList{}
				if err := c.List(ctx, secrets, &client.ListOptions{
					Namespace: clusterDeployments.Items[i].Namespace,
				}); err == nil {
					// add backup labels if not set yet
					updateSecretsLabels(ctx, c, *secrets, clusterDeployments.Items[i].Name,
						backupCredsClusterLabel,
						"clusterpool")
				}

				// add a label annnotation to the resource
				// to disable the creation webhook validation
				// which doesn't allow restoring the ClusterDeployment
				labels := clusterDeployment.GetLabels()
				if labels == nil {
					labels = make(map[string]string)
				}
				labels["hive.openshift.io/disable-creation-webhook-for-dr"] = "true"
				clusterDeployment.SetLabels(labels)
				msg := "update clusterDeployment " + clusterDeployment.Name
				logger.Info(msg)
				if err := c.Update(ctx, &clusterDeployment, &client.UpdateOptions{}); err != nil {
					logger.Error(err, "failed to update clusterDeployment")
				}
			}
		}
	}

	// update secrets for cluster pools
	clusterPools := &hivev1.ClusterPoolList{}
	if err := c.List(ctx, clusterPools, &client.ListOptions{}); err == nil {
		for i := range clusterPools.Items {
			secrets := &corev1.SecretList{}
			if err := c.List(ctx, secrets, &client.ListOptions{
				Namespace: clusterPools.Items[i].Namespace,
			}); err == nil {
				updateSecretsLabels(ctx, c, *secrets, clusterPools.Items[i].Name,
					backupCredsClusterLabel,
					"clusterpool")
			}
		}
	}
	// update infraSecrets
	aiSecrets := &corev1.SecretList{}
	if agentInstallLabel, err := labels.NewRequirement("agent-install.openshift.io/watch",
		selection.In, []string{"true"}); err == nil {

		// Init and add to selector.
		selector := labels.NewSelector()
		selector = selector.Add(*agentInstallLabel)
		if err := c.List(ctx, aiSecrets, &client.ListOptions{
			LabelSelector: selector,
		}); err == nil {
			for s := range aiSecrets.Items {
				updateSecret(ctx, c, aiSecrets.Items[s],
					backupCredsClusterLabel,
					"agent-install")
			}
		}
	}
	// update metal
	metalSecrets := &corev1.SecretList{}
	if metalInstallLabel, err := labels.NewRequirement("environment.metal3.io",
		selection.In, []string{"baremetal"}); err == nil {

		// Init and add to selector.
		selector := labels.NewSelector()
		selector = selector.Add(*metalInstallLabel)
		if err := c.List(ctx, metalSecrets, &client.ListOptions{
			LabelSelector: selector,
		}); err == nil {
			for s := range metalSecrets.Items {
				if metalSecrets.Items[s].Namespace == "openshift-machine-api" {
					// skip secrets from openshift-machine-api ns, these hosts are not backed up
					continue
				}
				updateSecret(ctx, c, metalSecrets.Items[s],
					backupCredsClusterLabel,
					"baremetal")
			}
		}
	}
}

// set backup label for hive secrets not having the label set
func updateSecretsLabels(ctx context.Context,
	c client.Client,
	secrets corev1.SecretList,
	prefix string,
	labelName string,
	labelValue string,
) {
	logger := log.FromContext(ctx)

	for s := range secrets.Items {
		secret := secrets.Items[s]

		//exclude import secrets
		if secret.Name == secret.Namespace+"-import" {
			// remove backup label if set by previus code
			// we don't want hive import secrets to be backed up
			if secret.GetLabels()[labelName] == labelValue {
				// remove this label
				delete(secret.GetLabels(), labelName)

				msg := fmt.Sprintf("Updating secret %s in ns %s, removing label %s", secret.Name, secret.Namespace, labelName)
				logger.Info(msg)
				if err := c.Update(ctx, &secret, &client.UpdateOptions{}); err == nil {
					logger.Info(fmt.Sprintf(update_msg, secret.Name, secret.Namespace))
				}
			}
			continue
		}

		if strings.HasPrefix(secret.Name, prefix) &&
			!strings.Contains(secret.Name, "-bootstrap-") {
			updateSecret(ctx, c, secret, labelName, labelValue)
		}
	}

}

// set backup label for hive secrets not having the label set
func updateSecret(ctx context.Context,
	c client.Client,
	secret corev1.Secret,
	labelName string,
	labelValue string,
) {
	logger := log.FromContext(ctx)
	labels := secret.GetLabels()
	if labels == nil {
		labels = make(map[string]string)
	}
	if labels[backupCredsHiveLabel] == "" &&
		labels[backupCredsUserLabel] == "" &&
		labels[backupCredsClusterLabel] == "" {
		// and set backup labels for secrets
		labels[labelName] = labelValue
		secret.SetLabels(labels)
		msg := "update secret " + secret.Name
		logger.Info(msg)
		if err := c.Update(ctx, &secret, &client.UpdateOptions{}); err != nil {
			logger.Error(err, "failed to update secret")
		}
	}

}

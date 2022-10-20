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

package v1beta1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// RestorePhase contains the phase of the restore
type RestorePhase string

const (
	// RestorePhaseStarted means the restore has been initialized and started
	RestorePhaseStarted = "Started"
	// RestorePhaseRunning means the restore is running and not yet finished
	RestorePhaseRunning = "Running"
	// RestorePhaseFinished means the restore finsihed
	RestorePhaseFinished = "Finished"
	// RestorePhaseFinishedWithErrors means the restore finsihed with 1+ errors restoring individual items
	RestorePhaseFinishedWithErrors = "FinishedWithErrors"
	// RestorePhaseError means the restore is in error phase and was unable to execute
	RestorePhaseError = "Error"
	// RestorePhaseUnknown means the restore is in unknown phase
	RestorePhaseUnknown = "Unknown"
	// RestorePhaseEnabled means the restore is enabled and will continue syncing with new backups
	RestorePhaseEnabled = "Enabled"
)

type CleanupType string

const (
	// clean up only resources created as a result of a previous restore operation
	CleanupTypeRestored = "CleanupRestored"
	// don't clean up any resources
	// this can be used on a new hub where there is no need to clean up any previously created data
	CleanupTypeNone = "None"
	// clean up all resources created by CRD in the acm backup included criteria,
	// even if these resources were not created by a previous restore
	// this option cleans up all resources not available with the current restored backup,
	// including user created resources
	// Use this option with caution as this could cleanup hub or user created resources
	CleanupTypeAll = "CleanupAll"
)

// RestoreSpec defines the desired state of Restore
type RestoreSpec struct {
	// VeleroManagedClustersBackupName is the name of the velero back-up used to restore managed clusters.
	// Is required, valid values are latest, skip or backup_name
	// If value is set to latest, the latest backup is used, skip will not restore this type of backup
	// backup_name points to the name of the backup to be restored
	// +kubebuilder:validation:Required
	VeleroManagedClustersBackupName *string `json:"veleroManagedClustersBackupName"`
	// VeleroResourcesBackupName is the name of the velero back-up used to restore resources.
	// Is required, valid values are latest, skip or backup_name
	// If value is set to latest, the latest backup is used, skip will not restore this type of backup
	// backup_name points to the name of the backup to be restored
	// +kubebuilder:validation:Required
	VeleroResourcesBackupName *string `json:"veleroResourcesBackupName"`
	// VeleroCredentialsBackupName is the name of the velero back-up used to restore credentials.
	// Is required, valid values are latest, skip or backup_name
	// If value is set to latest, the latest backup is used, skip will not restore this type of backup
	// backup_name points to the name of the backup to be restored
	// +kubebuilder:validation:Required
	VeleroCredentialsBackupName *string `json:"veleroCredentialsBackupName"`
	// +kubebuilder:validation:Required
	//
	// 1. Use CleanupRestored if you want to delete all
	// resources created by a previous restore operation, before restoring the new data
	// 2. Use None if you don't want to clean up any resources before restoring the new data.
	//
	CleanupBeforeRestore CleanupType `json:"cleanupBeforeRestore"`
	// +kubebuilder:validation:Optional
	// Set this to true if you want to keep checking for new backups and restore if updates are available.
	// If not defined, the value is set to false.
	// For this option to work, you need to set VeleroResourcesBackupName and VeleroCredentialsBackupName
	// to latest and VeleroManagedClustersBackupName to skip
	SyncRestoreWithNewBackups bool `json:"syncRestoreWithNewBackups,omitempty"`
	// +kubebuilder:validation:Optional
	// Used in combination with the SyncRestoreWithNewBackups property
	// When SyncRestoreWithNewBackups is set to true, defines the duration for checking on new backups
	// If not defined and SyncRestoreWithNewBackups is set to true, it defaults to 30minutes
	RestoreSyncInterval metav1.Duration `json:"restoreSyncInterval,omitempty"`
}

// RestoreStatus defines the observed state of Restore
type RestoreStatus struct {
	// +kubebuilder:validation:Optional
	VeleroManagedClustersRestoreName string `json:"veleroManagedClustersRestoreName,omitempty"`
	// +kubebuilder:validation:Optional
	VeleroResourcesRestoreName string `json:"veleroResourcesRestoreName,omitempty"`
	// +kubebuilder:validation:Optional
	VeleroCredentialsRestoreName string `json:"veleroCredentialsRestoreName,omitempty"`
	// Phase is the current phase of the restore
	// +kubebuilder:validation:Optional
	Phase RestorePhase `json:"phase"`
	// Message on the last operation
	// +kubebuilder:validation:Optional
	LastMessage string `json:"lastMessage"`
	//Messages contains any messages that were encountered during
	// the restore process.
	Messages []string `json:"messages"`
}

// +kubebuilder:object:root=true
// +kubebuilder:validation:Optional
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName={"crst"}
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Message",type=string,JSONPath=`.status.lastMessage`

// Restore is the Schema for the restores API
type Restore struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   RestoreSpec   `json:"spec,omitempty"`
	Status RestoreStatus `json:"status,omitempty"`
}

// Restore condition type
const (
	// RestoreComplete means the restore runs to completion
	RestoreComplete = "Complete"
)

// Valid Restore Reason
const (
	RestoreReasonNotStarted = "RestoreNotStarted"
	RestoreReasonStarted    = "RestoreStarted"
	RestoreReasonRunning    = "RestoreRunning"
	RestoreReasonFinished   = "RestoreFinished"
)

//+kubebuilder:object:root=true

// RestoreList contains a list of Restore
type RestoreList struct {
	metav1.TypeMeta `          json:",inline"`
	metav1.ListMeta `          json:"metadata,omitempty"`
	Items           []Restore `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Restore{}, &RestoreList{})
}

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
	"time"

	"github.com/pkg/errors"

	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/api/core/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	kubeclient "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	v1beta1 "github.com/open-cluster-management/cluster-backup-operator/api/v1beta1"
	veleroapi "github.com/vmware-tanzu/velero/pkg/apis/velero/v1"
)

var (
	apiGVStr = v1beta1.GroupVersion.String()
	// PublicAPIServerURL the public URL for the APIServer
	PublicAPIServerURL = ""
)

const (
	restoreOwnerKey              = ".metadata.controller"
	managedClusterImportInterval = 20 * time.Second // as soon restore is finished we start to poll for managedcluster registration
	// BootstrapHubKubeconfigSecretName boostrap-hub-kubeconfig secret name
	BootstrapHubKubeconfigSecretName = "bootstrap-hub-kubeconfig" /* #nosec G101 */
	// OpenClusterManagementAgentNamespaceName namespace name for OpenClusterManagementAgent
	OpenClusterManagementAgentNamespaceName = "open-cluster-management-agent" // TODO: this can change. Get the klusterlet.spec
	// OCMManagedClusterNamespaceLabelKey OCM managedcluster namespace label key
	OCMManagedClusterNamespaceLabelKey = "cluster.open-cluster-management.io/managedCluster"
)

// GetKubeClientFromSecretFunc is the function to get kubeclient from secret
type GetKubeClientFromSecretFunc func(*corev1.Secret) (kubeclient.Interface, error)

// RestoreReconciler reconciles a Restore object
type RestoreReconciler struct {
	client.Client
	KubeClient kubernetes.Interface
	Scheme     *runtime.Scheme
	Recorder   record.EventRecorder
}

//+kubebuilder:rbac:groups=cluster.open-cluster-management.io,resources=restores,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=cluster.open-cluster-management.io,resources=restores/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=cluster.open-cluster-management.io,resources=restores/finalizers,verbs=update
//+kubebuilder:rbac:groups=velero.io,resources=backups,verbs=get;list
//+kubebuilder:rbac:groups=velero.io,resources=restores,verbs=get;list;watch;create;update
//+kubebuilder:rbac:groups="",resources=events,verbs=create;patch

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *RestoreReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	restoreLogger := log.FromContext(ctx)
	restore := &v1beta1.Restore{}

	if err := r.Get(ctx, req.NamespacedName, restore); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// retrieve the velero restore (if any)
	veleroRestoreList := veleroapi.RestoreList{}
	if err := r.List(ctx, &veleroRestoreList, client.InNamespace(req.Namespace), client.MatchingFields{restoreOwnerKey: req.Name}); err != nil {
		restoreLogger.Error(
			err,
			"unable to list velero restores for restore",
			"namespace", req.Namespace,
			"name", req.Name,
		)
		return ctrl.Result{}, err
	}

	switch {
	case len(veleroRestoreList.Items) == 0:
		if err := r.initVeleroRestores(ctx, restore); err != nil {
			restoreLogger.Error(
				err,
				"unable to initialize Velero restores for restore",
				"namespace", req.Namespace,
				"name", req.Name,
			)
			return ctrl.Result{}, err
		}

	case len(veleroRestoreList.Items) > 0:
		for i := range veleroRestoreList.Items {
			veleroRestore := veleroRestoreList.Items[i].DeepCopy()
			switch {
			case isVeleroRestoreFinished(veleroRestore):
				r.Recorder.Event(
					restore,
					v1.EventTypeNormal,
					"Velero Restore finished",
					fmt.Sprintf(
						"%s finished",
						veleroRestore.Name,
					),
				) // TODO add check on conditions to avoid multiple events
				// the restore is complete now if not a managedcluster restore type
				if !strings.Contains(
					veleroRestore.Name,
					veleroScheduleNames[ManagedClusters],
				) {
					apimeta.SetStatusCondition(&restore.Status.Conditions,
						metav1.Condition{
							Type:    v1beta1.RestoreComplete,
							Status:  metav1.ConditionTrue,
							Reason:  v1beta1.RestoreReasonFinished,
							Message: fmt.Sprintf("Restore Complete %s", veleroRestore.Name),
						})
					continue
				}
			case isVeleroRestoreRunning(veleroRestore):
				apimeta.SetStatusCondition(&restore.Status.Conditions,
					metav1.Condition{
						Type:    v1beta1.RestoreStarted,
						Status:  metav1.ConditionTrue,
						Reason:  v1beta1.RestoreReasonRunning,
						Message: fmt.Sprintf("Velero Restore %s is running", veleroRestore.Name),
					})
			default:
				apimeta.SetStatusCondition(&restore.Status.Conditions,
					metav1.Condition{
						Type:    v1beta1.RestoreStarted,
						Status:  metav1.ConditionFalse,
						Reason:  v1beta1.RestoreReasonRunning,
						Message: fmt.Sprintf("Velero Restore %s is running", veleroRestore.Name),
					})
			}
		}
	default: // (should never happen)
	}

	err := r.Client.Status().Update(ctx, restore)
	return ctrl.Result{}, errors.Wrap(
		err,
		fmt.Sprintf("could not update status for restore %s/%s", restore.Namespace, restore.Name),
	)
}

// SetupWithManager sets up the controller with the Manager.
func (r *RestoreReconciler) SetupWithManager(mgr ctrl.Manager) error {
	if err := mgr.GetFieldIndexer().IndexField(context.Background(), &veleroapi.Restore{}, restoreOwnerKey, func(rawObj client.Object) []string {
		// grab the job object, extract the owner...
		job := rawObj.(*veleroapi.Restore)
		owner := metav1.GetControllerOf(job)
		if owner == nil {
			return nil
		}
		// ..should be a Restore in Group cluster.open-cluster-management.io
		if owner.APIVersion != apiGVStr || owner.Kind != "Restore" {
			return nil
		}
		return []string{owner.Name}
	}); err != nil {
		return err
	}
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1beta1.Restore{}).
		Owns(&veleroapi.Restore{}).
		//WithOptions(controller.Options{MaxConcurrentReconciles: 3}). TODO: enable parallelism as soon attaching works
		Complete(r)
}

// mostRecentWithLessErrors defines type and code to sort velero backups according to number of errors and start timestamp
type mostRecentWithLessErrors []veleroapi.Backup

func (backups mostRecentWithLessErrors) Len() int { return len(backups) }

func (backups mostRecentWithLessErrors) Swap(i, j int) {
	backups[i], backups[j] = backups[j], backups[i]
}
func (backups mostRecentWithLessErrors) Less(i, j int) bool {
	if backups[i].Status.Errors < backups[j].Status.Errors {
		return true
	}
	if backups[i].Status.Errors > backups[j].Status.Errors {
		return false
	}
	return backups[j].Status.StartTimestamp.Before(backups[i].Status.StartTimestamp)
}

// getVeleroBackupName returns the name of velero backup will be restored
func (r *RestoreReconciler) getVeleroBackupName(
	ctx context.Context,
	restore *v1beta1.Restore,
	resourceType ResourceType,
) (string, error) {
	var veleroBackupName *string
	switch resourceType {
	case ManagedClusters:
		veleroBackupName = restore.Spec.VeleroManagedClustersBackupName
	case Credentials:
		veleroBackupName = restore.Spec.VeleroCredentialsBackupName
	case Resources:
		veleroBackupName = restore.Spec.VeleroResourcesBackupName
	}
	// TODO: check whether name is valid
	if veleroBackupName != nil && len(*veleroBackupName) > 0 {
		veleroBackup := veleroapi.Backup{}
		err := r.Get(ctx,
			types.NamespacedName{Name: *veleroBackupName,
				Namespace: restore.Namespace},
			&veleroBackup)
		if err == nil {
			return *veleroBackupName, nil
		}
		return "", fmt.Errorf("cannot find %s Velero Backup: %v",
			*veleroBackupName, err)
	}
	// backup name not available, find a proper backup
	veleroBackups := &veleroapi.BackupList{}
	if err := r.Client.List(ctx, veleroBackups, client.InNamespace(restore.Namespace)); err != nil {
		return "", fmt.Errorf("unable to list velero backups: %v", err)
	}
	if len(veleroBackups.Items) == 0 {
		return "", fmt.Errorf("no backups found")
	}
	// filter available backups to get only the ones related to this resource type
	relatedBackups := filterBackups(veleroBackups.Items, func(bkp veleroapi.Backup) bool {
		return strings.Contains(bkp.Name, veleroScheduleNames[resourceType])
	})
	if relatedBackups == nil || len(relatedBackups) == 0 {
		return "", fmt.Errorf("no backups found")
	}
	sort.Sort(mostRecentWithLessErrors(relatedBackups))
	return relatedBackups[0].Name, nil
}

// create velero.io.Restore resource for each resource type
func (r *RestoreReconciler) initVeleroRestores(
	ctx context.Context,
	restore *v1beta1.Restore,
) error {
	restoreLogger := log.FromContext(ctx)

	// loop through resourceTypes to create a Velero restore per type
	for key := range veleroScheduleNames {
		veleroRestore := &veleroapi.Restore{}
		veleroBackupName, err := r.getVeleroBackupName(ctx, restore, key)
		if err != nil {
			restoreLogger.Info(
				"backup name not found, skipping restore for",
				"name", restore.Name,
				"namespace", restore.Namespace,
				"type", key,
			)
			continue
		}
		// TODO check length of produced name
		veleroRestore.Name = restore.Name + "-" + veleroBackupName

		veleroRestore.Namespace = restore.Namespace
		veleroRestore.Spec.BackupName = veleroBackupName

		if err := ctrl.SetControllerReference(restore, veleroRestore, r.Scheme); err != nil {
			return err
		}

		if err = r.Create(ctx, veleroRestore, &client.CreateOptions{}); err != nil {
			restoreLogger.Error(
				err,
				"unable to create Velero restore for restore %s/%s",
				veleroRestore.Namespace,
				veleroRestore.Name,
			)
			return err
		}

		r.Recorder.Event(restore, v1.EventTypeNormal, "Velero restore created:", veleroRestore.Name)

		switch key {
		case ManagedClusters:
			restore.Status.VeleroManagedClustersRestoreName = veleroRestore.Name
		case Credentials:
			restore.Status.VeleroCredentialsRestoreName = veleroRestore.Name
		case Resources:
			restore.Status.VeleroResourcesRestoreName = veleroRestore.Name
		}

		apimeta.SetStatusCondition(&restore.Status.Conditions,
			metav1.Condition{
				Type:    v1beta1.RestoreStarted,
				Status:  metav1.ConditionTrue,
				Reason:  v1beta1.RestoreReasonStarted,
				Message: fmt.Sprintf("Velero restore %s started", veleroRestore.Name),
			})
	}

	return nil
}

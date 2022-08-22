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
	"path/filepath"
	"testing"
	"time"

	v1beta1 "github.com/stolostron/cluster-backup-operator/api/v1beta1"
	veleroapi "github.com/vmware-tanzu/velero/pkg/apis/velero/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes/scheme"
	clusterv1 "open-cluster-management.io/api/cluster/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"

	dynamicfake "k8s.io/client-go/dynamic/fake"
)

func Test_isVeleroRestoreFinished(t *testing.T) {
	type args struct {
		restore *veleroapi.Restore
	}
	tests := []struct {
		name string
		args args
		want bool
	}{
		{
			name: "No velero restore",
			args: args{
				restore: nil,
			},
		},
		{
			name: "Finished",
			args: args{
				restore: &veleroapi.Restore{
					Status: veleroapi.RestoreStatus{
						Phase: veleroapi.RestorePhaseCompleted,
					},
				},
			},
			want: true,
		},
		{
			name: "Not Finished",
			args: args{
				restore: &veleroapi.Restore{
					Status: veleroapi.RestoreStatus{
						Phase: veleroapi.RestorePhaseInProgress,
					},
				},
			},
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isVeleroRestoreFinished(tt.args.restore); got != tt.want {
				t.Errorf("isVeleroRestoreFinished() = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_isVeleroRestoreRunning(t *testing.T) {
	type args struct {
		restore *veleroapi.Restore
	}
	tests := []struct {
		name string
		args args
		want bool
	}{
		{
			name: "No velero restore",
			args: args{
				restore: nil,
			},
		},
		{
			name: "New velero restore",
			args: args{
				restore: &veleroapi.Restore{
					Status: veleroapi.RestoreStatus{
						Phase: veleroapi.RestorePhaseNew,
					},
				},
			},
			want: true,
		},
		{
			name: "Failed velero restore",
			args: args{
				restore: &veleroapi.Restore{
					Status: veleroapi.RestoreStatus{
						Phase: veleroapi.RestorePhaseFailed,
					},
				},
			},
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isVeleroRestoreRunning(tt.args.restore); got != tt.want {
				t.Errorf("isVeleroRestoreRunning() = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_isValidSyncOptions(t *testing.T) {
	skipRestore := "skip"
	latestBackup := "latest"
	backupName := "acm-managed-clusters-schedule-111"
	type args struct {
		restore *v1beta1.Restore
	}
	tests := []struct {
		name string
		args args
		want bool
	}{
		{
			name: "Skip all",
			args: args{
				restore: &v1beta1.Restore{
					TypeMeta: metav1.TypeMeta{
						APIVersion: "cluster.open-cluster-management.io/v1beta1",
						Kind:       "Restore",
					},
					ObjectMeta: metav1.ObjectMeta{
						Name:      "Restore",
						Namespace: "veleroNamespace",
					},
					Spec: v1beta1.RestoreSpec{
						SyncRestoreWithNewBackups:       true,
						CleanupBeforeRestore:            v1beta1.CleanupTypeNone,
						VeleroManagedClustersBackupName: &skipRestore,
						VeleroCredentialsBackupName:     &skipRestore,
						VeleroResourcesBackupName:       &skipRestore,
					},
				},
			},
			want: false,
		},
		{
			name: "No backup name",
			args: args{
				restore: &v1beta1.Restore{
					TypeMeta: metav1.TypeMeta{
						APIVersion: "cluster.open-cluster-management.io/v1beta1",
						Kind:       "Restore",
					},
					ObjectMeta: metav1.ObjectMeta{
						Name:      "Restore",
						Namespace: "veleroNamespace",
					},
					Spec: v1beta1.RestoreSpec{
						SyncRestoreWithNewBackups: true,
						CleanupBeforeRestore:      v1beta1.CleanupTypeNone,
					},
				},
			},
			want: false,
		},
		{
			name: "Credentials should be set to skip or latest",
			args: args{
				restore: &v1beta1.Restore{
					TypeMeta: metav1.TypeMeta{
						APIVersion: "cluster.open-cluster-management.io/v1beta1",
						Kind:       "Restore",
					},
					ObjectMeta: metav1.ObjectMeta{
						Name:      "Restore",
						Namespace: "veleroNamespace",
					},
					Spec: v1beta1.RestoreSpec{
						SyncRestoreWithNewBackups:       true,
						CleanupBeforeRestore:            v1beta1.CleanupTypeAll,
						VeleroManagedClustersBackupName: &skipRestore,
						VeleroCredentialsBackupName:     &backupName,
						VeleroResourcesBackupName:       &latestBackup,
					},
				},
			},
			want: false,
		},
		{
			name: "Resources should be set to latest",
			args: args{
				restore: &v1beta1.Restore{
					TypeMeta: metav1.TypeMeta{
						APIVersion: "cluster.open-cluster-management.io/v1beta1",
						Kind:       "Restore",
					},
					ObjectMeta: metav1.ObjectMeta{
						Name:      "Restore",
						Namespace: "veleroNamespace",
					},
					Spec: v1beta1.RestoreSpec{
						SyncRestoreWithNewBackups:       true,
						CleanupBeforeRestore:            v1beta1.CleanupTypeAll,
						VeleroManagedClustersBackupName: &skipRestore,
						VeleroCredentialsBackupName:     &latestBackup,
						VeleroResourcesBackupName:       &skipRestore,
					},
				},
			},
			want: false,
		},
		{
			name: "InValid config, no sync",
			args: args{
				restore: &v1beta1.Restore{
					TypeMeta: metav1.TypeMeta{
						APIVersion: "cluster.open-cluster-management.io/v1beta1",
						Kind:       "Restore",
					},
					ObjectMeta: metav1.ObjectMeta{
						Name:      "Restore",
						Namespace: "veleroNamespace",
					},
					Spec: v1beta1.RestoreSpec{
						CleanupBeforeRestore:            v1beta1.CleanupTypeAll,
						VeleroManagedClustersBackupName: &skipRestore,
						VeleroCredentialsBackupName:     &latestBackup,
						VeleroResourcesBackupName:       &latestBackup,
					},
				},
			},
			want: false,
		},
		{
			name: "Valid config",
			args: args{
				restore: &v1beta1.Restore{
					TypeMeta: metav1.TypeMeta{
						APIVersion: "cluster.open-cluster-management.io/v1beta1",
						Kind:       "Restore",
					},
					ObjectMeta: metav1.ObjectMeta{
						Name:      "Restore",
						Namespace: "veleroNamespace",
					},
					Spec: v1beta1.RestoreSpec{
						SyncRestoreWithNewBackups:       true,
						CleanupBeforeRestore:            v1beta1.CleanupTypeAll,
						VeleroManagedClustersBackupName: &skipRestore,
						VeleroCredentialsBackupName:     &latestBackup,
						VeleroResourcesBackupName:       &latestBackup,
					},
				},
			},
			want: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got, msg := isValidSyncOptions(tt.args.restore); got != tt.want {
				t.Errorf("failed test %s isValidSyncOptions() = %v, want %v, message: %s", tt.name, got, tt.want, msg)
			}
		})
	}
}

func Test_isSkipAllRestores(t *testing.T) {
	skipRestore := "skip"
	latestBackup := "latest"
	type args struct {
		restore *v1beta1.Restore
	}
	tests := []struct {
		name string
		args args
		want bool
	}{
		{
			name: "Skip all",
			args: args{
				restore: &v1beta1.Restore{
					TypeMeta: metav1.TypeMeta{
						APIVersion: "cluster.open-cluster-management.io/v1beta1",
						Kind:       "Restore",
					},
					ObjectMeta: metav1.ObjectMeta{
						Name:      "Restore",
						Namespace: "veleroNamespace",
					},
					Spec: v1beta1.RestoreSpec{
						CleanupBeforeRestore:            v1beta1.CleanupTypeNone,
						VeleroManagedClustersBackupName: &skipRestore,
						VeleroCredentialsBackupName:     &skipRestore,
						VeleroResourcesBackupName:       &skipRestore,
					},
				},
			},
			want: true,
		},
		{
			name: "No backup name",
			args: args{
				restore: &v1beta1.Restore{
					TypeMeta: metav1.TypeMeta{
						APIVersion: "cluster.open-cluster-management.io/v1beta1",
						Kind:       "Restore",
					},
					ObjectMeta: metav1.ObjectMeta{
						Name:      "Restore",
						Namespace: "veleroNamespace",
					},
				},
			},
			want: true,
		},
		{
			name: "Do not skip all",
			args: args{
				restore: &v1beta1.Restore{
					TypeMeta: metav1.TypeMeta{
						APIVersion: "cluster.open-cluster-management.io/v1beta1",
						Kind:       "Restore",
					},
					ObjectMeta: metav1.ObjectMeta{
						Name:      "Restore",
						Namespace: "veleroNamespace",
					},
					Spec: v1beta1.RestoreSpec{
						CleanupBeforeRestore:            v1beta1.CleanupTypeAll,
						VeleroManagedClustersBackupName: &skipRestore,
						VeleroCredentialsBackupName:     &latestBackup,
						VeleroResourcesBackupName:       &latestBackup,
					},
				},
			},
			want: false,
		},
		{
			name: "Managed clusters name is not skip",
			args: args{
				restore: &v1beta1.Restore{
					TypeMeta: metav1.TypeMeta{
						APIVersion: "cluster.open-cluster-management.io/v1beta1",
						Kind:       "Restore",
					},
					ObjectMeta: metav1.ObjectMeta{
						Name:      "Restore",
						Namespace: "veleroNamespace",
					},
					Spec: v1beta1.RestoreSpec{
						CleanupBeforeRestore:            v1beta1.CleanupTypeAll,
						VeleroManagedClustersBackupName: &latestBackup,
						VeleroCredentialsBackupName:     &latestBackup,
						VeleroResourcesBackupName:       &latestBackup,
					},
				},
			},
			want: false,
		},
		{
			name: "Resources is not skip",
			args: args{
				restore: &v1beta1.Restore{
					TypeMeta: metav1.TypeMeta{
						APIVersion: "cluster.open-cluster-management.io/v1beta1",
						Kind:       "Restore",
					},
					ObjectMeta: metav1.ObjectMeta{
						Name:      "Restore",
						Namespace: "veleroNamespace",
					},
					Spec: v1beta1.RestoreSpec{
						CleanupBeforeRestore:            v1beta1.CleanupTypeNone,
						VeleroManagedClustersBackupName: &skipRestore,
						VeleroCredentialsBackupName:     &skipRestore,
						VeleroResourcesBackupName:       &latestBackup,
					},
				},
			},
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isSkipAllRestores(tt.args.restore); got != tt.want {
				t.Errorf("isSkipAllRestores() = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_deleteDynamicResource(t *testing.T) {

	res_local_ns := &unstructured.Unstructured{}
	res_local_ns.SetUnstructuredContent(map[string]interface{}{
		"apiVersion": "apps.open-cluster-management.io/v1",
		"kind":       "Channel",
		"metadata": map[string]interface{}{
			"name":      "channel-new",
			"namespace": "local-cluster",
		},
		"spec": map[string]interface{}{
			"type":     "Git",
			"pathname": "https://github.com/test/app-samples",
		},
	})

	res_default := &unstructured.Unstructured{}
	res_default.SetUnstructuredContent(map[string]interface{}{
		"apiVersion": "apps.open-cluster-management.io/v1",
		"kind":       "Channel",
		"metadata": map[string]interface{}{
			"name":      "channel-new",
			"namespace": "default",
		},
		"spec": map[string]interface{}{
			"type":     "Git",
			"pathname": "https://github.com/test/app-samples",
		},
	})

	res_exclude_from_backup := &unstructured.Unstructured{}
	res_exclude_from_backup.SetUnstructuredContent(map[string]interface{}{
		"apiVersion": "apps.open-cluster-management.io/v1",
		"kind":       "Channel",
		"metadata": map[string]interface{}{
			"name":      "channel-new",
			"namespace": "default",
			"labels": map[string]interface{}{
				"velero.io/exclude-from-backup": "true",
			},
		},
		"spec": map[string]interface{}{
			"type":     "Git",
			"pathname": "https://github.com/test/app-samples",
		},
	})

	res_global := &unstructured.Unstructured{}
	res_global.SetUnstructuredContent(map[string]interface{}{
		"apiVersion": "apps.open-cluster-management.io/v1",
		"kind":       "Channel",
		"metadata": map[string]interface{}{
			"name": "channel-new",
		},
		"spec": map[string]interface{}{
			"type":     "Git",
			"pathname": "https://github.com/test/app-samples",
		},
	})

	dynClient := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(), res_local_ns)

	targetGVK := schema.GroupVersionKind{Group: "apps.open-cluster-management.io", Version: "v1", Kind: "Channel"}
	targetGVR := targetGVK.GroupVersion().WithResource("somecrs")
	targetMapping := meta.RESTMapping{Resource: targetGVR, GroupVersionKind: targetGVK,
		Scope: meta.RESTScopeNamespace}

	targetMappingGlobal := meta.RESTMapping{Resource: targetGVR, GroupVersionKind: targetGVK,
		Scope: meta.RESTScopeRoot}

	var monboDBResource = schema.GroupVersionResource{Group: "apps.open-cluster-management.io", Version: "v1", Resource: "channel"}

	resInterface := dynClient.Resource(monboDBResource)

	deletePolicy := metav1.DeletePropagationForeground
	delOptions := metav1.DeleteOptions{
		PropagationPolicy: &deletePolicy,
	}

	type args struct {
		ctx                context.Context
		mapping            *meta.RESTMapping
		dr                 dynamic.NamespaceableResourceInterface
		resource           unstructured.Unstructured
		deleteOptions      v1.DeleteOptions
		excludedNamespaces []string
	}
	tests := []struct {
		name string
		args args
		want bool
	}{
		{
			name: "Delete local cluster resource",
			args: args{
				ctx:                context.Background(),
				mapping:            &targetMapping,
				dr:                 resInterface,
				resource:           *res_local_ns,
				deleteOptions:      delOptions,
				excludedNamespaces: []string{"abc"},
			},
			want: false,
		},
		{
			name: "Delete default resource",
			args: args{
				ctx:                context.Background(),
				mapping:            &targetMapping,
				dr:                 resInterface,
				resource:           *res_default,
				deleteOptions:      delOptions,
				excludedNamespaces: []string{"abc"},
			},
			want: true,
		},
		{
			name: "Delete default resource with ns excluded",
			args: args{
				ctx:                context.Background(),
				mapping:            &targetMapping,
				dr:                 resInterface,
				resource:           *res_default,
				deleteOptions:      delOptions,
				excludedNamespaces: []string{"default"},
			},
			want: false,
		},
		{
			name: "Delete default resource, excluded from backup",
			args: args{
				ctx:                context.Background(),
				mapping:            &targetMapping,
				dr:                 resInterface,
				resource:           *res_exclude_from_backup,
				deleteOptions:      delOptions,
				excludedNamespaces: []string{"abc"},
			},
			want: false,
		},
		{
			name: "Delete global resource",
			args: args{
				ctx:                context.Background(),
				mapping:            &targetMappingGlobal,
				dr:                 resInterface,
				resource:           *res_global,
				deleteOptions:      delOptions,
				excludedNamespaces: []string{},
			},
			want: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, got := deleteDynamicResource(tt.args.ctx,
				tt.args.mapping,
				tt.args.dr,
				tt.args.resource,
				tt.args.deleteOptions,
				tt.args.excludedNamespaces); got != tt.want {
				t.Errorf("deleteDynamicResource() = %v, want %v", got, tt.want)
			}
		})
	}

}

func Test_sendResults(t *testing.T) {
	skipRestore := "skip"
	type args struct {
		restore *v1beta1.Restore
		err     error
	}
	tests := []struct {
		name string
		args args
		want error
	}{
		{
			name: "Try restore again",
			args: args{
				restore: &v1beta1.Restore{
					TypeMeta: metav1.TypeMeta{
						APIVersion: "cluster.open-cluster-management.io/v1beta1",
						Kind:       "Restore",
					},
					ObjectMeta: metav1.ObjectMeta{
						Name:      "Restore",
						Namespace: "veleroNamespace",
					},
					Spec: v1beta1.RestoreSpec{
						SyncRestoreWithNewBackups:       true,
						RestoreSyncInterval:             v1.Duration{Duration: time.Minute * 15},
						CleanupBeforeRestore:            v1beta1.CleanupTypeNone,
						VeleroManagedClustersBackupName: &skipRestore,
						VeleroCredentialsBackupName:     &skipRestore,
						VeleroResourcesBackupName:       &skipRestore,
					},
					Status: v1beta1.RestoreStatus{
						Phase: v1beta1.RestorePhaseEnabled,
					},
				},
				err: nil,
			},
			want: nil,
		},
		{
			name: "Skip restore again",
			args: args{
				restore: &v1beta1.Restore{
					TypeMeta: metav1.TypeMeta{
						APIVersion: "cluster.open-cluster-management.io/v1beta1",
						Kind:       "Restore",
					},
					ObjectMeta: metav1.ObjectMeta{
						Name:      "Restore",
						Namespace: "veleroNamespace",
					},
					Spec: v1beta1.RestoreSpec{
						SyncRestoreWithNewBackups:       true,
						CleanupBeforeRestore:            v1beta1.CleanupTypeNone,
						VeleroManagedClustersBackupName: &skipRestore,
						VeleroCredentialsBackupName:     &skipRestore,
						VeleroResourcesBackupName:       &skipRestore,
					},
					Status: v1beta1.RestoreStatus{
						Phase: v1beta1.RestorePhaseFinished,
					},
				},
				err: nil,
			},
			want: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := sendResult(tt.args.restore, tt.args.err); err != tt.want {
				t.Errorf("isSkipAllRestores() = %v, want %v", err, tt.want)
			}
		})
	}
}

func Test_setRestorePhase(t *testing.T) {
	skipRestore := "skip"
	latestBackupStr := "latest"
	type args struct {
		restore     *v1beta1.Restore
		restoreList *veleroapi.RestoreList
	}
	tests := []struct {
		name string
		args args
		want v1beta1.RestorePhase
	}{
		{
			name: "Restore list empty and skip all, return finished phase",
			args: args{
				restore: &v1beta1.Restore{
					TypeMeta: metav1.TypeMeta{
						APIVersion: "cluster.open-cluster-management.io/v1beta1",
						Kind:       "Restore",
					},
					ObjectMeta: metav1.ObjectMeta{
						Name:      "Restore",
						Namespace: "veleroNamespace",
					},
					Spec: v1beta1.RestoreSpec{
						SyncRestoreWithNewBackups:       true,
						RestoreSyncInterval:             v1.Duration{Duration: time.Minute * 15},
						CleanupBeforeRestore:            v1beta1.CleanupTypeNone,
						VeleroManagedClustersBackupName: &skipRestore,
						VeleroCredentialsBackupName:     &skipRestore,
						VeleroResourcesBackupName:       &skipRestore,
					},
					Status: v1beta1.RestoreStatus{
						Phase: v1beta1.RestorePhaseRunning,
					},
				},
				restoreList: nil,
			},
			want: v1beta1.RestorePhaseFinished,
		},
		{
			name: "Restore list empty and NOT skip all, return finished RestorePhaseStarted",
			args: args{
				restore: &v1beta1.Restore{
					TypeMeta: metav1.TypeMeta{
						APIVersion: "cluster.open-cluster-management.io/v1beta1",
						Kind:       "Restore",
					},
					ObjectMeta: metav1.ObjectMeta{
						Name:      "Restore",
						Namespace: "veleroNamespace",
					},
					Spec: v1beta1.RestoreSpec{
						SyncRestoreWithNewBackups:       true,
						RestoreSyncInterval:             v1.Duration{Duration: time.Minute * 15},
						CleanupBeforeRestore:            v1beta1.CleanupTypeNone,
						VeleroManagedClustersBackupName: &latestBackupStr,
						VeleroCredentialsBackupName:     &skipRestore,
						VeleroResourcesBackupName:       &skipRestore,
					},
					Status: v1beta1.RestoreStatus{
						Phase: v1beta1.RestorePhaseRunning,
					},
				},
				restoreList: nil,
			},
			want: v1beta1.RestorePhaseStarted,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if phase := setRestorePhase(tt.args.restoreList, tt.args.restore); phase != tt.want {
				t.Errorf("setRestorePhase() = %v, want %v", phase, tt.want)
			}
		})
	}
}

func Test_postRestoreActivation(t *testing.T) {

	testEnv := &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("..", "config", "crd", "bases")},
		ErrorIfCRDPathMissing: true,
	}

	autoImporSecret := corev1.Secret{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "Secret",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      autoImportSecretName,
			Namespace: "managed1",
			Labels:    map[string]string{activateLabel: "true"},
		},
	}
	cfg, _ := testEnv.Start()
	k8sClient1, _ := client.New(cfg, client.Options{Scheme: scheme.Scheme})
	k8sClient1.Create(context.Background(), &autoImporSecret)

	fourHoursAgo := "2022-07-26T11:25:34Z"
	nextTenHours := "2022-07-27T04:25:34Z"

	current, _ := time.Parse(time.RFC3339, "2022-07-26T15:25:34Z")

	type args struct {
		ctx             context.Context
		secrets         []corev1.Secret
		managedClusters []clusterv1.ManagedCluster
		currentTime     time.Time
	}
	tests := []struct {
		name string
		args args
		want []string
	}{
		{
			name: "create NO auto import secrets, managed1 is active",
			args: args{
				ctx:         context.Background(),
				currentTime: current,
				managedClusters: []clusterv1.ManagedCluster{
					{
						TypeMeta: metav1.TypeMeta{
							APIVersion: "cluster.open-cluster-management.io/v1",
							Kind:       "ManagedCluster",
						},
						ObjectMeta: metav1.ObjectMeta{
							Name: "local-cluster",
						},
						Spec: clusterv1.ManagedClusterSpec{
							HubAcceptsClient: true,
						},
					},
					{
						TypeMeta: metav1.TypeMeta{
							APIVersion: "cluster.open-cluster-management.io/v1",
							Kind:       "ManagedCluster",
						},
						ObjectMeta: metav1.ObjectMeta{
							Name: "test1",
						},
						Spec: clusterv1.ManagedClusterSpec{
							HubAcceptsClient: true,
						},
					},
					{
						TypeMeta: metav1.TypeMeta{
							APIVersion: "cluster.open-cluster-management.io/v1",
							Kind:       "ManagedCluster",
						},
						ObjectMeta: metav1.ObjectMeta{
							Name: "managed1",
						},
						Spec: clusterv1.ManagedClusterSpec{
							HubAcceptsClient: true,
							ManagedClusterClientConfigs: []clusterv1.ClientConfig{
								clusterv1.ClientConfig{
									URL: "someurl",
								},
							},
						},
						Status: clusterv1.ManagedClusterStatus{
							Conditions: []metav1.Condition{
								v1.Condition{
									Status: v1.ConditionTrue,
									Type:   "ManagedClusterConditionAvailable",
								},
							},
						},
					},
				},
				secrets: []corev1.Secret{
					corev1.Secret{
						TypeMeta: metav1.TypeMeta{
							APIVersion: "v1",
							Kind:       "Secret",
						},
						ObjectMeta: metav1.ObjectMeta{
							Name:      "auto-import",
							Namespace: "local-cluster",
							Annotations: map[string]string{
								"lastRefreshTimestamp": fourHoursAgo,
								"expirationTimestamp":  nextTenHours,
							},
						},
					},
					corev1.Secret{
						TypeMeta: metav1.TypeMeta{
							APIVersion: "v1",
							Kind:       "Secret",
						},
						ObjectMeta: metav1.ObjectMeta{
							Name:      "auto-import",
							Namespace: "managed1",
							Annotations: map[string]string{
								"lastRefreshTimestamp": fourHoursAgo,
								"expirationTimestamp":  nextTenHours,
							},
						},
						Data: map[string][]byte{
							"token": []byte("YWRtaW4="),
						},
					},
				}},
			want: []string{},
		},
		{
			name: "create NO auto import secret for managed1, it has no URL",
			args: args{
				ctx:         context.Background(),
				currentTime: current,
				managedClusters: []clusterv1.ManagedCluster{
					{
						TypeMeta: metav1.TypeMeta{
							APIVersion: "cluster.open-cluster-management.io/v1",
							Kind:       "ManagedCluster",
						},
						ObjectMeta: metav1.ObjectMeta{
							Name: "local-cluster",
						},
						Spec: clusterv1.ManagedClusterSpec{
							HubAcceptsClient: true,
						},
					},
					{
						TypeMeta: metav1.TypeMeta{
							APIVersion: "cluster.open-cluster-management.io/v1",
							Kind:       "ManagedCluster",
						},
						ObjectMeta: metav1.ObjectMeta{
							Name: "test1",
						},
						Spec: clusterv1.ManagedClusterSpec{
							HubAcceptsClient: true,
						},
					},
					{
						TypeMeta: metav1.TypeMeta{
							APIVersion: "cluster.open-cluster-management.io/v1",
							Kind:       "ManagedCluster",
						},
						ObjectMeta: metav1.ObjectMeta{
							Name: "managed1",
						},
						Spec: clusterv1.ManagedClusterSpec{
							HubAcceptsClient: true,
							ManagedClusterClientConfigs: []clusterv1.ClientConfig{
								clusterv1.ClientConfig{},
							},
						},
						Status: clusterv1.ManagedClusterStatus{
							Conditions: []metav1.Condition{
								v1.Condition{
									Status: v1.ConditionFalse,
								},
							},
						},
					},
				},
				secrets: []corev1.Secret{
					corev1.Secret{
						TypeMeta: metav1.TypeMeta{
							APIVersion: "v1",
							Kind:       "Secret",
						},
						ObjectMeta: metav1.ObjectMeta{
							Name:      "auto-import",
							Namespace: "local-cluster",
							Annotations: map[string]string{
								"lastRefreshTimestamp": fourHoursAgo,
								"expirationTimestamp":  nextTenHours,
							},
						},
					},
					corev1.Secret{
						TypeMeta: metav1.TypeMeta{
							APIVersion: "v1",
							Kind:       "Secret",
						},
						ObjectMeta: metav1.ObjectMeta{
							Name:      "auto-import",
							Namespace: "managed1",
							Annotations: map[string]string{
								"lastRefreshTimestamp": fourHoursAgo,
								"expirationTimestamp":  nextTenHours,
							},
						},
						Data: map[string][]byte{
							"token": []byte("YWRtaW4="),
						},
					},
					corev1.Secret{
						TypeMeta: metav1.TypeMeta{
							APIVersion: "v1",
							Kind:       "Secret",
						},
						ObjectMeta: metav1.ObjectMeta{
							Name:      "auto-import",
							Namespace: "managed2",
							Annotations: map[string]string{
								"lastRefreshTimestamp": fourHoursAgo,
								"expirationTimestamp":  nextTenHours,
							},
						},
						Data: map[string][]byte{
							"token1": []byte("aaa"), // test invalid token for managed2 ns
						},
					},
					corev1.Secret{
						TypeMeta: metav1.TypeMeta{
							APIVersion: "v1",
							Kind:       "Secret",
						},
						ObjectMeta: metav1.ObjectMeta{
							Name:      "auto-import-pair", // this should be skipped
							Namespace: "managed1",
							Annotations: map[string]string{
								"lastRefreshTimestamp": fourHoursAgo,
								"expirationTimestamp":  nextTenHours,
							},
						},
						Data: map[string][]byte{
							"token": []byte("YWRtaW4="),
						},
					},
				}},
			want: []string{},
		},
		{
			name: "create auto import for managed1 cluster",
			args: args{
				ctx:         context.Background(),
				currentTime: current,
				managedClusters: []clusterv1.ManagedCluster{
					{
						TypeMeta: metav1.TypeMeta{
							APIVersion: "cluster.open-cluster-management.io/v1",
							Kind:       "ManagedCluster",
						},
						ObjectMeta: metav1.ObjectMeta{
							Name: "local-cluster",
						},
						Spec: clusterv1.ManagedClusterSpec{
							HubAcceptsClient: true,
						},
					},
					{
						TypeMeta: metav1.TypeMeta{
							APIVersion: "cluster.open-cluster-management.io/v1",
							Kind:       "ManagedCluster",
						},
						ObjectMeta: metav1.ObjectMeta{
							Name: "test1",
						},
						Spec: clusterv1.ManagedClusterSpec{
							HubAcceptsClient: true,
						},
					},
					{
						TypeMeta: metav1.TypeMeta{
							APIVersion: "cluster.open-cluster-management.io/v1",
							Kind:       "ManagedCluster",
						},
						ObjectMeta: metav1.ObjectMeta{
							Name: "managed1",
						},
						Spec: clusterv1.ManagedClusterSpec{
							HubAcceptsClient: true,
							ManagedClusterClientConfigs: []clusterv1.ClientConfig{
								clusterv1.ClientConfig{
									URL: "someurl",
								},
							},
						},
						Status: clusterv1.ManagedClusterStatus{
							Conditions: []metav1.Condition{
								v1.Condition{
									Status: v1.ConditionFalse,
								},
							},
						},
					},
				},
				secrets: []corev1.Secret{
					corev1.Secret{
						TypeMeta: metav1.TypeMeta{
							APIVersion: "v1",
							Kind:       "Secret",
						},
						ObjectMeta: metav1.ObjectMeta{
							Name:      "auto-import",
							Namespace: "local-cluster",
							Annotations: map[string]string{
								"lastRefreshTimestamp": fourHoursAgo,
								"expirationTimestamp":  nextTenHours,
							},
						},
					},
					corev1.Secret{
						TypeMeta: metav1.TypeMeta{
							APIVersion: "v1",
							Kind:       "Secret",
						},
						ObjectMeta: metav1.ObjectMeta{
							Name:      "auto-import",
							Namespace: "managed1",
							Annotations: map[string]string{
								"lastRefreshTimestamp": fourHoursAgo,
								"expirationTimestamp":  nextTenHours,
							},
						},
						Data: map[string][]byte{
							"token": []byte("YWRtaW4="),
						},
					},
				}},
			want: []string{"managed1"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := postRestoreActivation(tt.args.ctx, k8sClient1,
				tt.args.secrets, tt.args.managedClusters, tt.args.currentTime); len(got) != len(tt.want) {
				t.Errorf("postRestoreActivation() returns = %v, want %v", got, tt.want)
			}
		})
	}

}

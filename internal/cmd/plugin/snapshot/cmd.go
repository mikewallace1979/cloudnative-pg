/*
Copyright The CloudNativePG Contributors

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

package snapshot

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	apiv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	"github.com/cloudnative-pg/cloudnative-pg/internal/cmd/plugin"
	"github.com/cloudnative-pg/cloudnative-pg/internal/plugin/resources"
	"github.com/cloudnative-pg/cloudnative-pg/pkg/reconciler/persistentvolumeclaim"
	"github.com/cloudnative-pg/cloudnative-pg/pkg/utils/snapshot"
)

// NewCmd implements the `snapshot` subcommand
func NewCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "snapshot <cluster-name>",
		Short: "Take a snapshot of a CloudNativePG cluster",
		Long: `This command will take a snapshot of an existing CNPG cluster as a set of VolumeSnapshot
resources. The created resources can be used later to create a new CloudNativePG cluster
containing the snapshotted data.

The command will:

1. Select a PostgreSQL replica Pod and fence it
2. Take a snapshot of the PVCs
3. Unfence the replica Pod

Fencing the Pod will result in a temporary out-of-service of the selected replica.
The other replicas will continue working.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			clusterName := args[0]

			snapshotClassName, _ := cmd.Flags().GetString("volume-snapshot-class-name")
			snapshotNameSuffix, _ := cmd.Flags().GetString("volume-snapshot-suffix")

			return execute(cmd.Context(), clusterName, snapshotClassName, snapshotNameSuffix)
		},
	}

	cmd.Flags().StringP(
		"volume-snapshot-class-name",
		"c",
		"",
		`The VolumeSnapshotClass name to be used for the snapshot
(defaults to empty, which will make use of the default VolumeSnapshotClass)`)

	cmd.Flags().StringP("volume-snapshot-suffix",
		"x",
		"",
		"Specifies the suffix of the created volume snapshot. Optional. "+
			"Defaults to the snapshot time expressed as unix timestamp",
	)

	return cmd
}

// execute creates the snapshot command
func execute(
	ctx context.Context,
	clusterName string,
	snapshotClassName string,
	snapshotSuffix string,
) error {
	// Get the Cluster object
	var cluster apiv1.Cluster
	err := plugin.Client.Get(
		ctx,
		client.ObjectKey{Namespace: plugin.Namespace, Name: clusterName},
		&cluster)
	if err != nil {
		return fmt.Errorf("could not get cluster: %v", err)
	}

	// Get the target Pod
	managedInstances, primaryInstance, err := resources.GetInstancePods(ctx, clusterName)
	if err != nil {
		return fmt.Errorf("could not get cluster pods: %w", err)
	}
	if primaryInstance.Name == "" {
		return fmt.Errorf("no primary instance found, cannot proceed")
	}

	var targetPod *corev1.Pod
	// Get the replica Pod to be fenced
	for i := len(managedInstances) - 1; i >= 0; i-- {
		if managedInstances[i].Name != primaryInstance.Name {
			targetPod = managedInstances[i].DeepCopy()
			break
		}
	}

	if targetPod == nil {
		return fmt.Errorf("no replicas found, cannot proceed")
	}

	// Get the PVCs that will be snapshotted
	pvcs, err := persistentvolumeclaim.GetInstancePVCs(ctx, plugin.Client, targetPod.Name, plugin.Namespace)
	if err != nil {
		return fmt.Errorf("cannot get PVCs: %w", err)
	}

	executor := snapshot.NewExecutorBuilder(plugin.Client, apiv1.VolumeSnapshotConfiguration{
		ClassName:              snapshotClassName,
		SnapshotOwnerReference: "none",
	}).
		FenceInstance(true).
		WithSnapshotSuffix(snapshotSuffix).
		Build()

	_, err = executor.Execute(ctx, &cluster, targetPod, pvcs)
	return err
}

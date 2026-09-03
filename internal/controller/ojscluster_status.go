package controller

import (
	"context"
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	ojsv1alpha1 "github.com/openjobspec/ojs-k8s-operator/api/v1alpha1"
)

// OJSCluster condition type and reason constants. The values are shared with
// OJSWorker (see condWorker* aliases in status.go) since both kinds expose
// the same Ready/Available/Progressing/Degraded condition vocabulary.
const (
	condReady       = "Ready"
	condAvailable   = "Available"
	condProgressing = "Progressing"
	condDegraded    = "Degraded"

	// condBackend is OJSCluster-specific: it reports embedded backend health
	// and has no OJSWorker equivalent.
	condBackend = "BackendReady"
)

// clusterStatusMessages holds the OJSCluster-specific wording for the shared
// deployment status transition policy in status.go.
var clusterStatusMessages = deploymentStatusMessages{
	notFoundMsg:            "Server deployment does not exist yet",
	notApplicableMsg:       "Cluster is still initializing",
	allReadyMsg:            "All server replicas are ready",
	noReplicasReadyMsg:     "No replicas are ready yet",
	noReplicasAvailableMsg: "No replicas are available",
	stillInitializingMsg:   "Cluster is still starting up",
}

// updateStatus refreshes cluster.Status from the current state of the
// server Deployment and persists it, recording a PhaseChanged event on
// transition.
func (r *OJSClusterReconciler) updateStatus(ctx context.Context, cluster *ojsv1alpha1.OJSCluster) error {
	previousPhase := cluster.Status.Phase

	dep := &appsv1.Deployment{}
	found := true
	if err := r.Get(ctx, types.NamespacedName{Name: cluster.Name + "-server", Namespace: cluster.Namespace}, dep); err != nil {
		if !errors.IsNotFound(err) {
			return err
		}
		found = false
	}

	cluster.Status.Phase = applyDeploymentStatus(&cluster.Status.Conditions, cluster.Generation, dep, found, clusterStatusMessages)
	if found {
		cluster.Status.Replicas = dep.Status.Replicas
		cluster.Status.ReadyReplicas = dep.Status.ReadyReplicas
	}

	if previousPhase != "" && previousPhase != cluster.Status.Phase {
		r.recordEvent(cluster, corev1.EventTypeNormal, "PhaseChanged",
			fmt.Sprintf("Cluster transitioned from %s to %s", previousPhase, cluster.Status.Phase))
	}

	return r.Status().Update(ctx, cluster)
}

func (r *OJSClusterReconciler) setCondition(cluster *ojsv1alpha1.OJSCluster, condType string, status metav1.ConditionStatus, reason, message string) {
	meta.SetStatusCondition(&cluster.Status.Conditions, metav1.Condition{
		Type:               condType,
		Status:             status,
		Reason:             reason,
		Message:            message,
		ObservedGeneration: cluster.Generation,
	})
}

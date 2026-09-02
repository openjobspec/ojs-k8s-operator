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

// OJSWorker condition type aliases. These share the exact same string values
// as OJSCluster's condition types (defined in ojscluster_status.go) since
// both kinds expose the same Ready/Available/Progressing/Degraded
// vocabulary; kind-specific names are kept for readability at call sites and
// to preserve the historical test/API surface.
const (
	condWorkerReady    = condReady
	condWorkerAvail    = condAvailable
	condWorkerProgress = condProgressing
	condWorkerDegraded = condDegraded
)

// workerStatusMessages holds the OJSWorker-specific wording for the shared
// deployment status transition policy in status.go.
var workerStatusMessages = deploymentStatusMessages{
	notFoundMsg:            "Worker deployment does not exist yet",
	notApplicableMsg:       "Worker is still initializing",
	allReadyMsg:            "All worker replicas are ready",
	noReplicasReadyMsg:     "No worker replicas are ready yet",
	noReplicasAvailableMsg: "No worker replicas are available",
	stillInitializingMsg:   "Worker is still starting up",
}

// updateWorkerStatus refreshes worker.Status from the current state of the
// worker Deployment and persists it, recording a PhaseChanged event on
// transition.
func (r *OJSWorkerReconciler) updateWorkerStatus(ctx context.Context, worker *ojsv1alpha1.OJSWorker) error {
	previousPhase := worker.Status.Phase

	dep := &appsv1.Deployment{}
	found := true
	if err := r.Get(ctx, types.NamespacedName{Name: worker.Name, Namespace: worker.Namespace}, dep); err != nil {
		if !errors.IsNotFound(err) {
			return err
		}
		found = false
	}

	worker.Status.Phase = applyDeploymentStatus(&worker.Status.Conditions, worker.Generation, dep, found, workerStatusMessages)
	if found {
		worker.Status.Replicas = dep.Status.Replicas
		worker.Status.ReadyReplicas = dep.Status.ReadyReplicas
	}

	if previousPhase != "" && previousPhase != worker.Status.Phase {
		r.recordEvent(worker, corev1.EventTypeNormal, "PhaseChanged",
			fmt.Sprintf("Worker transitioned from %s to %s", previousPhase, worker.Status.Phase))
	}

	return r.Status().Update(ctx, worker)
}

func (r *OJSWorkerReconciler) setWorkerCondition(worker *ojsv1alpha1.OJSWorker, condType string, status metav1.ConditionStatus, reason, message string) {
	meta.SetStatusCondition(&worker.Status.Conditions, metav1.Condition{
		Type:               condType,
		Status:             status,
		Reason:             reason,
		Message:            message,
		ObservedGeneration: worker.Generation,
	})
}

package controller

import (
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// deploymentStatusMessages carries the exact, historically-preserved
// condition message text for a given custom resource kind (OJSCluster vs
// OJSWorker). The transition logic itself -- phase decisions, condition
// ordering, reasons, and ObservedGeneration handling -- is identical between
// the two controllers and lives in applyDeploymentStatus below; only the
// human-readable wording differs per kind, so it is supplied by the caller.
type deploymentStatusMessages struct {
	notFoundMsg            string
	notApplicableMsg       string
	allReadyMsg            string
	noReplicasReadyMsg     string
	noReplicasAvailableMsg string
	stillInitializingMsg   string
}

// applyDeploymentStatus derives a phase ("Pending"/"Running"/"Scaling") and
// sets the four standard status conditions (Ready, Available, Progressing,
// Degraded) based on a child Deployment's rollout status.
//
// This captures the condition/status transition policy that was previously
// duplicated verbatim between OJSClusterReconciler.updateStatus and
// OJSWorkerReconciler.updateWorkerStatus: both controllers observed the exact
// same phase decisions, condition order, reasons, and generation handling,
// differing only in message wording (msgs) and the target condition set
// (conditions).
func applyDeploymentStatus(conditions *[]metav1.Condition, generation int64, dep *appsv1.Deployment, found bool, msgs deploymentStatusMessages) string {
	set := func(condType string, status metav1.ConditionStatus, reason, message string) {
		meta.SetStatusCondition(conditions, metav1.Condition{
			Type:               condType,
			Status:             status,
			Reason:             reason,
			Message:            message,
			ObservedGeneration: generation,
		})
	}

	if !found {
		set(condReady, metav1.ConditionFalse, "DeploymentNotFound", msgs.notFoundMsg)
		set(condAvailable, metav1.ConditionFalse, "DeploymentNotFound", msgs.notFoundMsg)
		set(condProgressing, metav1.ConditionTrue, "DeploymentPending", "Waiting for deployment to be created")
		set(condDegraded, metav1.ConditionFalse, "NotApplicable", msgs.notApplicableMsg)
		return "Pending"
	}

	switch {
	case dep.Status.ReadyReplicas == dep.Status.Replicas && dep.Status.Replicas > 0:
		set(condReady, metav1.ConditionTrue, "AllReplicasReady", msgs.allReadyMsg)
		set(condAvailable, metav1.ConditionTrue, "DeploymentAvailable",
			fmt.Sprintf("%d/%d replicas available", dep.Status.ReadyReplicas, dep.Status.Replicas))
		set(condProgressing, metav1.ConditionFalse, "DeploymentComplete", "Deployment rollout complete")
		set(condDegraded, metav1.ConditionFalse, "AllReplicasReady", msgs.allReadyMsg)
		return "Running"
	case dep.Status.ReadyReplicas > 0:
		set(condReady, metav1.ConditionFalse, "ScalingInProgress", "Not all replicas are ready")
		set(condAvailable, metav1.ConditionTrue, "PartiallyAvailable",
			fmt.Sprintf("%d/%d replicas available", dep.Status.ReadyReplicas, dep.Status.Replicas))
		set(condProgressing, metav1.ConditionTrue, "ScalingInProgress",
			fmt.Sprintf("Scaling from %d to %d replicas", dep.Status.ReadyReplicas, dep.Status.Replicas))
		set(condDegraded, metav1.ConditionTrue, "InsufficientReplicas",
			fmt.Sprintf("Only %d of %d replicas ready", dep.Status.ReadyReplicas, dep.Status.Replicas))
		return "Scaling"
	default:
		set(condReady, metav1.ConditionFalse, "NoReplicasReady", msgs.noReplicasReadyMsg)
		set(condAvailable, metav1.ConditionFalse, "NoReplicasReady", msgs.noReplicasAvailableMsg)
		set(condProgressing, metav1.ConditionTrue, "DeploymentInProgress", "Waiting for replicas to become ready")
		set(condDegraded, metav1.ConditionFalse, "Initializing", msgs.stillInitializingMsg)
		return "Pending"
	}
}

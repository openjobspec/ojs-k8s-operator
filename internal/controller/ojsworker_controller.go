package controller

import (
	"context"
	"fmt"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	ojsv1alpha1 "github.com/openjobspec/ojs-k8s-operator/api/v1alpha1"
)

const (
	workerFinalizer = "ojs.openjobspec.dev/worker-finalizer"
	condWorkerReady = "Ready"
	condScaling     = "Scaling"
)

// OJSWorkerReconciler reconciles OJSWorker objects.
type OJSWorkerReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=ojs.openjobspec.dev,resources=ojsworkers,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=ojs.openjobspec.dev,resources=ojsworkers/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=ojs.openjobspec.dev,resources=ojsworkers/scale,verbs=get;update;patch
// +kubebuilder:rbac:groups=ojs.openjobspec.dev,resources=ojsworkers/finalizers,verbs=update
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete

// Reconcile handles create/update/delete of OJSWorker resources.
func (r *OJSWorkerReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	worker := &ojsv1alpha1.OJSWorker{}
	if err := r.Get(ctx, req.NamespacedName, worker); err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// Handle deletion
	if !worker.DeletionTimestamp.IsZero() {
		if controllerutil.ContainsFinalizer(worker, workerFinalizer) {
			controllerutil.RemoveFinalizer(worker, workerFinalizer)
			if err := r.Update(ctx, worker); err != nil {
				return ctrl.Result{}, err
			}
		}
		return ctrl.Result{}, nil
	}

	// Add finalizer
	if !controllerutil.ContainsFinalizer(worker, workerFinalizer) {
		controllerutil.AddFinalizer(worker, workerFinalizer)
		if err := r.Update(ctx, worker); err != nil {
			return ctrl.Result{}, err
		}
	}

	// Resolve the parent OJSCluster
	cluster := &ojsv1alpha1.OJSCluster{}
	if err := r.Get(ctx, types.NamespacedName{Name: worker.Spec.ClusterRef, Namespace: worker.Namespace}, cluster); err != nil {
		if errors.IsNotFound(err) {
			r.setWorkerCondition(worker, condWorkerReady, metav1.ConditionFalse, "ClusterNotFound",
				fmt.Sprintf("OJSCluster %q not found", worker.Spec.ClusterRef))
			worker.Status.Phase = "Error"
			_ = r.Status().Update(ctx, worker)
			return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
		}
		return ctrl.Result{}, err
	}

	// Reconcile the worker Deployment
	if err := r.reconcileWorkerDeployment(ctx, worker, cluster); err != nil {
		logger.Error(err, "failed to reconcile worker deployment")
		return ctrl.Result{}, err
	}

	// Update status
	if err := r.updateWorkerStatus(ctx, worker); err != nil {
		logger.Error(err, "failed to update worker status")
		return ctrl.Result{}, err
	}

	// Re-queue periodically if autoscaling is enabled
	if worker.Spec.AutoScaling != nil && worker.Spec.AutoScaling.Enabled {
		interval := 30 * time.Second
		if worker.Spec.AutoScaling.PollingInterval != "" {
			if d, err := time.ParseDuration(worker.Spec.AutoScaling.PollingInterval); err == nil {
				interval = d
			}
		}
		return ctrl.Result{RequeueAfter: interval}, nil
	}

	return ctrl.Result{}, nil
}

func (r *OJSWorkerReconciler) reconcileWorkerDeployment(ctx context.Context, worker *ojsv1alpha1.OJSWorker, cluster *ojsv1alpha1.OJSCluster) error {
	replicas := int32(1)
	if worker.Spec.Replicas != nil {
		replicas = *worker.Spec.Replicas
	}

	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      worker.Name,
			Namespace: worker.Namespace,
		},
	}

	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, dep, func() error {
		if err := ctrl.SetControllerReference(worker, dep, r.Scheme); err != nil {
			return err
		}

		labels := labelsForWorker(worker.Name, cluster.Name)
		dep.Labels = labels
		dep.Spec.Replicas = &replicas
		dep.Spec.Selector = &metav1.LabelSelector{MatchLabels: labels}

		// Build environment variables
		envVars := []corev1.EnvVar{
			{Name: "OJS_URL", Value: fmt.Sprintf("http://%s-server.%s.svc.cluster.local:%d",
				cluster.Name, cluster.Namespace, defaultPort)},
			{Name: "OJS_QUEUES", Value: joinStrings(worker.Spec.Queues)},
			{Name: "OJS_JOB_TYPES", Value: joinStrings(worker.Spec.JobTypes)},
		}
		if worker.Spec.Concurrency > 0 {
			envVars = append(envVars, corev1.EnvVar{
				Name:  "OJS_CONCURRENCY",
				Value: fmt.Sprintf("%d", worker.Spec.Concurrency),
			})
		}
		envVars = append(envVars, worker.Spec.Env...)

		container := corev1.Container{
			Name:    "ojs-worker",
			Image:   worker.Spec.Image,
			Env:     envVars,
		}

		if len(worker.Spec.Command) > 0 {
			container.Command = worker.Spec.Command
		}

		if worker.Spec.Resources.Limits != nil || worker.Spec.Resources.Requests != nil {
			container.Resources = worker.Spec.Resources
		}

		// Graceful shutdown configuration
		terminationGracePeriod := int64(30)
		if worker.Spec.GracefulShutdown != nil && worker.Spec.GracefulShutdown.TimeoutSeconds > 0 {
			terminationGracePeriod = int64(worker.Spec.GracefulShutdown.TimeoutSeconds)
		}

		dep.Spec.Template = corev1.PodTemplateSpec{
			ObjectMeta: metav1.ObjectMeta{Labels: labels},
			Spec: corev1.PodSpec{
				Containers:                    []corev1.Container{container},
				TerminationGracePeriodSeconds: &terminationGracePeriod,
			},
		}
		return nil
	})
	return err
}

func (r *OJSWorkerReconciler) updateWorkerStatus(ctx context.Context, worker *ojsv1alpha1.OJSWorker) error {
	dep := &appsv1.Deployment{}
	if err := r.Get(ctx, types.NamespacedName{Name: worker.Name, Namespace: worker.Namespace}, dep); err != nil {
		if errors.IsNotFound(err) {
			worker.Status.Phase = "Pending"
		} else {
			return err
		}
	} else {
		worker.Status.Replicas = dep.Status.Replicas
		worker.Status.ReadyReplicas = dep.Status.ReadyReplicas

		if dep.Status.ReadyReplicas == dep.Status.Replicas && dep.Status.Replicas > 0 {
			worker.Status.Phase = "Running"
			r.setWorkerCondition(worker, condWorkerReady, metav1.ConditionTrue, "AllReplicasReady", "All worker replicas are ready")
		} else if dep.Status.ReadyReplicas > 0 {
			worker.Status.Phase = "Scaling"
			r.setWorkerCondition(worker, condWorkerReady, metav1.ConditionFalse, "ScalingInProgress", "Not all replicas are ready")
		} else {
			worker.Status.Phase = "Pending"
			r.setWorkerCondition(worker, condWorkerReady, metav1.ConditionFalse, "NoReplicasReady", "No worker replicas are ready yet")
		}
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

func labelsForWorker(workerName, clusterName string) map[string]string {
	return map[string]string{
		"app.kubernetes.io/name":       "ojs-worker",
		"app.kubernetes.io/instance":   workerName,
		"app.kubernetes.io/component":  "worker",
		"app.kubernetes.io/part-of":    "ojs",
		"app.kubernetes.io/managed-by": "ojs-k8s-operator",
		"ojs.openjobspec.dev/cluster":  clusterName,
	}
}

func joinStrings(ss []string) string {
	result := ""
	for i, s := range ss {
		if i > 0 {
			result += ","
		}
		result += s
	}
	return result
}

// SetupWithManager sets up the controller with the Manager.
func (r *OJSWorkerReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&ojsv1alpha1.OJSWorker{}).
		Owns(&appsv1.Deployment{}).
		Named("ojsworker").
		Complete(r)
}

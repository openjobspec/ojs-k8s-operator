package controller

import (
	"context"
	"fmt"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	ojsv1alpha1 "github.com/openjobspec/ojs-k8s-operator/api/v1alpha1"
)

// This file owns the OJSWorker reconciliation lifecycle: finalizer
// handling, resolving the parent OJSCluster, orchestrating child-resource
// reconciliation, and autoscaling requeue behavior, plus controller
// registration. Deployment projection lives in ojsworker_deployment.go, HPA
// policy in ojsworker_hpa.go, status transitions in ojsworker_status.go, and
// pure desired-state builders (including labels/serialization) in
// ojsworker_desired.go.

const (
	workerFinalizer = "ojs.openjobspec.dev/worker-finalizer"

	// clusterNotFoundRequeueDelay controls how soon a worker whose
	// referenced OJSCluster does not (yet) exist is retried.
	clusterNotFoundRequeueDelay = 30 * time.Second

	// defaultAutoScalingPollingInterval is used when autoscaling is enabled
	// but spec.autoScaling.pollingInterval is unset or fails to parse.
	defaultAutoScalingPollingInterval = 30 * time.Second
)

// OJSWorkerReconciler reconciles OJSWorker objects.
type OJSWorkerReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder
}

// +kubebuilder:rbac:groups=ojs.openjobspec.dev,resources=ojsworkers,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=ojs.openjobspec.dev,resources=ojsworkers/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=ojs.openjobspec.dev,resources=ojsworkers/scale,verbs=get;update;patch
// +kubebuilder:rbac:groups=ojs.openjobspec.dev,resources=ojsworkers/finalizers,verbs=update
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=autoscaling,resources=horizontalpodautoscalers,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core,resources=events,verbs=create;patch

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

	if done, err := r.reconcileFinalizer(ctx, worker); done {
		return ctrl.Result{}, err
	}

	cluster, result, err := r.resolveCluster(ctx, worker)
	if err != nil || cluster == nil {
		return result, err
	}

	// Reconcile the worker Deployment
	if err := r.reconcileWorkerDeployment(ctx, worker, cluster); err != nil {
		logger.Error(err, "failed to reconcile worker deployment")
		return ctrl.Result{}, err
	}

	// Reconcile HPA if autoscaling is configured
	if err := r.reconcileWorkerHPA(ctx, worker); err != nil {
		logger.Error(err, "failed to reconcile worker HPA")
		return ctrl.Result{}, err
	}

	// Update status
	if err := r.updateWorkerStatus(ctx, worker); err != nil {
		logger.Error(err, "failed to update worker status")
		return ctrl.Result{}, err
	}

	return r.autoScalingRequeueResult(worker), nil
}

// reconcileFinalizer handles the deletion and finalizer-registration phases
// of the OJSWorker lifecycle. When done is true, Reconcile must return
// immediately with (ctrl.Result{}, err).
func (r *OJSWorkerReconciler) reconcileFinalizer(ctx context.Context, worker *ojsv1alpha1.OJSWorker) (done bool, err error) {
	if !worker.DeletionTimestamp.IsZero() {
		if controllerutil.ContainsFinalizer(worker, workerFinalizer) {
			r.recordEvent(worker, corev1.EventTypeNormal, "Deleting", "Cleaning up worker resources")
			controllerutil.RemoveFinalizer(worker, workerFinalizer)
			if err := r.Update(ctx, worker); err != nil {
				return true, err
			}
		}
		return true, nil
	}

	if !controllerutil.ContainsFinalizer(worker, workerFinalizer) {
		controllerutil.AddFinalizer(worker, workerFinalizer)
		if err := r.Update(ctx, worker); err != nil {
			return true, err
		}
	}
	return false, nil
}

// resolveCluster resolves the OJSCluster referenced by worker.Spec.ClusterRef.
// If the cluster is not found, it marks the worker as errored (status,
// conditions, event) and returns (nil, requeueResult, nil) so Reconcile can
// return immediately without treating it as a Reconcile error. Any other
// lookup error is returned as-is.
func (r *OJSWorkerReconciler) resolveCluster(ctx context.Context, worker *ojsv1alpha1.OJSWorker) (*ojsv1alpha1.OJSCluster, ctrl.Result, error) {
	logger := log.FromContext(ctx)

	cluster := &ojsv1alpha1.OJSCluster{}
	if err := r.Get(ctx, types.NamespacedName{Name: worker.Spec.ClusterRef, Namespace: worker.Namespace}, cluster); err != nil {
		if !errors.IsNotFound(err) {
			return nil, ctrl.Result{}, err
		}

		r.setWorkerCondition(worker, condWorkerReady, metav1.ConditionFalse, "ClusterNotFound",
			fmt.Sprintf("OJSCluster %q not found", worker.Spec.ClusterRef))
		r.setWorkerCondition(worker, condWorkerDegraded, metav1.ConditionTrue, "ClusterNotFound",
			fmt.Sprintf("OJSCluster %q not found", worker.Spec.ClusterRef))
		worker.Status.Phase = "Error"
		if updateErr := r.Status().Update(ctx, worker); updateErr != nil {
			logger.Error(updateErr, "failed to update worker status after cluster not found")
		}
		r.recordEvent(worker, corev1.EventTypeWarning, "ClusterNotFound",
			fmt.Sprintf("Referenced OJSCluster %q not found", worker.Spec.ClusterRef))
		return nil, ctrl.Result{RequeueAfter: clusterNotFoundRequeueDelay}, nil
	}
	return cluster, ctrl.Result{}, nil
}

// autoScalingRequeueResult computes the periodic requeue result used to
// keep polling queue-depth-based autoscaling decisions while enabled.
func (r *OJSWorkerReconciler) autoScalingRequeueResult(worker *ojsv1alpha1.OJSWorker) ctrl.Result {
	if worker.Spec.AutoScaling == nil || !worker.Spec.AutoScaling.Enabled {
		return ctrl.Result{}
	}
	interval := defaultAutoScalingPollingInterval
	if worker.Spec.AutoScaling.PollingInterval != "" {
		if d, err := time.ParseDuration(worker.Spec.AutoScaling.PollingInterval); err == nil {
			interval = d
		}
	}
	return ctrl.Result{RequeueAfter: interval}
}

func (r *OJSWorkerReconciler) recordEvent(worker *ojsv1alpha1.OJSWorker, eventType, reason, message string) {
	if r.Recorder != nil {
		r.Recorder.Event(worker, eventType, reason, message)
	}
}

// SetupWithManager sets up the controller with the Manager.
func (r *OJSWorkerReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&ojsv1alpha1.OJSWorker{}).
		Owns(&appsv1.Deployment{}).
		Owns(&autoscalingv2.HorizontalPodAutoscaler{}).
		Named("ojsworker").
		Complete(r)
}

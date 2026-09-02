package controller

import (
	"context"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	ojsv1alpha1 "github.com/openjobspec/ojs-k8s-operator/api/v1alpha1"
)

// This file owns the OJSCluster reconciliation lifecycle (finalizer
// handling, orchestration of child-resource reconciliation, and requeue
// behavior) and controller registration. Status computation lives in
// ojscluster_status.go, desired child-resource reconciliation lives in
// ojscluster_resources.go, and pure desired-state builders live in
// ojscluster_desired.go.

const ojsFinalizer = "ojs.openjobspec.dev/finalizer"

// OJSClusterReconciler reconciles OJSCluster objects.
type OJSClusterReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder
}

// +kubebuilder:rbac:groups=ojs.openjobspec.dev,resources=ojsclusters,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=ojs.openjobspec.dev,resources=ojsclusters/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=ojs.openjobspec.dev,resources=ojsclusters/finalizers,verbs=update
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core,resources=services;configmaps,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core,resources=events,verbs=create;patch
// +kubebuilder:rbac:groups=policy,resources=poddisruptionbudgets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=monitoring.coreos.com,resources=servicemonitors,verbs=get;list;watch;create;update;patch;delete

// Reconcile handles create/update/delete of OJSCluster resources.
func (r *OJSClusterReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	cluster := &ojsv1alpha1.OJSCluster{}
	if err := r.Get(ctx, req.NamespacedName, cluster); err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	if done, err := r.reconcileFinalizer(ctx, cluster); done {
		return ctrl.Result{}, err
	}

	if err := r.ensureInitialPhase(ctx, cluster); err != nil {
		return ctrl.Result{}, err
	}

	if err := r.reconcileEmbeddedBackendPhase(ctx, cluster); err != nil {
		return ctrl.Result{}, err
	}

	if err := r.reconcileChildResources(ctx, cluster); err != nil {
		return ctrl.Result{}, err
	}

	// Reconcile or remove the optional ServiceMonitor based on
	// spec.monitoring (see reconcileServiceMonitorPhase). Status is refreshed
	// even when this optional-resource API call fails, then the error is
	// returned so controller-runtime retries with rate limiting.
	serviceMonitorErr := r.reconcileServiceMonitorPhase(ctx, cluster)

	// Update status from Deployment
	if err := r.updateStatus(ctx, cluster); err != nil {
		logger.Error(err, "failed to update status")
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, serviceMonitorErr
}

// reconcileFinalizer handles the deletion and finalizer-registration phases
// of the OJSCluster lifecycle. When done is true, Reconcile must return
// immediately with (ctrl.Result{}, err).
func (r *OJSClusterReconciler) reconcileFinalizer(ctx context.Context, cluster *ojsv1alpha1.OJSCluster) (done bool, err error) {
	if !cluster.DeletionTimestamp.IsZero() {
		if controllerutil.ContainsFinalizer(cluster, ojsFinalizer) {
			r.recordEvent(cluster, corev1.EventTypeNormal, "Deleting", "Cleaning up child resources")
			controllerutil.RemoveFinalizer(cluster, ojsFinalizer)
			if err := r.Update(ctx, cluster); err != nil {
				return true, err
			}
		}
		return true, nil
	}

	if !controllerutil.ContainsFinalizer(cluster, ojsFinalizer) {
		controllerutil.AddFinalizer(cluster, ojsFinalizer)
		if err := r.Update(ctx, cluster); err != nil {
			return true, err
		}
	}
	return false, nil
}

// ensureInitialPhase sets the cluster's phase to Pending on first
// reconciliation (i.e. while Status.Phase is still empty).
func (r *OJSClusterReconciler) ensureInitialPhase(ctx context.Context, cluster *ojsv1alpha1.OJSCluster) error {
	if cluster.Status.Phase != "" {
		return nil
	}
	cluster.Status.Phase = "Pending"
	r.setCondition(cluster, condProgressing, metav1.ConditionTrue, "Reconciling", "Initial reconciliation in progress")
	if err := r.Status().Update(ctx, cluster); err != nil {
		return err
	}
	r.recordEvent(cluster, corev1.EventTypeNormal, "Reconciling", "Starting initial reconciliation")
	return nil
}

// reconcileEmbeddedBackendPhase reconciles the embedded backend (if
// requested by spec.backend.embedded) and reflects any failure into the
// cluster's status/conditions/events. A non-nil return means Reconcile
// should return early with a requeue.
func (r *OJSClusterReconciler) reconcileEmbeddedBackendPhase(ctx context.Context, cluster *ojsv1alpha1.OJSCluster) error {
	if !cluster.Spec.Backend.Embedded {
		return nil
	}

	logger := log.FromContext(ctx)
	if err := r.reconcileEmbeddedBackend(ctx, cluster); err != nil {
		logger.Error(err, "failed to reconcile embedded backend")
		r.setCondition(cluster, condBackend, metav1.ConditionFalse, "BackendFailed", err.Error())
		r.setCondition(cluster, condDegraded, metav1.ConditionTrue, "BackendFailed", err.Error())
		if statusErr := r.Status().Update(ctx, cluster); statusErr != nil {
			logger.Error(statusErr, "failed to update status during backend failure")
		}
		r.recordEvent(cluster, corev1.EventTypeWarning, "BackendFailed", err.Error())
		return err
	}
	r.setCondition(cluster, condBackend, metav1.ConditionTrue, "BackendReady", "Embedded backend is running")
	return nil
}

// reconcileChildResources reconciles the always-required child resources
// (ConfigMap, Deployment, Service, PodDisruptionBudget) in the order the
// operator has always used. ServiceMonitor is intentionally excluded here:
// it is optional and best-effort (see Reconcile).
func (r *OJSClusterReconciler) reconcileChildResources(ctx context.Context, cluster *ojsv1alpha1.OJSCluster) error {
	logger := log.FromContext(ctx)

	if err := r.reconcileConfigMap(ctx, cluster); err != nil {
		logger.Error(err, "failed to reconcile ConfigMap")
		return err
	}
	if err := r.reconcileDeployment(ctx, cluster); err != nil {
		logger.Error(err, "failed to reconcile Deployment")
		return err
	}
	if err := r.reconcileService(ctx, cluster); err != nil {
		logger.Error(err, "failed to reconcile Service")
		return err
	}
	if err := r.reconcilePDB(ctx, cluster); err != nil {
		logger.Error(err, "failed to reconcile PodDisruptionBudget")
		return err
	}
	return nil
}

func (r *OJSClusterReconciler) recordEvent(cluster *ojsv1alpha1.OJSCluster, eventType, reason, message string) {
	if r.Recorder != nil {
		r.Recorder.Event(cluster, eventType, reason, message)
	}
}

// SetupWithManager sets up the controller with the Manager.
func (r *OJSClusterReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&ojsv1alpha1.OJSCluster{}).
		Owns(&appsv1.Deployment{}).
		Owns(&corev1.Service{}).
		Owns(&corev1.ConfigMap{}).
		Owns(&policyv1.PodDisruptionBudget{}).
		Named("ojscluster").
		Complete(r)
}

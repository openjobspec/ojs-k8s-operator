package controller

import (
	"context"

	autoscalingv2 "k8s.io/api/autoscaling/v2"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	ojsv1alpha1 "github.com/openjobspec/ojs-k8s-operator/api/v1alpha1"
)

// reconcileWorkerHPA reconciles (or deletes) the worker's
// HorizontalPodAutoscaler based on spec.autoScaling. Field-level policy
// lives in desiredHPASpec (ojsworker_desired.go).
func (r *OJSWorkerReconciler) reconcileWorkerHPA(ctx context.Context, worker *ojsv1alpha1.OJSWorker) error {
	hpa := &autoscalingv2.HorizontalPodAutoscaler{
		ObjectMeta: metav1.ObjectMeta{
			Name:      worker.Name + "-hpa",
			Namespace: worker.Namespace,
		},
	}

	if worker.Spec.AutoScaling == nil || !worker.Spec.AutoScaling.Enabled {
		return r.deleteWorkerHPA(ctx, worker, hpa)
	}

	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, hpa, func() error {
		if err := ctrl.SetControllerReference(worker, hpa, r.Scheme); err != nil {
			return err
		}
		hpa.Labels = labelsForWorker(worker.Name, worker.Spec.ClusterRef)
		hpa.Spec = desiredHPASpec(worker)
		return nil
	})
	return err
}

// deleteWorkerHPA removes hpa when autoscaling has been disabled, but only
// when the current worker is its controller owner. Same-name HPAs that are
// ownerless or controlled by another object are left untouched.
func (r *OJSWorkerReconciler) deleteWorkerHPA(ctx context.Context, worker *ojsv1alpha1.OJSWorker, hpa *autoscalingv2.HorizontalPodAutoscaler) error {
	if err := r.Get(ctx, types.NamespacedName{Name: hpa.Name, Namespace: hpa.Namespace}, hpa); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return err
	}
	if !metav1.IsControlledBy(hpa, worker) {
		return nil
	}
	// Ignore NotFound on Delete too, in case the HPA was removed
	// concurrently between the Get above and this call.
	return client.IgnoreNotFound(r.Delete(ctx, hpa))
}

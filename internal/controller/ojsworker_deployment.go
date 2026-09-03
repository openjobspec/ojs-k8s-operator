package controller

import (
	"context"

	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	ojsv1alpha1 "github.com/openjobspec/ojs-k8s-operator/api/v1alpha1"
)

// reconcileWorkerDeployment reconciles the worker's Deployment projection.
// Field-level policy lives in applyWorkerDeploymentSpec (ojsworker_desired.go)
// so it can be tested without a client.
func (r *OJSWorkerReconciler) reconcileWorkerDeployment(ctx context.Context, worker *ojsv1alpha1.OJSWorker, cluster *ojsv1alpha1.OJSCluster) error {
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
		applyWorkerDeploymentSpec(dep, worker, cluster)
		return nil
	})
	return err
}

package controller

import (
	"context"
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	ojsv1alpha1 "github.com/openjobspec/ojs-k8s-operator/api/v1alpha1"
)

// This file owns the desired child-resource *reconciliation policy* for
// OJSCluster: it is the only place that talks to the Kubernetes API server to
// create/update the ConfigMap, Deployment, Service, embedded-backend
// resources, PodDisruptionBudget, and ServiceMonitor that make up a running
// cluster. Field-level projection logic lives in the pure builders in
// ojscluster_desired.go so it can be tested without a client.

func (r *OJSClusterReconciler) reconcileConfigMap(ctx context.Context, cluster *ojsv1alpha1.OJSCluster) error {
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      cluster.Name + "-config",
			Namespace: cluster.Namespace,
		},
	}

	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, cm, func() error {
		if err := ctrl.SetControllerReference(cluster, cm, r.Scheme); err != nil {
			return err
		}
		cm.Labels = labelsForCluster(cluster.Name)
		cm.Data = desiredConfigMapData(cluster)
		return nil
	})
	return err
}

func (r *OJSClusterReconciler) reconcileDeployment(ctx context.Context, cluster *ojsv1alpha1.OJSCluster) error {
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      cluster.Name + "-server",
			Namespace: cluster.Namespace,
		},
	}

	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, dep, func() error {
		if err := ctrl.SetControllerReference(cluster, dep, r.Scheme); err != nil {
			return err
		}
		applyServerDeploymentSpec(dep, cluster)
		return nil
	})
	return err
}

func (r *OJSClusterReconciler) reconcileService(ctx context.Context, cluster *ojsv1alpha1.OJSCluster) error {
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      cluster.Name + "-server",
			Namespace: cluster.Namespace,
		},
	}

	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, svc, func() error {
		if err := ctrl.SetControllerReference(cluster, svc, r.Scheme); err != nil {
			return err
		}
		labels := labelsForCluster(cluster.Name)
		svc.Labels = labels
		applyDesiredServiceSpec(svc, desiredServerServiceSpec(labels))
		return nil
	})
	return err
}

func (r *OJSClusterReconciler) reconcileEmbeddedBackend(ctx context.Context, cluster *ojsv1alpha1.OJSCluster) error {
	switch cluster.Spec.Backend.Type {
	case "redis":
		return r.reconcileEmbeddedRedis(ctx, cluster)
	default:
		return fmt.Errorf("embedded backend not supported for type %q; only 'redis' is supported", cluster.Spec.Backend.Type)
	}
}

func (r *OJSClusterReconciler) reconcileEmbeddedRedis(ctx context.Context, cluster *ojsv1alpha1.OJSCluster) error {
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      cluster.Name + "-redis",
			Namespace: cluster.Namespace,
		},
	}

	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, dep, func() error {
		if err := ctrl.SetControllerReference(cluster, dep, r.Scheme); err != nil {
			return err
		}
		applyRedisDeploymentSpec(dep, cluster)
		return nil
	})
	if err != nil {
		return err
	}

	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      cluster.Name + "-redis",
			Namespace: cluster.Namespace,
		},
	}

	_, err = controllerutil.CreateOrUpdate(ctx, r.Client, svc, func() error {
		if err := ctrl.SetControllerReference(cluster, svc, r.Scheme); err != nil {
			return err
		}
		labels := redisLabels(cluster.Name)
		svc.Labels = labels
		applyDesiredServiceSpec(svc, desiredRedisServiceSpec(labels))
		return nil
	})
	return err
}

// applyDesiredServiceSpec updates only fields owned by the operator. The API
// server owns/defaults the remaining ServiceSpec fields, many of which are
// immutable after creation and must be carried through subsequent updates.
func applyDesiredServiceSpec(svc *corev1.Service, desired corev1.ServiceSpec) {
	svc.Spec.Selector = desired.Selector
	svc.Spec.Ports = mergeServicePorts(svc.Spec.Ports, desired.Ports)
	if desired.Type != "" {
		svc.Spec.Type = desired.Type
	}
}

// mergeServicePorts preserves API-assigned node ports while updating the
// operator-owned port definitions.
func mergeServicePorts(existing, desired []corev1.ServicePort) []corev1.ServicePort {
	ports := make([]corev1.ServicePort, len(desired))
	copy(ports, desired)

	for i := range ports {
		if ports[i].NodePort != 0 {
			continue
		}
		for _, current := range existing {
			if current.Name == ports[i].Name && current.Protocol == ports[i].Protocol {
				ports[i].NodePort = current.NodePort
				break
			}
		}
	}
	return ports
}

func (r *OJSClusterReconciler) reconcilePDB(ctx context.Context, cluster *ojsv1alpha1.OJSCluster) error {
	replicas := clusterReplicas(cluster)
	if pdbDisabled(cluster, replicas) {
		return r.deletePDB(ctx, cluster)
	}

	pdb := &policyv1.PodDisruptionBudget{
		ObjectMeta: metav1.ObjectMeta{
			Name:      cluster.Name + "-server",
			Namespace: cluster.Namespace,
		},
	}

	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, pdb, func() error {
		if err := ctrl.SetControllerReference(cluster, pdb, r.Scheme); err != nil {
			return err
		}
		labels := labelsForCluster(cluster.Name)
		pdb.Labels = labels
		pdb.Spec = desiredPDBSpec(cluster, labels)
		return nil
	})
	return err
}

// deletePDB removes a no-longer-desired PDB only when this cluster controls
// it. Missing resources and API no-match errors are safe no-ops.
func (r *OJSClusterReconciler) deletePDB(ctx context.Context, cluster *ojsv1alpha1.OJSCluster) error {
	pdb := &policyv1.PodDisruptionBudget{}
	key := client.ObjectKey{Name: cluster.Name + "-server", Namespace: cluster.Namespace}
	if err := r.Get(ctx, key, pdb); err != nil {
		if apierrors.IsNotFound(err) || apimeta.IsNoMatchError(err) {
			return nil
		}
		return err
	}
	if !metav1.IsControlledBy(pdb, cluster) {
		return nil
	}
	if err := r.Delete(ctx, pdb); err != nil {
		if apierrors.IsNotFound(err) || apimeta.IsNoMatchError(err) {
			return nil
		}
		return err
	}
	return nil
}

func serviceMonitorGVK() schema.GroupVersionKind {
	return schema.GroupVersionKind{
		Group:   "monitoring.coreos.com",
		Version: "v1",
		Kind:    "ServiceMonitor",
	}
}

func (r *OJSClusterReconciler) reconcileServiceMonitor(ctx context.Context, cluster *ojsv1alpha1.OJSCluster) error {
	sm := &unstructured.Unstructured{}
	sm.SetGroupVersionKind(serviceMonitorGVK())
	sm.SetName(cluster.Name + "-server")
	sm.SetNamespace(cluster.Namespace)

	labels := labelsForCluster(cluster.Name)

	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, sm, func() error {
		if err := ctrl.SetControllerReference(cluster, sm, r.Scheme); err != nil {
			return err
		}
		sm.SetLabels(labels)
		sm.Object["spec"] = desiredServiceMonitorSpec(cluster.Namespace, labels)
		return nil
	})
	if apimeta.IsNoMatchError(err) {
		return nil
	}
	return err
}

// deleteServiceMonitor removes a previously-created ServiceMonitor. It is
// used when monitoring/ServiceMonitor has been turned off so the resource
// does not linger as an orphan (mirroring how reconcileWorkerHPA removes the
// HPA once autoscaling is disabled). A missing CRD/kind (prometheus-operator
// not installed) and an already-deleted object are treated as "nothing to
// delete". Same-name resources not controlled by this cluster are preserved.
func (r *OJSClusterReconciler) deleteServiceMonitor(ctx context.Context, cluster *ojsv1alpha1.OJSCluster) error {
	sm := &unstructured.Unstructured{}
	sm.SetGroupVersionKind(serviceMonitorGVK())

	key := client.ObjectKey{Name: cluster.Name + "-server", Namespace: cluster.Namespace}
	if err := r.Get(ctx, key, sm); err != nil {
		if apierrors.IsNotFound(err) || apimeta.IsNoMatchError(err) {
			return nil
		}
		return err
	}
	if !metav1.IsControlledBy(sm, cluster) {
		return nil
	}
	if err := r.Delete(ctx, sm); err != nil {
		if apierrors.IsNotFound(err) || apimeta.IsNoMatchError(err) {
			return nil
		}
		return err
	}
	return nil
}

// reconcileServiceMonitorPhase reconciles or removes the optional
// prometheus-operator ServiceMonitor based on spec.monitoring. A missing CRD
// is an expected optional-capability absence; other API failures are
// logged/evented and returned so controller-runtime retries reconciliation.
func (r *OJSClusterReconciler) reconcileServiceMonitorPhase(ctx context.Context, cluster *ojsv1alpha1.OJSCluster) error {
	logger := log.FromContext(ctx)

	if wantServiceMonitor(cluster) {
		if err := r.reconcileServiceMonitor(ctx, cluster); err != nil {
			logger.Error(err, "failed to reconcile ServiceMonitor")
			r.recordEvent(cluster, corev1.EventTypeWarning, "ServiceMonitorFailed", err.Error())
			return err
		}
		return nil
	}

	if err := r.deleteServiceMonitor(ctx, cluster); err != nil {
		logger.Error(err, "failed to delete ServiceMonitor")
		r.recordEvent(cluster, corev1.EventTypeWarning, "ServiceMonitorDeleteFailed", err.Error())
		return err
	}
	return nil
}

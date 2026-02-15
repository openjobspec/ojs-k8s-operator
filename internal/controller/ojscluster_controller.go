package controller

import (
	"context"
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	ojsv1alpha1 "github.com/openjobspec/ojs-k8s-operator/api/v1alpha1"
)

const (
	ojsFinalizer   = "ojs.openjobspec.dev/finalizer"
	defaultImage   = "ghcr.io/openjobspec/ojs-server:latest"
	defaultPort    = 8080
	metricsPort    = 9090
	condReady      = "Ready"
	condBackend    = "BackendReady"
)

// OJSClusterReconciler reconciles OJSCluster objects.
type OJSClusterReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=ojs.openjobspec.dev,resources=ojsclusters,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=ojs.openjobspec.dev,resources=ojsclusters/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=ojs.openjobspec.dev,resources=ojsclusters/finalizers,verbs=update
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core,resources=services;configmaps,verbs=get;list;watch;create;update;patch;delete

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

	// Handle deletion
	if !cluster.DeletionTimestamp.IsZero() {
		if controllerutil.ContainsFinalizer(cluster, ojsFinalizer) {
			controllerutil.RemoveFinalizer(cluster, ojsFinalizer)
			if err := r.Update(ctx, cluster); err != nil {
				return ctrl.Result{}, err
			}
		}
		return ctrl.Result{}, nil
	}

	// Add finalizer if missing
	if !controllerutil.ContainsFinalizer(cluster, ojsFinalizer) {
		controllerutil.AddFinalizer(cluster, ojsFinalizer)
		if err := r.Update(ctx, cluster); err != nil {
			return ctrl.Result{}, err
		}
	}

	// Set phase to Pending if empty
	if cluster.Status.Phase == "" {
		cluster.Status.Phase = "Pending"
		if err := r.Status().Update(ctx, cluster); err != nil {
			return ctrl.Result{}, err
		}
	}

	// Reconcile embedded backend if requested
	if cluster.Spec.Backend.Embedded {
		if err := r.reconcileEmbeddedBackend(ctx, cluster); err != nil {
			logger.Error(err, "failed to reconcile embedded backend")
			r.setCondition(cluster, condBackend, metav1.ConditionFalse, "BackendFailed", err.Error())
			_ = r.Status().Update(ctx, cluster)
			return ctrl.Result{}, err
		}
		r.setCondition(cluster, condBackend, metav1.ConditionTrue, "BackendReady", "Embedded backend is running")
	}

	// Reconcile ConfigMap
	if err := r.reconcileConfigMap(ctx, cluster); err != nil {
		logger.Error(err, "failed to reconcile ConfigMap")
		return ctrl.Result{}, err
	}

	// Reconcile OJS server Deployment
	if err := r.reconcileDeployment(ctx, cluster); err != nil {
		logger.Error(err, "failed to reconcile Deployment")
		return ctrl.Result{}, err
	}

	// Reconcile Service
	if err := r.reconcileService(ctx, cluster); err != nil {
		logger.Error(err, "failed to reconcile Service")
		return ctrl.Result{}, err
	}

	// Reconcile ServiceMonitor if monitoring enabled
	if cluster.Spec.Monitoring != nil && cluster.Spec.Monitoring.Enabled && cluster.Spec.Monitoring.ServiceMonitor {
		logger.Info("ServiceMonitor reconciliation requested (requires prometheus-operator CRDs)")
	}

	// Update status from Deployment
	if err := r.updateStatus(ctx, cluster); err != nil {
		logger.Error(err, "failed to update status")
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

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
		backendURL := r.resolveBackendURL(cluster)
		cm.Data = map[string]string{
			"BACKEND_TYPE": cluster.Spec.Backend.Type,
			"BACKEND_URL":  backendURL,
		}
		return nil
	})
	return err
}

func (r *OJSClusterReconciler) reconcileDeployment(ctx context.Context, cluster *ojsv1alpha1.OJSCluster) error {
	replicas := int32(2)
	if cluster.Spec.Replicas != nil {
		replicas = *cluster.Spec.Replicas
	}

	image := defaultImage
	if cluster.Spec.Image != "" {
		image = cluster.Spec.Image
	}

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

		labels := labelsForCluster(cluster.Name)
		dep.Labels = labels
		dep.Spec.Replicas = &replicas
		dep.Spec.Selector = &metav1.LabelSelector{
			MatchLabels: labels,
		}

		envVars := []corev1.EnvVar{
			{Name: "BACKEND_TYPE", ValueFrom: &corev1.EnvVarSource{
				ConfigMapKeyRef: &corev1.ConfigMapKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{Name: cluster.Name + "-config"},
					Key:                  "BACKEND_TYPE",
				},
			}},
		}

		// Resolve backend URL from secret or configmap
		if cluster.Spec.Backend.URLSecretRef != nil {
			envVars = append(envVars, corev1.EnvVar{
				Name: backendURLEnvVar(cluster.Spec.Backend.Type),
				ValueFrom: &corev1.EnvVarSource{
					SecretKeyRef: &corev1.SecretKeySelector{
						LocalObjectReference: corev1.LocalObjectReference{Name: cluster.Spec.Backend.URLSecretRef.Name},
						Key:                  cluster.Spec.Backend.URLSecretRef.Key,
					},
				},
			})
		} else {
			envVars = append(envVars, corev1.EnvVar{
				Name: backendURLEnvVar(cluster.Spec.Backend.Type),
				ValueFrom: &corev1.EnvVarSource{
					ConfigMapKeyRef: &corev1.ConfigMapKeySelector{
						LocalObjectReference: corev1.LocalObjectReference{Name: cluster.Name + "-config"},
						Key:                  "BACKEND_URL",
					},
				},
			})
		}

		container := corev1.Container{
			Name:  "ojs-server",
			Image: image,
			Ports: []corev1.ContainerPort{
				{Name: "http", ContainerPort: int32(defaultPort), Protocol: corev1.ProtocolTCP},
				{Name: "metrics", ContainerPort: int32(metricsPort), Protocol: corev1.ProtocolTCP},
			},
			Env: envVars,
			LivenessProbe: &corev1.Probe{
				ProbeHandler: corev1.ProbeHandler{
					HTTPGet: &corev1.HTTPGetAction{
						Path: "/healthz",
						Port: intstr.FromInt(defaultPort),
					},
				},
				InitialDelaySeconds: 10,
				PeriodSeconds:       10,
			},
			ReadinessProbe: &corev1.Probe{
				ProbeHandler: corev1.ProbeHandler{
					HTTPGet: &corev1.HTTPGetAction{
						Path: "/readyz",
						Port: intstr.FromInt(defaultPort),
					},
				},
				InitialDelaySeconds: 5,
				PeriodSeconds:       5,
			},
		}

		if cluster.Spec.Resources.Limits != nil || cluster.Spec.Resources.Requests != nil {
			container.Resources = cluster.Spec.Resources
		}

		dep.Spec.Template = corev1.PodTemplateSpec{
			ObjectMeta: metav1.ObjectMeta{Labels: labels},
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{container},
			},
		}
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
		svc.Spec.Selector = labels
		svc.Spec.Ports = []corev1.ServicePort{
			{Name: "http", Port: int32(defaultPort), TargetPort: intstr.FromString("http"), Protocol: corev1.ProtocolTCP},
			{Name: "metrics", Port: int32(metricsPort), TargetPort: intstr.FromString("metrics"), Protocol: corev1.ProtocolTCP},
		}
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
	labels := map[string]string{
		"app.kubernetes.io/name":      "redis",
		"app.kubernetes.io/instance":  cluster.Name,
		"app.kubernetes.io/component": "backend",
		"app.kubernetes.io/part-of":   "ojs",
	}

	// Redis Deployment
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      cluster.Name + "-redis",
			Namespace: cluster.Namespace,
		},
	}

	replicas := int32(1)
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, dep, func() error {
		if err := ctrl.SetControllerReference(cluster, dep, r.Scheme); err != nil {
			return err
		}
		dep.Labels = labels
		dep.Spec.Replicas = &replicas
		dep.Spec.Selector = &metav1.LabelSelector{MatchLabels: labels}
		dep.Spec.Template = corev1.PodTemplateSpec{
			ObjectMeta: metav1.ObjectMeta{Labels: labels},
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{{
					Name:  "redis",
					Image: "redis:7-alpine",
					Ports: []corev1.ContainerPort{
						{Name: "redis", ContainerPort: 6379, Protocol: corev1.ProtocolTCP},
					},
					ReadinessProbe: &corev1.Probe{
						ProbeHandler: corev1.ProbeHandler{
							Exec: &corev1.ExecAction{
								Command: []string{"redis-cli", "ping"},
							},
						},
						PeriodSeconds: 5,
					},
				}},
			},
		}
		return nil
	})
	if err != nil {
		return err
	}

	// Redis Service
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
		svc.Labels = labels
		svc.Spec.Selector = labels
		svc.Spec.Ports = []corev1.ServicePort{
			{Name: "redis", Port: 6379, TargetPort: intstr.FromString("redis"), Protocol: corev1.ProtocolTCP},
		}
		return nil
	})
	return err
}

func (r *OJSClusterReconciler) updateStatus(ctx context.Context, cluster *ojsv1alpha1.OJSCluster) error {
	dep := &appsv1.Deployment{}
	if err := r.Get(ctx, types.NamespacedName{Name: cluster.Name + "-server", Namespace: cluster.Namespace}, dep); err != nil {
		if errors.IsNotFound(err) {
			cluster.Status.Phase = "Pending"
		} else {
			return err
		}
	} else {
		cluster.Status.Replicas = dep.Status.Replicas
		cluster.Status.ReadyReplicas = dep.Status.ReadyReplicas

		if dep.Status.ReadyReplicas == dep.Status.Replicas && dep.Status.Replicas > 0 {
			cluster.Status.Phase = "Running"
			r.setCondition(cluster, condReady, metav1.ConditionTrue, "AllReplicasReady", "All server replicas are ready")
		} else if dep.Status.ReadyReplicas > 0 {
			cluster.Status.Phase = "Scaling"
			r.setCondition(cluster, condReady, metav1.ConditionFalse, "ScalingInProgress", "Not all replicas are ready")
		} else {
			cluster.Status.Phase = "Pending"
			r.setCondition(cluster, condReady, metav1.ConditionFalse, "NoReplicasReady", "No replicas are ready yet")
		}
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

func (r *OJSClusterReconciler) resolveBackendURL(cluster *ojsv1alpha1.OJSCluster) string {
	if cluster.Spec.Backend.URL != "" {
		return cluster.Spec.Backend.URL
	}
	if cluster.Spec.Backend.Embedded {
		switch cluster.Spec.Backend.Type {
		case "redis":
			return fmt.Sprintf("redis://%s-redis.%s.svc.cluster.local:6379", cluster.Name, cluster.Namespace)
		case "postgres":
			return fmt.Sprintf("postgres://%s-postgres.%s.svc.cluster.local:5432/ojs?sslmode=disable", cluster.Name, cluster.Namespace)
		}
	}
	return ""
}

func backendURLEnvVar(backendType string) string {
	switch backendType {
	case "postgres":
		return "DATABASE_URL"
	default:
		return "REDIS_URL"
	}
}

func labelsForCluster(name string) map[string]string {
	return map[string]string{
		"app.kubernetes.io/name":      "ojs-server",
		"app.kubernetes.io/instance":  name,
		"app.kubernetes.io/component": "server",
		"app.kubernetes.io/part-of":   "ojs",
		"app.kubernetes.io/managed-by": "ojs-k8s-operator",
	}
}

// SetupWithManager sets up the controller with the Manager.
func (r *OJSClusterReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&ojsv1alpha1.OJSCluster{}).
		Owns(&appsv1.Deployment{}).
		Owns(&corev1.Service{}).
		Owns(&corev1.ConfigMap{}).
		Named("ojscluster").
		Complete(r)
}

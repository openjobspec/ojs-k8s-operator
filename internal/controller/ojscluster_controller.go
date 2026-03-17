package controller

import (
	"context"
	"fmt"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/tools/record"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	ojsv1alpha1 "github.com/openjobspec/ojs-k8s-operator/api/v1alpha1"
)

const (
	ojsFinalizer     = "ojs.openjobspec.dev/finalizer"
	defaultImage     = "ghcr.io/openjobspec/ojs-server:latest"
	defaultPort      = 8080
	metricsPort      = 9090
	condReady        = "Ready"
	condAvailable    = "Available"
	condProgressing  = "Progressing"
	condDegraded     = "Degraded"
	condBackend      = "BackendReady"

	reconcileRequeueDelay = 10 * time.Second
)

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

	// Handle deletion
	if !cluster.DeletionTimestamp.IsZero() {
		if controllerutil.ContainsFinalizer(cluster, ojsFinalizer) {
			r.recordEvent(cluster, corev1.EventTypeNormal, "Deleting", "Cleaning up child resources")
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
		r.setCondition(cluster, condProgressing, metav1.ConditionTrue, "Reconciling", "Initial reconciliation in progress")
		if err := r.Status().Update(ctx, cluster); err != nil {
			return ctrl.Result{}, err
		}
		r.recordEvent(cluster, corev1.EventTypeNormal, "Reconciling", "Starting initial reconciliation")
	}

	// Reconcile embedded backend if requested
	if cluster.Spec.Backend.Embedded {
		if err := r.reconcileEmbeddedBackend(ctx, cluster); err != nil {
			logger.Error(err, "failed to reconcile embedded backend")
			r.setCondition(cluster, condBackend, metav1.ConditionFalse, "BackendFailed", err.Error())
			r.setCondition(cluster, condDegraded, metav1.ConditionTrue, "BackendFailed", err.Error())
			if statusErr := r.Status().Update(ctx, cluster); statusErr != nil {
				logger.Error(statusErr, "failed to update status during backend failure")
			}
			r.recordEvent(cluster, corev1.EventTypeWarning, "BackendFailed", err.Error())
			return ctrl.Result{RequeueAfter: reconcileRequeueDelay}, err
		}
		r.setCondition(cluster, condBackend, metav1.ConditionTrue, "BackendReady", "Embedded backend is running")
	}

	// Reconcile ConfigMap
	if err := r.reconcileConfigMap(ctx, cluster); err != nil {
		logger.Error(err, "failed to reconcile ConfigMap")
		return ctrl.Result{RequeueAfter: reconcileRequeueDelay}, err
	}

	// Reconcile OJS server Deployment
	if err := r.reconcileDeployment(ctx, cluster); err != nil {
		logger.Error(err, "failed to reconcile Deployment")
		return ctrl.Result{RequeueAfter: reconcileRequeueDelay}, err
	}

	// Reconcile Service
	if err := r.reconcileService(ctx, cluster); err != nil {
		logger.Error(err, "failed to reconcile Service")
		return ctrl.Result{RequeueAfter: reconcileRequeueDelay}, err
	}

	// Reconcile PodDisruptionBudget for HA
	if err := r.reconcilePDB(ctx, cluster); err != nil {
		logger.Error(err, "failed to reconcile PodDisruptionBudget")
		return ctrl.Result{RequeueAfter: reconcileRequeueDelay}, err
	}

	// Reconcile ServiceMonitor if monitoring enabled
	if cluster.Spec.Monitoring != nil && cluster.Spec.Monitoring.Enabled && cluster.Spec.Monitoring.ServiceMonitor {
		if err := r.reconcileServiceMonitor(ctx, cluster); err != nil {
			logger.Error(err, "failed to reconcile ServiceMonitor")
			r.recordEvent(cluster, corev1.EventTypeWarning, "ServiceMonitorFailed", err.Error())
		}
	}

	// Update status from Deployment
	if err := r.updateStatus(ctx, cluster); err != nil {
		logger.Error(err, "failed to update status")
		return ctrl.Result{RequeueAfter: reconcileRequeueDelay}, err
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

		// Build container security context
		containerSC := &corev1.SecurityContext{
			AllowPrivilegeEscalation: ptr.To(false),
			Capabilities: &corev1.Capabilities{
				Drop: []corev1.Capability{"ALL"},
			},
		}
		if cluster.Spec.SecurityContext != nil {
			if cluster.Spec.SecurityContext.ReadOnlyRootFilesystem != nil {
				containerSC.ReadOnlyRootFilesystem = cluster.Spec.SecurityContext.ReadOnlyRootFilesystem
			} else {
				containerSC.ReadOnlyRootFilesystem = ptr.To(true)
			}
		} else {
			containerSC.ReadOnlyRootFilesystem = ptr.To(true)
		}

		container := corev1.Container{
			Name:            "ojs-server",
			Image:           image,
			ImagePullPolicy: corev1.PullIfNotPresent,
			Ports: []corev1.ContainerPort{
				{Name: "http", ContainerPort: int32(defaultPort), Protocol: corev1.ProtocolTCP},
				{Name: "metrics", ContainerPort: int32(metricsPort), Protocol: corev1.ProtocolTCP},
			},
			Env:             envVars,
			SecurityContext: containerSC,
			LivenessProbe: &corev1.Probe{
				ProbeHandler: corev1.ProbeHandler{
					HTTPGet: &corev1.HTTPGetAction{
						Path: "/healthz",
						Port: intstr.FromInt(defaultPort),
					},
				},
				InitialDelaySeconds: 10,
				PeriodSeconds:       10,
				TimeoutSeconds:      3,
				FailureThreshold:    3,
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
				TimeoutSeconds:      2,
				FailureThreshold:    3,
			},
			StartupProbe: &corev1.Probe{
				ProbeHandler: corev1.ProbeHandler{
					HTTPGet: &corev1.HTTPGetAction{
						Path: "/healthz",
						Port: intstr.FromInt(defaultPort),
					},
				},
				InitialDelaySeconds: 2,
				PeriodSeconds:       3,
				FailureThreshold:    10,
			},
		}

		// Apply resource requirements (with sensible defaults)
		if cluster.Spec.Resources.Limits != nil || cluster.Spec.Resources.Requests != nil {
			container.Resources = cluster.Spec.Resources
		} else {
			container.Resources = corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("100m"),
					corev1.ResourceMemory: resource.MustParse("128Mi"),
				},
				Limits: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("1"),
					corev1.ResourceMemory: resource.MustParse("512Mi"),
				},
			}
		}

		// Build pod security context
		podSC := r.buildPodSecurityContext(cluster)

		podSpec := corev1.PodSpec{
			Containers:                []corev1.Container{container},
			SecurityContext:           podSC,
			TerminationGracePeriodSeconds: ptr.To(int64(30)),
			AutomountServiceAccountToken:  ptr.To(false),
		}

		if cluster.Spec.ServiceAccountName != "" {
			podSpec.ServiceAccountName = cluster.Spec.ServiceAccountName
			podSpec.AutomountServiceAccountToken = ptr.To(true)
		}

		// Apply topology spread constraints
		if len(cluster.Spec.TopologySpreadConstraints) > 0 {
			podSpec.TopologySpreadConstraints = cluster.Spec.TopologySpreadConstraints
		} else if replicas > 1 {
			// Default: spread across zones for HA
			podSpec.TopologySpreadConstraints = []corev1.TopologySpreadConstraint{
				{
					MaxSkew:           1,
					TopologyKey:       "topology.kubernetes.io/zone",
					WhenUnsatisfiable: corev1.ScheduleAnyway,
					LabelSelector:     &metav1.LabelSelector{MatchLabels: labels},
				},
			}
			podSpec.Affinity = &corev1.Affinity{
				PodAntiAffinity: &corev1.PodAntiAffinity{
					PreferredDuringSchedulingIgnoredDuringExecution: []corev1.WeightedPodAffinityTerm{
						{
							Weight: 100,
							PodAffinityTerm: corev1.PodAffinityTerm{
								LabelSelector: &metav1.LabelSelector{MatchLabels: labels},
								TopologyKey:   "kubernetes.io/hostname",
							},
						},
					},
				},
			}
		}

		dep.Spec.Template = corev1.PodTemplateSpec{
			ObjectMeta: metav1.ObjectMeta{Labels: labels},
			Spec:       podSpec,
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
				SecurityContext: &corev1.PodSecurityContext{
					RunAsNonRoot: ptr.To(true),
					RunAsUser:    ptr.To(int64(999)),
					RunAsGroup:   ptr.To(int64(999)),
					SeccompProfile: &corev1.SeccompProfile{
						Type: corev1.SeccompProfileTypeRuntimeDefault,
					},
				},
				AutomountServiceAccountToken: ptr.To(false),
				Containers: []corev1.Container{{
					Name:            "redis",
					Image:           "redis:7-alpine",
					ImagePullPolicy: corev1.PullIfNotPresent,
					Ports: []corev1.ContainerPort{
						{Name: "redis", ContainerPort: 6379, Protocol: corev1.ProtocolTCP},
					},
					SecurityContext: &corev1.SecurityContext{
						AllowPrivilegeEscalation: ptr.To(false),
						ReadOnlyRootFilesystem:   ptr.To(true),
						Capabilities: &corev1.Capabilities{
							Drop: []corev1.Capability{"ALL"},
						},
					},
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("50m"),
							corev1.ResourceMemory: resource.MustParse("64Mi"),
						},
						Limits: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("500m"),
							corev1.ResourceMemory: resource.MustParse("256Mi"),
						},
					},
					VolumeMounts: []corev1.VolumeMount{
						{Name: "redis-data", MountPath: "/data"},
					},
					ReadinessProbe: &corev1.Probe{
						ProbeHandler: corev1.ProbeHandler{
							Exec: &corev1.ExecAction{
								Command: []string{"redis-cli", "ping"},
							},
						},
						PeriodSeconds: 5,
					},
					LivenessProbe: &corev1.Probe{
						ProbeHandler: corev1.ProbeHandler{
							Exec: &corev1.ExecAction{
								Command: []string{"redis-cli", "ping"},
							},
						},
						InitialDelaySeconds: 10,
						PeriodSeconds:       10,
					},
				}},
				Volumes: []corev1.Volume{
					{
						Name: "redis-data",
						VolumeSource: corev1.VolumeSource{
							EmptyDir: &corev1.EmptyDirVolumeSource{},
						},
					},
				},
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

func (r *OJSClusterReconciler) buildPodSecurityContext(cluster *ojsv1alpha1.OJSCluster) *corev1.PodSecurityContext {
	sc := &corev1.PodSecurityContext{
		RunAsNonRoot: ptr.To(true),
		RunAsUser:    ptr.To(int64(65534)),
		RunAsGroup:   ptr.To(int64(65534)),
		SeccompProfile: &corev1.SeccompProfile{
			Type: corev1.SeccompProfileTypeRuntimeDefault,
		},
	}
	if cluster.Spec.SecurityContext != nil {
		if cluster.Spec.SecurityContext.RunAsNonRoot != nil {
			sc.RunAsNonRoot = cluster.Spec.SecurityContext.RunAsNonRoot
		}
		if cluster.Spec.SecurityContext.RunAsUser != nil {
			sc.RunAsUser = cluster.Spec.SecurityContext.RunAsUser
		}
		if cluster.Spec.SecurityContext.RunAsGroup != nil {
			sc.RunAsGroup = cluster.Spec.SecurityContext.RunAsGroup
		}
		if cluster.Spec.SecurityContext.FSGroup != nil {
			sc.FSGroup = cluster.Spec.SecurityContext.FSGroup
		}
	}
	return sc
}

func (r *OJSClusterReconciler) reconcilePDB(ctx context.Context, cluster *ojsv1alpha1.OJSCluster) error {
	replicas := int32(2)
	if cluster.Spec.Replicas != nil {
		replicas = *cluster.Spec.Replicas
	}

	// PDB only makes sense with >1 replicas
	if replicas <= 1 {
		return nil
	}

	// Check if PDB is explicitly disabled
	if cluster.Spec.PodDisruptionBudget != nil && cluster.Spec.PodDisruptionBudget.Enabled != nil && !*cluster.Spec.PodDisruptionBudget.Enabled {
		return nil
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
		pdb.Spec.Selector = &metav1.LabelSelector{MatchLabels: labels}

		if cluster.Spec.PodDisruptionBudget != nil && cluster.Spec.PodDisruptionBudget.MinAvailable != nil {
			minAvail := intstr.FromInt32(*cluster.Spec.PodDisruptionBudget.MinAvailable)
			pdb.Spec.MinAvailable = &minAvail
			pdb.Spec.MaxUnavailable = nil
		} else if cluster.Spec.PodDisruptionBudget != nil && cluster.Spec.PodDisruptionBudget.MaxUnavailable != nil {
			maxUnavail := intstr.FromInt32(*cluster.Spec.PodDisruptionBudget.MaxUnavailable)
			pdb.Spec.MaxUnavailable = &maxUnavail
			pdb.Spec.MinAvailable = nil
		} else {
			// Default: allow at most 1 pod unavailable
			maxUnavail := intstr.FromInt(1)
			pdb.Spec.MaxUnavailable = &maxUnavail
			pdb.Spec.MinAvailable = nil
		}
		return nil
	})
	return err
}

func (r *OJSClusterReconciler) reconcileServiceMonitor(ctx context.Context, cluster *ojsv1alpha1.OJSCluster) error {
	sm := &unstructured.Unstructured{}
	sm.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "monitoring.coreos.com",
		Version: "v1",
		Kind:    "ServiceMonitor",
	})
	sm.SetName(cluster.Name + "-server")
	sm.SetNamespace(cluster.Namespace)

	labels := labelsForCluster(cluster.Name)

	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, sm, func() error {
		if err := ctrl.SetControllerReference(cluster, sm, r.Scheme); err != nil {
			return err
		}
		sm.SetLabels(labels)
		sm.Object["spec"] = map[string]interface{}{
			"selector": map[string]interface{}{
				"matchLabels": labels,
			},
			"namespaceSelector": map[string]interface{}{
				"matchNames": []interface{}{cluster.Namespace},
			},
			"endpoints": []interface{}{
				map[string]interface{}{
					"port":     "metrics",
					"path":     "/metrics",
					"interval": "30s",
				},
			},
		}
		return nil
	})
	return err
}

func (r *OJSClusterReconciler) recordEvent(cluster *ojsv1alpha1.OJSCluster, eventType, reason, message string) {
	if r.Recorder != nil {
		r.Recorder.Event(cluster, eventType, reason, message)
	}
}

func (r *OJSClusterReconciler) updateStatus(ctx context.Context, cluster *ojsv1alpha1.OJSCluster) error {
	previousPhase := cluster.Status.Phase

	dep := &appsv1.Deployment{}
	if err := r.Get(ctx, types.NamespacedName{Name: cluster.Name + "-server", Namespace: cluster.Namespace}, dep); err != nil {
		if errors.IsNotFound(err) {
			cluster.Status.Phase = "Pending"
			r.setCondition(cluster, condReady, metav1.ConditionFalse, "DeploymentNotFound", "Server deployment does not exist yet")
			r.setCondition(cluster, condAvailable, metav1.ConditionFalse, "DeploymentNotFound", "Server deployment does not exist yet")
			r.setCondition(cluster, condProgressing, metav1.ConditionTrue, "DeploymentPending", "Waiting for deployment to be created")
			r.setCondition(cluster, condDegraded, metav1.ConditionFalse, "NotApplicable", "Cluster is still initializing")
		} else {
			return err
		}
	} else {
		cluster.Status.Replicas = dep.Status.Replicas
		cluster.Status.ReadyReplicas = dep.Status.ReadyReplicas

		if dep.Status.ReadyReplicas == dep.Status.Replicas && dep.Status.Replicas > 0 {
			cluster.Status.Phase = "Running"
			r.setCondition(cluster, condReady, metav1.ConditionTrue, "AllReplicasReady", "All server replicas are ready")
			r.setCondition(cluster, condAvailable, metav1.ConditionTrue, "DeploymentAvailable",
				fmt.Sprintf("%d/%d replicas available", dep.Status.ReadyReplicas, dep.Status.Replicas))
			r.setCondition(cluster, condProgressing, metav1.ConditionFalse, "DeploymentComplete", "Deployment rollout complete")
			r.setCondition(cluster, condDegraded, metav1.ConditionFalse, "AllReplicasReady", "All server replicas are ready")
		} else if dep.Status.ReadyReplicas > 0 {
			cluster.Status.Phase = "Scaling"
			r.setCondition(cluster, condReady, metav1.ConditionFalse, "ScalingInProgress", "Not all replicas are ready")
			r.setCondition(cluster, condAvailable, metav1.ConditionTrue, "PartiallyAvailable",
				fmt.Sprintf("%d/%d replicas available", dep.Status.ReadyReplicas, dep.Status.Replicas))
			r.setCondition(cluster, condProgressing, metav1.ConditionTrue, "ScalingInProgress",
				fmt.Sprintf("Scaling from %d to %d replicas", dep.Status.ReadyReplicas, dep.Status.Replicas))
			r.setCondition(cluster, condDegraded, metav1.ConditionTrue, "InsufficientReplicas",
				fmt.Sprintf("Only %d of %d replicas ready", dep.Status.ReadyReplicas, dep.Status.Replicas))
		} else {
			cluster.Status.Phase = "Pending"
			r.setCondition(cluster, condReady, metav1.ConditionFalse, "NoReplicasReady", "No replicas are ready yet")
			r.setCondition(cluster, condAvailable, metav1.ConditionFalse, "NoReplicasReady", "No replicas are available")
			r.setCondition(cluster, condProgressing, metav1.ConditionTrue, "DeploymentInProgress", "Waiting for replicas to become ready")
			r.setCondition(cluster, condDegraded, metav1.ConditionFalse, "Initializing", "Cluster is still starting up")
		}
	}

	// Record event on phase transition
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
		case "nats":
			return fmt.Sprintf("nats://%s-nats.%s.svc.cluster.local:4222", cluster.Name, cluster.Namespace)
		case "kafka":
			return fmt.Sprintf("%s-kafka.%s.svc.cluster.local:9092", cluster.Name, cluster.Namespace)
		case "sqs":
			return fmt.Sprintf("https://sqs.%s-sqs.%s.svc.cluster.local", cluster.Name, cluster.Namespace)
		}
	}
	return ""
}

func backendURLEnvVar(backendType string) string {
	switch backendType {
	case "postgres":
		return "DATABASE_URL"
	case "nats":
		return "NATS_URL"
	case "kafka":
		return "KAFKA_BROKERS"
	case "sqs":
		return "SQS_QUEUE_URL"
	case "lite":
		return "BACKEND_URL"
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
		Owns(&policyv1.PodDisruptionBudget{}).
		Named("ojscluster").
		Complete(r)
}

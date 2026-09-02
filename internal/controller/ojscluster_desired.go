package controller

import (
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"

	ojsv1alpha1 "github.com/openjobspec/ojs-k8s-operator/api/v1alpha1"
)

// This file contains the pure desired-state policy for OJSCluster child
// resources: given a cluster spec, these functions deterministically compute
// the Kubernetes object fields the controller should reconcile towards. They
// perform no I/O and hold no controller state, so they can be
// unit/characterization tested directly (see ojscluster_desired_test.go)
// without a fake client.

const (
	defaultImage = "ghcr.io/openjobspec/ojs-server:v0.5.0"
	defaultPort  = 8080
	metricsPort  = 9090
)

// clusterReplicas returns the effective replica count for the server
// Deployment, applying the documented default of 2 when unset.
func clusterReplicas(cluster *ojsv1alpha1.OJSCluster) int32 {
	if cluster.Spec.Replicas != nil {
		return *cluster.Spec.Replicas
	}
	return 2
}

// clusterImage returns the effective server image, applying defaultImage
// when the cluster does not specify one.
func clusterImage(cluster *ojsv1alpha1.OJSCluster) string {
	if cluster.Spec.Image != "" {
		return cluster.Spec.Image
	}
	return defaultImage
}

func labelsForCluster(name string) map[string]string {
	return map[string]string{
		"app.kubernetes.io/name":       "ojs-server",
		"app.kubernetes.io/instance":   name,
		"app.kubernetes.io/component":  "server",
		"app.kubernetes.io/part-of":    "ojs",
		"app.kubernetes.io/managed-by": "ojs-k8s-operator",
	}
}

// resolveBackendURL returns the configured backend URL, or, for embedded
// backends, the in-cluster Service URL the operator will provision.
func resolveBackendURL(cluster *ojsv1alpha1.OJSCluster) string {
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

// desiredConfigMapData computes the config map data for the server
// ConfigMap.
func desiredConfigMapData(cluster *ojsv1alpha1.OJSCluster) map[string]string {
	return map[string]string{
		"BACKEND_TYPE": cluster.Spec.Backend.Type,
		"BACKEND_URL":  resolveBackendURL(cluster),
	}
}

// desiredServerEnvVars computes the environment variables for the ojs-server
// container, resolving the backend URL from either a Secret (when
// URLSecretRef is set) or the generated ConfigMap.
func desiredServerEnvVars(cluster *ojsv1alpha1.OJSCluster) []corev1.EnvVar {
	envVars := []corev1.EnvVar{
		{Name: "BACKEND_TYPE", ValueFrom: &corev1.EnvVarSource{
			ConfigMapKeyRef: &corev1.ConfigMapKeySelector{
				LocalObjectReference: corev1.LocalObjectReference{Name: cluster.Name + "-config"},
				Key:                  "BACKEND_TYPE",
			},
		}},
	}

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

	return envVars
}

// desiredServerContainerSecurityContext computes the ojs-server container
// SecurityContext, honoring an operator-specified ReadOnlyRootFilesystem
// override and defaulting to the hardened posture otherwise.
func desiredServerContainerSecurityContext(cluster *ojsv1alpha1.OJSCluster) *corev1.SecurityContext {
	sc := &corev1.SecurityContext{
		AllowPrivilegeEscalation: ptr.To(false),
		Capabilities: &corev1.Capabilities{
			Drop: []corev1.Capability{"ALL"},
		},
	}
	if cluster.Spec.SecurityContext != nil && cluster.Spec.SecurityContext.ReadOnlyRootFilesystem != nil {
		sc.ReadOnlyRootFilesystem = cluster.Spec.SecurityContext.ReadOnlyRootFilesystem
	} else {
		sc.ReadOnlyRootFilesystem = ptr.To(true)
	}
	return sc
}

// desiredServerLivenessProbe, desiredServerReadinessProbe, and
// desiredServerStartupProbe compute the ojs-server container's health
// probes. Their timing values have been fixed since the operator's earliest
// release and are preserved exactly here.

func desiredServerLivenessProbe() *corev1.Probe {
	return &corev1.Probe{
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
	}
}

func desiredServerReadinessProbe() *corev1.Probe {
	return &corev1.Probe{
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
	}
}

func desiredServerStartupProbe() *corev1.Probe {
	return &corev1.Probe{
		ProbeHandler: corev1.ProbeHandler{
			HTTPGet: &corev1.HTTPGetAction{
				Path: "/healthz",
				Port: intstr.FromInt(defaultPort),
			},
		},
		InitialDelaySeconds: 2,
		PeriodSeconds:       3,
		FailureThreshold:    10,
	}
}

// desiredServerResources computes the ojs-server container's resource
// requirements, falling back to sensible defaults when the cluster does not
// set any.
func desiredServerResources(cluster *ojsv1alpha1.OJSCluster) corev1.ResourceRequirements {
	if cluster.Spec.Resources.Limits != nil || cluster.Spec.Resources.Requests != nil {
		return cluster.Spec.Resources
	}
	return corev1.ResourceRequirements{
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

// desiredServerContainer computes the ojs-server container spec, including
// ports, env vars, probes, security context, and resource requirements.
func desiredServerContainer(cluster *ojsv1alpha1.OJSCluster, image string) corev1.Container {
	return corev1.Container{
		Name:            "ojs-server",
		Image:           image,
		ImagePullPolicy: corev1.PullIfNotPresent,
		Ports: []corev1.ContainerPort{
			{Name: "http", ContainerPort: int32(defaultPort), Protocol: corev1.ProtocolTCP},
			{Name: "metrics", ContainerPort: int32(metricsPort), Protocol: corev1.ProtocolTCP},
		},
		Env:             desiredServerEnvVars(cluster),
		SecurityContext: desiredServerContainerSecurityContext(cluster),
		LivenessProbe:   desiredServerLivenessProbe(),
		ReadinessProbe:  desiredServerReadinessProbe(),
		StartupProbe:    desiredServerStartupProbe(),
		Resources:       desiredServerResources(cluster),
	}
}

// buildPodSecurityContext computes the server pod's PodSecurityContext,
// applying per-field operator overrides on top of the hardened defaults.
func buildPodSecurityContext(cluster *ojsv1alpha1.OJSCluster) *corev1.PodSecurityContext {
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

// desiredServerPodSpec computes the server Pod template spec, including
// topology spread / anti-affinity defaults for HA (>1 replica) deployments.
func desiredServerPodSpec(cluster *ojsv1alpha1.OJSCluster, container corev1.Container, labels map[string]string, replicas int32) corev1.PodSpec {
	podSpec := corev1.PodSpec{
		Containers:                    []corev1.Container{container},
		SecurityContext:               buildPodSecurityContext(cluster),
		TerminationGracePeriodSeconds: ptr.To(int64(30)),
		AutomountServiceAccountToken:  ptr.To(false),
	}

	if cluster.Spec.ServiceAccountName != "" {
		podSpec.ServiceAccountName = cluster.Spec.ServiceAccountName
		podSpec.AutomountServiceAccountToken = ptr.To(true)
	}

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

	return podSpec
}

// applyServerDeploymentSpec projects the desired server Deployment fields
// (labels, replicas, selector, pod template) onto dep. It mutates dep in
// place so it can be used directly inside a controllerutil.CreateOrUpdate
// mutate callback.
func applyServerDeploymentSpec(dep *appsv1.Deployment, cluster *ojsv1alpha1.OJSCluster) {
	replicas := clusterReplicas(cluster)
	image := clusterImage(cluster)
	labels := labelsForCluster(cluster.Name)

	dep.Labels = labels
	dep.Spec.Replicas = &replicas
	dep.Spec.Selector = &metav1.LabelSelector{MatchLabels: labels}

	container := desiredServerContainer(cluster, image)
	dep.Spec.Template = corev1.PodTemplateSpec{
		ObjectMeta: metav1.ObjectMeta{Labels: labels},
		Spec:       desiredServerPodSpec(cluster, container, labels, replicas),
	}
}

// desiredServerServiceSpec computes the server Service spec.
func desiredServerServiceSpec(labels map[string]string) corev1.ServiceSpec {
	return corev1.ServiceSpec{
		Selector: labels,
		Ports: []corev1.ServicePort{
			{Name: "http", Port: int32(defaultPort), TargetPort: intstr.FromString("http"), Protocol: corev1.ProtocolTCP},
			{Name: "metrics", Port: int32(metricsPort), TargetPort: intstr.FromString("metrics"), Protocol: corev1.ProtocolTCP},
		},
	}
}

// redisLabels returns the labels applied to the embedded Redis backend
// resources.
func redisLabels(clusterName string) map[string]string {
	return map[string]string{
		"app.kubernetes.io/name":      "redis",
		"app.kubernetes.io/instance":  clusterName,
		"app.kubernetes.io/component": "backend",
		"app.kubernetes.io/part-of":   "ojs",
	}
}

// redisPodSecurityContext computes the embedded-Redis pod's
// PodSecurityContext.
func redisPodSecurityContext() *corev1.PodSecurityContext {
	return &corev1.PodSecurityContext{
		RunAsNonRoot: ptr.To(true),
		RunAsUser:    ptr.To(int64(999)),
		RunAsGroup:   ptr.To(int64(999)),
		SeccompProfile: &corev1.SeccompProfile{
			Type: corev1.SeccompProfileTypeRuntimeDefault,
		},
	}
}

// redisContainer computes the embedded-Redis container spec.
func redisContainer() corev1.Container {
	return corev1.Container{
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
	}
}

// applyRedisDeploymentSpec projects the desired embedded-Redis Deployment
// fields onto dep.
func applyRedisDeploymentSpec(dep *appsv1.Deployment, cluster *ojsv1alpha1.OJSCluster) {
	labels := redisLabels(cluster.Name)
	replicas := int32(1)

	dep.Labels = labels
	dep.Spec.Replicas = &replicas
	dep.Spec.Selector = &metav1.LabelSelector{MatchLabels: labels}
	dep.Spec.Template = corev1.PodTemplateSpec{
		ObjectMeta: metav1.ObjectMeta{Labels: labels},
		Spec: corev1.PodSpec{
			SecurityContext:              redisPodSecurityContext(),
			AutomountServiceAccountToken: ptr.To(false),
			Containers:                   []corev1.Container{redisContainer()},
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
}

// desiredRedisServiceSpec computes the embedded-Redis Service spec.
func desiredRedisServiceSpec(labels map[string]string) corev1.ServiceSpec {
	return corev1.ServiceSpec{
		Selector: labels,
		Ports: []corev1.ServicePort{
			{Name: "redis", Port: 6379, TargetPort: intstr.FromString("redis"), Protocol: corev1.ProtocolTCP},
		},
	}
}

// pdbDisabled reports whether a PodDisruptionBudget should NOT be reconciled
// for this cluster: PDBs only make sense for >1 replica, and can be
// explicitly disabled via spec.podDisruptionBudget.enabled=false.
func pdbDisabled(cluster *ojsv1alpha1.OJSCluster, replicas int32) bool {
	if replicas <= 1 {
		return true
	}
	if cluster.Spec.PodDisruptionBudget != nil && cluster.Spec.PodDisruptionBudget.Enabled != nil && !*cluster.Spec.PodDisruptionBudget.Enabled {
		return true
	}
	return false
}

// desiredPDBSpec computes the PodDisruptionBudget spec, preferring an
// explicit MinAvailable, then MaxUnavailable, and otherwise defaulting to
// allowing at most one pod unavailable.
func desiredPDBSpec(cluster *ojsv1alpha1.OJSCluster, labels map[string]string) policyv1.PodDisruptionBudgetSpec {
	spec := policyv1.PodDisruptionBudgetSpec{Selector: &metav1.LabelSelector{MatchLabels: labels}}

	switch {
	case cluster.Spec.PodDisruptionBudget != nil && cluster.Spec.PodDisruptionBudget.MinAvailable != nil:
		minAvail := intstr.FromInt32(*cluster.Spec.PodDisruptionBudget.MinAvailable)
		spec.MinAvailable = &minAvail
	case cluster.Spec.PodDisruptionBudget != nil && cluster.Spec.PodDisruptionBudget.MaxUnavailable != nil:
		maxUnavail := intstr.FromInt32(*cluster.Spec.PodDisruptionBudget.MaxUnavailable)
		spec.MaxUnavailable = &maxUnavail
	default:
		maxUnavail := intstr.FromInt(1)
		spec.MaxUnavailable = &maxUnavail
	}

	return spec
}

// wantServiceMonitor reports whether spec.monitoring requests a
// prometheus-operator ServiceMonitor for this cluster.
func wantServiceMonitor(cluster *ojsv1alpha1.OJSCluster) bool {
	return cluster.Spec.Monitoring != nil && cluster.Spec.Monitoring.Enabled && cluster.Spec.Monitoring.ServiceMonitor
}

// desiredServiceMonitorSpec computes the unstructured spec map for the
// prometheus-operator ServiceMonitor created per cluster.
//
// matchLabels MUST be a map[string]interface{} (not map[string]string):
// unstructured.Unstructured content is required to be built from plain JSON
// types (map[string]interface{}, []interface{}, string, int64/float64, bool,
// nil). A raw map[string]string previously stored here would panic in
// runtime.DeepCopyJSONValue ("cannot deep copy map[string]string") the first
// time anything deep-copied the object -- e.g. the fake client's object
// tracker in tests, and controller-runtime's cache/patch machinery against a
// real cluster.
func desiredServiceMonitorSpec(namespace string, labels map[string]string) map[string]interface{} {
	return map[string]interface{}{
		"selector": map[string]interface{}{
			"matchLabels": stringMapToInterfaceMap(labels),
		},
		"namespaceSelector": map[string]interface{}{
			"matchNames": []interface{}{namespace},
		},
		"endpoints": []interface{}{
			map[string]interface{}{
				"port":     "metrics",
				"path":     "/metrics",
				"interval": "30s",
			},
		},
	}
}

// stringMapToInterfaceMap converts a map[string]string into the
// map[string]interface{} shape required by unstructured object content.
func stringMapToInterfaceMap(m map[string]string) map[string]interface{} {
	out := make(map[string]interface{}, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

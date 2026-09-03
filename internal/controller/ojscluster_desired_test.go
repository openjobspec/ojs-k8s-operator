package controller

import (
	"reflect"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	ojsv1alpha1 "github.com/openjobspec/ojs-k8s-operator/api/v1alpha1"
)

// Characterization tests for the pure OJSCluster desired-state builders.
// These pin the exact names, labels, probes, images, resources, security
// settings, topology, and serialization the operator has always produced,
// independent of any Kubernetes client.

func baseCluster() *ojsv1alpha1.OJSCluster {
	return &ojsv1alpha1.OJSCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "snap-cluster", Namespace: "snap-ns"},
		Spec: ojsv1alpha1.OJSClusterSpec{
			Backend: ojsv1alpha1.BackendSpec{Type: "redis", URL: "redis://localhost:6379"},
		},
	}
}

func TestApplyServerDeploymentSpec_Defaults(t *testing.T) {
	cluster := baseCluster()
	dep := &appsv1.Deployment{}

	applyServerDeploymentSpec(dep, cluster)

	wantLabels := map[string]string{
		"app.kubernetes.io/name":       "ojs-server",
		"app.kubernetes.io/instance":   "snap-cluster",
		"app.kubernetes.io/component":  "server",
		"app.kubernetes.io/part-of":    "ojs",
		"app.kubernetes.io/managed-by": "ojs-k8s-operator",
	}
	if !reflect.DeepEqual(dep.Labels, wantLabels) {
		t.Errorf("labels = %v, want %v", dep.Labels, wantLabels)
	}
	if dep.Spec.Replicas == nil || *dep.Spec.Replicas != 2 {
		t.Errorf("default replicas = %v, want 2", dep.Spec.Replicas)
	}
	if !reflect.DeepEqual(dep.Spec.Selector.MatchLabels, wantLabels) {
		t.Errorf("selector = %v, want %v", dep.Spec.Selector.MatchLabels, wantLabels)
	}
	if !reflect.DeepEqual(dep.Spec.Template.Labels, wantLabels) {
		t.Errorf("template labels = %v, want %v", dep.Spec.Template.Labels, wantLabels)
	}

	containers := dep.Spec.Template.Spec.Containers
	if len(containers) != 1 {
		t.Fatalf("expected 1 container, got %d", len(containers))
	}
	c := containers[0]
	if c.Name != "ojs-server" {
		t.Errorf("container name = %q, want ojs-server", c.Name)
	}
	if c.Image != defaultImage {
		t.Errorf("image = %q, want %q", c.Image, defaultImage)
	}
	if c.ImagePullPolicy != corev1.PullIfNotPresent {
		t.Errorf("pull policy = %v, want IfNotPresent", c.ImagePullPolicy)
	}

	wantPorts := []corev1.ContainerPort{
		{Name: "http", ContainerPort: 8080, Protocol: corev1.ProtocolTCP},
		{Name: "metrics", ContainerPort: 9090, Protocol: corev1.ProtocolTCP},
	}
	if !reflect.DeepEqual(c.Ports, wantPorts) {
		t.Errorf("ports = %+v, want %+v", c.Ports, wantPorts)
	}

	if c.LivenessProbe == nil || c.LivenessProbe.HTTPGet.Path != "/healthz" ||
		c.LivenessProbe.InitialDelaySeconds != 10 || c.LivenessProbe.PeriodSeconds != 10 ||
		c.LivenessProbe.TimeoutSeconds != 3 || c.LivenessProbe.FailureThreshold != 3 {
		t.Errorf("unexpected liveness probe: %+v", c.LivenessProbe)
	}
	if c.ReadinessProbe == nil || c.ReadinessProbe.HTTPGet.Path != "/readyz" ||
		c.ReadinessProbe.InitialDelaySeconds != 5 || c.ReadinessProbe.PeriodSeconds != 5 ||
		c.ReadinessProbe.TimeoutSeconds != 2 || c.ReadinessProbe.FailureThreshold != 3 {
		t.Errorf("unexpected readiness probe: %+v", c.ReadinessProbe)
	}
	if c.StartupProbe == nil || c.StartupProbe.HTTPGet.Path != "/healthz" ||
		c.StartupProbe.InitialDelaySeconds != 2 || c.StartupProbe.PeriodSeconds != 3 ||
		c.StartupProbe.FailureThreshold != 10 {
		t.Errorf("unexpected startup probe: %+v", c.StartupProbe)
	}

	if c.SecurityContext == nil ||
		c.SecurityContext.AllowPrivilegeEscalation == nil || *c.SecurityContext.AllowPrivilegeEscalation != false ||
		c.SecurityContext.ReadOnlyRootFilesystem == nil || *c.SecurityContext.ReadOnlyRootFilesystem != true ||
		!reflect.DeepEqual(c.SecurityContext.Capabilities.Drop, []corev1.Capability{"ALL"}) {
		t.Errorf("unexpected container security context: %+v", c.SecurityContext)
	}

	wantResources := corev1.ResourceRequirements{
		Requests: corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("100m"),
			corev1.ResourceMemory: resource.MustParse("128Mi"),
		},
		Limits: corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("1"),
			corev1.ResourceMemory: resource.MustParse("512Mi"),
		},
	}
	if !reflect.DeepEqual(c.Resources, wantResources) {
		t.Errorf("resources = %+v, want %+v", c.Resources, wantResources)
	}

	podSC := dep.Spec.Template.Spec.SecurityContext
	if podSC == nil || podSC.RunAsNonRoot == nil || !*podSC.RunAsNonRoot ||
		podSC.RunAsUser == nil || *podSC.RunAsUser != 65534 ||
		podSC.RunAsGroup == nil || *podSC.RunAsGroup != 65534 ||
		podSC.SeccompProfile == nil || podSC.SeccompProfile.Type != corev1.SeccompProfileTypeRuntimeDefault {
		t.Errorf("unexpected pod security context: %+v", podSC)
	}

	// Default replicas is 2 (>1), so the HA topology spread / anti-affinity
	// defaults are expected to be present; see
	// TestApplyServerDeploymentSpec_SingleReplicaNoTopologySpread for the
	// replicas=1 case.
	if len(dep.Spec.Template.Spec.TopologySpreadConstraints) != 1 {
		t.Errorf("expected 1 default topology spread constraint for 2 replicas, got %+v",
			dep.Spec.Template.Spec.TopologySpreadConstraints)
	}
	if dep.Spec.Template.Spec.Affinity == nil {
		t.Error("expected default anti-affinity for 2 replicas")
	}
}

func TestApplyServerDeploymentSpec_SingleReplicaNoTopologySpread(t *testing.T) {
	cluster := baseCluster()
	cluster.Spec.Replicas = int32Ptr(1)
	dep := &appsv1.Deployment{}

	applyServerDeploymentSpec(dep, cluster)

	if len(dep.Spec.Template.Spec.TopologySpreadConstraints) != 0 {
		t.Errorf("expected no topology spread constraints for single replica, got %+v",
			dep.Spec.Template.Spec.TopologySpreadConstraints)
	}
	if dep.Spec.Template.Spec.Affinity != nil {
		t.Errorf("expected no affinity for single replica, got %+v", dep.Spec.Template.Spec.Affinity)
	}
}

func TestApplyServerDeploymentSpec_CustomImageAndReplicas(t *testing.T) {
	cluster := baseCluster()
	cluster.Spec.Replicas = int32Ptr(3)
	cluster.Spec.Image = "ghcr.io/example/ojs-server:v9"
	dep := &appsv1.Deployment{}

	applyServerDeploymentSpec(dep, cluster)

	if *dep.Spec.Replicas != 3 {
		t.Errorf("replicas = %d, want 3", *dep.Spec.Replicas)
	}
	if dep.Spec.Template.Spec.Containers[0].Image != "ghcr.io/example/ojs-server:v9" {
		t.Errorf("image = %q, want custom image", dep.Spec.Template.Spec.Containers[0].Image)
	}
}

func TestApplyServerDeploymentSpec_HADefaultTopologySpread(t *testing.T) {
	cluster := baseCluster()
	cluster.Spec.Replicas = int32Ptr(3)
	dep := &appsv1.Deployment{}

	applyServerDeploymentSpec(dep, cluster)

	tsc := dep.Spec.Template.Spec.TopologySpreadConstraints
	if len(tsc) != 1 || tsc[0].MaxSkew != 1 || tsc[0].TopologyKey != "topology.kubernetes.io/zone" ||
		tsc[0].WhenUnsatisfiable != corev1.ScheduleAnyway {
		t.Errorf("unexpected default topology spread constraints: %+v", tsc)
	}

	aff := dep.Spec.Template.Spec.Affinity
	if aff == nil || aff.PodAntiAffinity == nil ||
		len(aff.PodAntiAffinity.PreferredDuringSchedulingIgnoredDuringExecution) != 1 ||
		aff.PodAntiAffinity.PreferredDuringSchedulingIgnoredDuringExecution[0].Weight != 100 ||
		aff.PodAntiAffinity.PreferredDuringSchedulingIgnoredDuringExecution[0].PodAffinityTerm.TopologyKey != "kubernetes.io/hostname" {
		t.Errorf("unexpected default pod anti-affinity: %+v", aff)
	}
}

func TestApplyServerDeploymentSpec_CustomTopologySpreadOverridesDefault(t *testing.T) {
	cluster := baseCluster()
	cluster.Spec.Replicas = int32Ptr(3)
	custom := []corev1.TopologySpreadConstraint{
		{MaxSkew: 2, TopologyKey: "custom-key", WhenUnsatisfiable: corev1.DoNotSchedule},
	}
	cluster.Spec.TopologySpreadConstraints = custom
	dep := &appsv1.Deployment{}

	applyServerDeploymentSpec(dep, cluster)

	if !reflect.DeepEqual(dep.Spec.Template.Spec.TopologySpreadConstraints, custom) {
		t.Errorf("expected custom topology spread constraints to be used verbatim, got %+v",
			dep.Spec.Template.Spec.TopologySpreadConstraints)
	}
	// Custom constraints suppress the default anti-affinity injection too.
	if dep.Spec.Template.Spec.Affinity != nil {
		t.Errorf("expected no default affinity when custom topology spread is set, got %+v",
			dep.Spec.Template.Spec.Affinity)
	}
}

func TestApplyServerDeploymentSpec_ServiceAccountAutomount(t *testing.T) {
	cluster := baseCluster()
	cluster.Spec.ServiceAccountName = "custom-sa"
	dep := &appsv1.Deployment{}

	applyServerDeploymentSpec(dep, cluster)

	podSpec := dep.Spec.Template.Spec
	if podSpec.ServiceAccountName != "custom-sa" {
		t.Errorf("service account = %q, want custom-sa", podSpec.ServiceAccountName)
	}
	if podSpec.AutomountServiceAccountToken == nil || !*podSpec.AutomountServiceAccountToken {
		t.Error("expected AutomountServiceAccountToken=true when ServiceAccountName is set")
	}
}

func TestApplyServerDeploymentSpec_DefaultAutomountDisabled(t *testing.T) {
	cluster := baseCluster()
	dep := &appsv1.Deployment{}

	applyServerDeploymentSpec(dep, cluster)

	automount := dep.Spec.Template.Spec.AutomountServiceAccountToken
	if automount == nil || *automount {
		t.Error("expected AutomountServiceAccountToken=false by default")
	}
}

func TestApplyServerDeploymentSpec_SecurityContextOverrides(t *testing.T) {
	cluster := baseCluster()
	falseVal := false
	uid := int64(1000)
	cluster.Spec.SecurityContext = &ojsv1alpha1.PodSecuritySpec{
		ReadOnlyRootFilesystem: &falseVal,
		RunAsUser:              &uid,
	}
	dep := &appsv1.Deployment{}

	applyServerDeploymentSpec(dep, cluster)

	c := dep.Spec.Template.Spec.Containers[0]
	if c.SecurityContext.ReadOnlyRootFilesystem == nil || *c.SecurityContext.ReadOnlyRootFilesystem != false {
		t.Errorf("expected ReadOnlyRootFilesystem override to be honored, got %+v", c.SecurityContext.ReadOnlyRootFilesystem)
	}

	podSC := dep.Spec.Template.Spec.SecurityContext
	if podSC.RunAsUser == nil || *podSC.RunAsUser != 1000 {
		t.Errorf("expected RunAsUser override to be honored, got %v", podSC.RunAsUser)
	}
	// Non-overridden fields keep their hardened defaults.
	if podSC.RunAsGroup == nil || *podSC.RunAsGroup != 65534 {
		t.Errorf("expected RunAsGroup default preserved, got %v", podSC.RunAsGroup)
	}
}

func TestApplyServerDeploymentSpec_ResourceOverrides(t *testing.T) {
	cluster := baseCluster()
	cluster.Spec.Resources = corev1.ResourceRequirements{
		Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("250m")},
	}
	dep := &appsv1.Deployment{}

	applyServerDeploymentSpec(dep, cluster)

	c := dep.Spec.Template.Spec.Containers[0]
	if !reflect.DeepEqual(c.Resources, cluster.Spec.Resources) {
		t.Errorf("resources = %+v, want %+v", c.Resources, cluster.Spec.Resources)
	}
}

func TestDesiredServerEnvVars_SecretRef(t *testing.T) {
	cluster := baseCluster()
	cluster.Spec.Backend.URLSecretRef = &ojsv1alpha1.SecretKeyRef{Name: "backend-secret", Key: "url"}

	envVars := desiredServerEnvVars(cluster)
	if len(envVars) != 2 {
		t.Fatalf("expected 2 env vars, got %d", len(envVars))
	}
	urlVar := envVars[1]
	if urlVar.Name != "REDIS_URL" {
		t.Errorf("env var name = %q, want REDIS_URL", urlVar.Name)
	}
	if urlVar.ValueFrom == nil || urlVar.ValueFrom.SecretKeyRef == nil ||
		urlVar.ValueFrom.SecretKeyRef.Name != "backend-secret" || urlVar.ValueFrom.SecretKeyRef.Key != "url" {
		t.Errorf("expected env var sourced from secret, got %+v", urlVar.ValueFrom)
	}
}

func TestDesiredServerEnvVars_ConfigMapDefault(t *testing.T) {
	cluster := baseCluster()

	envVars := desiredServerEnvVars(cluster)
	urlVar := envVars[1]
	if urlVar.ValueFrom == nil || urlVar.ValueFrom.ConfigMapKeyRef == nil ||
		urlVar.ValueFrom.ConfigMapKeyRef.Name != "snap-cluster-config" || urlVar.ValueFrom.ConfigMapKeyRef.Key != "BACKEND_URL" {
		t.Errorf("expected env var sourced from ConfigMap, got %+v", urlVar.ValueFrom)
	}
}

func TestDesiredConfigMapData(t *testing.T) {
	cluster := baseCluster()
	got := desiredConfigMapData(cluster)
	want := map[string]string{"BACKEND_TYPE": "redis", "BACKEND_URL": "redis://localhost:6379"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("configmap data = %v, want %v", got, want)
	}
}

func TestDesiredServerServiceSpec(t *testing.T) {
	labels := labelsForCluster("svc-cluster")
	spec := desiredServerServiceSpec(labels)

	if !reflect.DeepEqual(spec.Selector, labels) {
		t.Errorf("selector = %v, want %v", spec.Selector, labels)
	}
	want := []corev1.ServicePort{
		{Name: "http", Port: 8080, TargetPort: intstr.FromString("http"), Protocol: corev1.ProtocolTCP},
		{Name: "metrics", Port: 9090, TargetPort: intstr.FromString("metrics"), Protocol: corev1.ProtocolTCP},
	}
	if !reflect.DeepEqual(spec.Ports, want) {
		t.Errorf("ports = %+v, want %+v", spec.Ports, want)
	}
}

func TestApplyRedisDeploymentSpec(t *testing.T) {
	cluster := baseCluster()
	dep := &appsv1.Deployment{}

	applyRedisDeploymentSpec(dep, cluster)

	wantLabels := map[string]string{
		"app.kubernetes.io/name":      "redis",
		"app.kubernetes.io/instance":  "snap-cluster",
		"app.kubernetes.io/component": "backend",
		"app.kubernetes.io/part-of":   "ojs",
	}
	if !reflect.DeepEqual(dep.Labels, wantLabels) {
		t.Errorf("labels = %v, want %v", dep.Labels, wantLabels)
	}
	if *dep.Spec.Replicas != 1 {
		t.Errorf("replicas = %d, want 1", *dep.Spec.Replicas)
	}

	c := dep.Spec.Template.Spec.Containers[0]
	if c.Name != "redis" || c.Image != "redis:7-alpine" {
		t.Errorf("unexpected redis container: %+v", c)
	}
	if len(c.Ports) != 1 || c.Ports[0].ContainerPort != 6379 {
		t.Errorf("unexpected redis ports: %+v", c.Ports)
	}
	if len(dep.Spec.Template.Spec.Volumes) != 1 || dep.Spec.Template.Spec.Volumes[0].Name != "redis-data" {
		t.Errorf("unexpected redis volumes: %+v", dep.Spec.Template.Spec.Volumes)
	}
}

func TestDesiredRedisServiceSpec(t *testing.T) {
	labels := redisLabels("redis-cluster")
	spec := desiredRedisServiceSpec(labels)

	if !reflect.DeepEqual(spec.Selector, labels) {
		t.Errorf("selector = %v, want %v", spec.Selector, labels)
	}
	if len(spec.Ports) != 1 || spec.Ports[0].Name != "redis" || spec.Ports[0].Port != 6379 {
		t.Errorf("unexpected redis service ports: %+v", spec.Ports)
	}
}

func TestPdbDisabled(t *testing.T) {
	cluster := baseCluster()
	if !pdbDisabled(cluster, 1) {
		t.Error("expected PDB disabled for replicas<=1")
	}
	if pdbDisabled(cluster, 2) {
		t.Error("expected PDB enabled by default for replicas>1")
	}

	falseVal := false
	cluster.Spec.PodDisruptionBudget = &ojsv1alpha1.PDBSpec{Enabled: &falseVal}
	if !pdbDisabled(cluster, 3) {
		t.Error("expected PDB disabled when explicitly disabled")
	}
}

func TestDesiredPDBSpec_DefaultMaxUnavailableOne(t *testing.T) {
	cluster := baseCluster()
	labels := labelsForCluster(cluster.Name)

	spec := desiredPDBSpec(cluster, labels)

	if !reflect.DeepEqual(spec.Selector.MatchLabels, labels) {
		t.Errorf("selector = %v, want %v", spec.Selector.MatchLabels, labels)
	}
	if spec.MaxUnavailable == nil || spec.MaxUnavailable.IntValue() != 1 {
		t.Errorf("expected default MaxUnavailable=1, got %v", spec.MaxUnavailable)
	}
	if spec.MinAvailable != nil {
		t.Errorf("expected MinAvailable unset by default, got %v", spec.MinAvailable)
	}
}

func TestDesiredPDBSpec_MinAvailablePreferredOverMaxUnavailable(t *testing.T) {
	cluster := baseCluster()
	minAvail := int32(2)
	maxUnavail := int32(1)
	cluster.Spec.PodDisruptionBudget = &ojsv1alpha1.PDBSpec{MinAvailable: &minAvail, MaxUnavailable: &maxUnavail}
	labels := labelsForCluster(cluster.Name)

	spec := desiredPDBSpec(cluster, labels)

	if spec.MinAvailable == nil || spec.MinAvailable.IntValue() != 2 {
		t.Errorf("expected MinAvailable=2, got %v", spec.MinAvailable)
	}
	if spec.MaxUnavailable != nil {
		t.Errorf("expected MaxUnavailable unset when MinAvailable is set, got %v", spec.MaxUnavailable)
	}
}

func TestDesiredPDBSpec_RetainsExplicitZero(t *testing.T) {
	tests := []struct {
		name               string
		pdb                *ojsv1alpha1.PDBSpec
		wantMinAvailable   bool
		wantMaxUnavailable bool
	}{
		{
			name:             "minAvailable",
			pdb:              &ojsv1alpha1.PDBSpec{MinAvailable: int32Ptr(0)},
			wantMinAvailable: true,
		},
		{
			name:               "maxUnavailable",
			pdb:                &ojsv1alpha1.PDBSpec{MaxUnavailable: int32Ptr(0)},
			wantMaxUnavailable: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cluster := baseCluster()
			cluster.Spec.PodDisruptionBudget = tt.pdb

			spec := desiredPDBSpec(cluster, labelsForCluster(cluster.Name))

			if tt.wantMinAvailable {
				if spec.MinAvailable == nil || spec.MinAvailable.IntValue() != 0 {
					t.Fatalf("MinAvailable = %v, want explicit zero", spec.MinAvailable)
				}
			} else if spec.MinAvailable != nil {
				t.Fatalf("MinAvailable = %v, want nil", spec.MinAvailable)
			}
			if tt.wantMaxUnavailable {
				if spec.MaxUnavailable == nil || spec.MaxUnavailable.IntValue() != 0 {
					t.Fatalf("MaxUnavailable = %v, want explicit zero", spec.MaxUnavailable)
				}
			} else if spec.MaxUnavailable != nil {
				t.Fatalf("MaxUnavailable = %v, want nil", spec.MaxUnavailable)
			}
		})
	}
}

func TestDesiredServiceMonitorSpec(t *testing.T) {
	labels := labelsForCluster("sm-cluster")
	spec := desiredServiceMonitorSpec("sm-ns", labels)

	selector, ok := spec["selector"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected selector map, got %T", spec["selector"])
	}
	// matchLabels must be map[string]interface{} (unstructured content
	// requirement), not map[string]string -- see desiredServiceMonitorSpec.
	matchLabels, ok := selector["matchLabels"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected matchLabels to be map[string]interface{}, got %T", selector["matchLabels"])
	}
	if !reflect.DeepEqual(matchLabels, stringMapToInterfaceMap(labels)) {
		t.Errorf("matchLabels = %v, want %v", matchLabels, labels)
	}

	nsSelector, ok := spec["namespaceSelector"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected namespaceSelector map, got %T", spec["namespaceSelector"])
	}
	if !reflect.DeepEqual(nsSelector["matchNames"], []interface{}{"sm-ns"}) {
		t.Errorf("matchNames = %v, want [sm-ns]", nsSelector["matchNames"])
	}

	endpoints, ok := spec["endpoints"].([]interface{})
	if !ok || len(endpoints) != 1 {
		t.Fatalf("expected 1 endpoint, got %+v", spec["endpoints"])
	}
	ep, ok := endpoints[0].(map[string]interface{})
	if !ok || ep["port"] != "metrics" || ep["path"] != "/metrics" || ep["interval"] != "30s" {
		t.Errorf("unexpected endpoint: %+v", ep)
	}
}

func TestResolveBackendURL(t *testing.T) {
	tests := []struct {
		name string
		spec ojsv1alpha1.BackendSpec
		want string
	}{
		{"explicit URL wins", ojsv1alpha1.BackendSpec{URL: "redis://explicit:6379", Embedded: true, Type: "redis"}, "redis://explicit:6379"},
		{"embedded redis", ojsv1alpha1.BackendSpec{Type: "redis", Embedded: true}, "redis://c-redis.ns.svc.cluster.local:6379"},
		{"embedded postgres", ojsv1alpha1.BackendSpec{Type: "postgres", Embedded: true}, "postgres://c-postgres.ns.svc.cluster.local:5432/ojs?sslmode=disable"},
		{"embedded nats", ojsv1alpha1.BackendSpec{Type: "nats", Embedded: true}, "nats://c-nats.ns.svc.cluster.local:4222"},
		{"embedded kafka", ojsv1alpha1.BackendSpec{Type: "kafka", Embedded: true}, "c-kafka.ns.svc.cluster.local:9092"},
		{"embedded sqs", ojsv1alpha1.BackendSpec{Type: "sqs", Embedded: true}, "https://sqs.c-sqs.ns.svc.cluster.local"},
		{"non-embedded, no URL", ojsv1alpha1.BackendSpec{Type: "redis"}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cluster := &ojsv1alpha1.OJSCluster{
				ObjectMeta: metav1.ObjectMeta{Name: "c", Namespace: "ns"},
				Spec:       ojsv1alpha1.OJSClusterSpec{Backend: tt.spec},
			}
			if got := resolveBackendURL(cluster); got != tt.want {
				t.Errorf("resolveBackendURL() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestBuildPodSecurityContext_Defaults(t *testing.T) {
	cluster := baseCluster()
	sc := buildPodSecurityContext(cluster)

	if sc.RunAsNonRoot == nil || !*sc.RunAsNonRoot {
		t.Error("expected RunAsNonRoot=true by default")
	}
	if sc.RunAsUser == nil || *sc.RunAsUser != 65534 {
		t.Errorf("RunAsUser = %v, want 65534", sc.RunAsUser)
	}
	if sc.RunAsGroup == nil || *sc.RunAsGroup != 65534 {
		t.Errorf("RunAsGroup = %v, want 65534", sc.RunAsGroup)
	}
	if sc.FSGroup != nil {
		t.Errorf("expected FSGroup unset by default, got %v", sc.FSGroup)
	}
	if sc.SeccompProfile == nil || sc.SeccompProfile.Type != corev1.SeccompProfileTypeRuntimeDefault {
		t.Errorf("unexpected seccomp profile: %+v", sc.SeccompProfile)
	}
}

func TestBuildPodSecurityContext_FSGroupOverride(t *testing.T) {
	cluster := baseCluster()
	fsGroup := int64(2000)
	cluster.Spec.SecurityContext = &ojsv1alpha1.PodSecuritySpec{FSGroup: &fsGroup}

	sc := buildPodSecurityContext(cluster)
	if sc.FSGroup == nil || *sc.FSGroup != 2000 {
		t.Errorf("FSGroup = %v, want 2000", sc.FSGroup)
	}
}

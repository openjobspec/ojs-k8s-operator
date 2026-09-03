package controller

import (
	"reflect"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	ojsv1alpha1 "github.com/openjobspec/ojs-k8s-operator/api/v1alpha1"
)

// Characterization tests for the pure OJSWorker desired-state builders.

func baseWorker() (*ojsv1alpha1.OJSWorker, *ojsv1alpha1.OJSCluster) {
	cluster := &ojsv1alpha1.OJSCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "snap-cluster", Namespace: "snap-ns"},
	}
	worker := &ojsv1alpha1.OJSWorker{
		ObjectMeta: metav1.ObjectMeta{Name: "snap-worker", Namespace: "snap-ns"},
		Spec: ojsv1alpha1.OJSWorkerSpec{
			ClusterRef: "snap-cluster",
			JobTypes:   []string{"email.send"},
			Queues:     []string{"default"},
			Image:      "ojs-worker:test",
		},
	}
	return worker, cluster
}

func TestApplyWorkerDeploymentSpec_Defaults(t *testing.T) {
	worker, cluster := baseWorker()
	dep := &appsv1.Deployment{}

	applyWorkerDeploymentSpec(dep, worker, cluster)

	wantLabels := map[string]string{
		"app.kubernetes.io/name":       "ojs-worker",
		"app.kubernetes.io/instance":   "snap-worker",
		"app.kubernetes.io/component":  "worker",
		"app.kubernetes.io/part-of":    "ojs",
		"app.kubernetes.io/managed-by": "ojs-k8s-operator",
		"ojs.openjobspec.dev/cluster":  "snap-cluster",
	}
	if !reflect.DeepEqual(dep.Labels, wantLabels) {
		t.Errorf("labels = %v, want %v", dep.Labels, wantLabels)
	}
	if dep.Spec.Replicas == nil || *dep.Spec.Replicas != 1 {
		t.Errorf("default replicas = %v, want 1", dep.Spec.Replicas)
	}
	if !reflect.DeepEqual(dep.Spec.Selector.MatchLabels, wantLabels) {
		t.Errorf("selector = %v, want %v", dep.Spec.Selector.MatchLabels, wantLabels)
	}

	c := dep.Spec.Template.Spec.Containers[0]
	if c.Name != "ojs-worker" || c.Image != "ojs-worker:test" {
		t.Errorf("unexpected container: %+v", c)
	}

	wantEnv := []corev1.EnvVar{
		{Name: "OJS_URL", Value: "http://snap-cluster-server.snap-ns.svc.cluster.local:8080"},
		{Name: "OJS_QUEUES", Value: "default"},
		{Name: "OJS_JOB_TYPES", Value: "email.send"},
	}
	if !reflect.DeepEqual(c.Env, wantEnv) {
		t.Errorf("env = %+v, want %+v", c.Env, wantEnv)
	}

	if dep.Spec.Template.Spec.TerminationGracePeriodSeconds == nil ||
		*dep.Spec.Template.Spec.TerminationGracePeriodSeconds != 30 {
		t.Errorf("expected default termination grace period 30s, got %v",
			dep.Spec.Template.Spec.TerminationGracePeriodSeconds)
	}
}

func TestApplyWorkerDeploymentSpec_AutoscalingCreateUsesReplicaBounds(t *testing.T) {
	tests := []struct {
		name     string
		replicas *int32
		min      int32
		max      int32
		want     int32
	}{
		{name: "omitted replicas uses minimum", min: 3, max: 10, want: 3},
		{name: "explicit replicas within range", replicas: int32Ptr(5), min: 3, max: 10, want: 5},
		{name: "explicit replicas below minimum", replicas: int32Ptr(1), min: 3, max: 10, want: 3},
		{name: "explicit replicas above maximum", replicas: int32Ptr(12), min: 3, max: 10, want: 10},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			worker, cluster := baseWorker()
			worker.Spec.Replicas = tt.replicas
			worker.Spec.AutoScaling = &ojsv1alpha1.WorkerAutoScalingSpec{
				Enabled:     true,
				MinReplicas: tt.min,
				MaxReplicas: tt.max,
			}
			dep := &appsv1.Deployment{}

			applyWorkerDeploymentSpec(dep, worker, cluster)

			if dep.Spec.Replicas == nil || *dep.Spec.Replicas != tt.want {
				t.Errorf("initial replicas = %v, want %d", dep.Spec.Replicas, tt.want)
			}
		})
	}
}

func TestApplyWorkerDeploymentSpec_AutoscalingUpdatePreservesReplicas(t *testing.T) {
	worker, cluster := baseWorker()
	worker.Spec.Replicas = int32Ptr(2)
	worker.Spec.AutoScaling = &ojsv1alpha1.WorkerAutoScalingSpec{
		Enabled:     true,
		MinReplicas: 1,
		MaxReplicas: 10,
	}
	hpaReplicas := int32(7)
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{ResourceVersion: "existing"},
		Spec:       appsv1.DeploymentSpec{Replicas: &hpaReplicas},
	}

	applyWorkerDeploymentSpec(dep, worker, cluster)

	if dep.Spec.Replicas == nil || *dep.Spec.Replicas != 7 {
		t.Errorf("autoscaling update replicas = %v, want preserved value 7", dep.Spec.Replicas)
	}
}

func TestApplyWorkerDeploymentSpec_AutoscalingDisabledResumesReplicaManagement(t *testing.T) {
	worker, cluster := baseWorker()
	worker.Spec.Replicas = int32Ptr(3)
	worker.Spec.AutoScaling = &ojsv1alpha1.WorkerAutoScalingSpec{Enabled: false}
	hpaReplicas := int32(7)
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{ResourceVersion: "existing"},
		Spec:       appsv1.DeploymentSpec{Replicas: &hpaReplicas},
	}

	applyWorkerDeploymentSpec(dep, worker, cluster)

	if dep.Spec.Replicas == nil || *dep.Spec.Replicas != 3 {
		t.Errorf("disabled autoscaling replicas = %v, want spec value 3", dep.Spec.Replicas)
	}
}

func TestApplyWorkerDeploymentSpec_Concurrency(t *testing.T) {
	worker, cluster := baseWorker()
	worker.Spec.Concurrency = 5
	dep := &appsv1.Deployment{}

	applyWorkerDeploymentSpec(dep, worker, cluster)

	c := dep.Spec.Template.Spec.Containers[0]
	if len(c.Env) != 4 || c.Env[3].Name != "OJS_CONCURRENCY" || c.Env[3].Value != "5" {
		t.Errorf("expected OJS_CONCURRENCY=5 appended, got %+v", c.Env)
	}
}

func TestApplyWorkerDeploymentSpec_ExtraEnvAppendedLast(t *testing.T) {
	worker, cluster := baseWorker()
	worker.Spec.Env = []corev1.EnvVar{{Name: "CUSTOM", Value: "1"}}
	dep := &appsv1.Deployment{}

	applyWorkerDeploymentSpec(dep, worker, cluster)

	c := dep.Spec.Template.Spec.Containers[0]
	last := c.Env[len(c.Env)-1]
	if last.Name != "CUSTOM" || last.Value != "1" {
		t.Errorf("expected user Env appended last, got %+v", c.Env)
	}
}

func TestApplyWorkerDeploymentSpec_CommandOverride(t *testing.T) {
	worker, cluster := baseWorker()
	worker.Spec.Command = []string{"custom-entrypoint", "--flag"}
	dep := &appsv1.Deployment{}

	applyWorkerDeploymentSpec(dep, worker, cluster)

	c := dep.Spec.Template.Spec.Containers[0]
	if !reflect.DeepEqual(c.Command, worker.Spec.Command) {
		t.Errorf("command = %+v, want %+v", c.Command, worker.Spec.Command)
	}
}

func TestApplyWorkerDeploymentSpec_ResourceOverrides(t *testing.T) {
	worker, cluster := baseWorker()
	worker.Spec.Resources = corev1.ResourceRequirements{
		Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("100m")},
	}
	dep := &appsv1.Deployment{}

	applyWorkerDeploymentSpec(dep, worker, cluster)

	c := dep.Spec.Template.Spec.Containers[0]
	if !reflect.DeepEqual(c.Resources, worker.Spec.Resources) {
		t.Errorf("resources = %+v, want %+v", c.Resources, worker.Spec.Resources)
	}
}

func TestApplyWorkerDeploymentSpec_GracefulShutdownOverride(t *testing.T) {
	worker, cluster := baseWorker()
	worker.Spec.GracefulShutdown = &ojsv1alpha1.GracefulShutdownSpec{TimeoutSeconds: 90}
	dep := &appsv1.Deployment{}

	applyWorkerDeploymentSpec(dep, worker, cluster)

	if *dep.Spec.Template.Spec.TerminationGracePeriodSeconds != 90 {
		t.Errorf("termination grace period = %v, want 90",
			*dep.Spec.Template.Spec.TerminationGracePeriodSeconds)
	}
}

func TestApplyWorkerDeploymentSpec_GracefulShutdownZeroUsesDefault(t *testing.T) {
	worker, cluster := baseWorker()
	worker.Spec.GracefulShutdown = &ojsv1alpha1.GracefulShutdownSpec{TimeoutSeconds: 0}
	dep := &appsv1.Deployment{}

	applyWorkerDeploymentSpec(dep, worker, cluster)

	if *dep.Spec.Template.Spec.TerminationGracePeriodSeconds != 30 {
		t.Errorf("expected default 30s when TimeoutSeconds is 0, got %v",
			*dep.Spec.Template.Spec.TerminationGracePeriodSeconds)
	}
}

func TestJoinStrings_Serialization(t *testing.T) {
	tests := []struct {
		input []string
		want  string
	}{
		{nil, ""},
		{[]string{}, ""},
		{[]string{"a"}, "a"},
		{[]string{"a", "b", "c"}, "a,b,c"},
	}
	for _, tt := range tests {
		if got := joinStrings(tt.input); got != tt.want {
			t.Errorf("joinStrings(%v) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestLabelsForWorker_Snapshot(t *testing.T) {
	got := labelsForWorker("w1", "c1")
	want := map[string]string{
		"app.kubernetes.io/name":       "ojs-worker",
		"app.kubernetes.io/instance":   "w1",
		"app.kubernetes.io/component":  "worker",
		"app.kubernetes.io/part-of":    "ojs",
		"app.kubernetes.io/managed-by": "ojs-k8s-operator",
		"ojs.openjobspec.dev/cluster":  "c1",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("labelsForWorker() = %v, want %v", got, want)
	}
}

func TestDesiredHPASpec(t *testing.T) {
	worker, _ := baseWorker()
	worker.Spec.AutoScaling = &ojsv1alpha1.WorkerAutoScalingSpec{
		Enabled:     true,
		MinReplicas: 2,
		MaxReplicas: 10,
	}

	spec := desiredHPASpec(worker)

	if spec.ScaleTargetRef.APIVersion != "apps/v1" || spec.ScaleTargetRef.Kind != "Deployment" ||
		spec.ScaleTargetRef.Name != "snap-worker" {
		t.Errorf("unexpected scale target ref: %+v", spec.ScaleTargetRef)
	}
	if spec.MinReplicas == nil || *spec.MinReplicas != 2 {
		t.Errorf("MinReplicas = %v, want 2", spec.MinReplicas)
	}
	if spec.MaxReplicas != 10 {
		t.Errorf("MaxReplicas = %d, want 10", spec.MaxReplicas)
	}
	if len(spec.Metrics) != 1 || spec.Metrics[0].Type != autoscalingv2.ResourceMetricSourceType {
		t.Errorf("unexpected metrics: %+v", spec.Metrics)
	}
	target := spec.Metrics[0].Resource.Target
	if target.AverageUtilization == nil || *target.AverageUtilization != 80 {
		t.Errorf("expected 80%% CPU utilization target, got %v", target.AverageUtilization)
	}
}

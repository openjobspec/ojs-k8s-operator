package controller

import (
	"context"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	ojsv1alpha1 "github.com/openjobspec/ojs-k8s-operator/api/v1alpha1"
)

func newWorkerScheme() *runtime.Scheme {
	s := newScheme()
	_ = autoscalingv2.AddToScheme(s)
	return s
}

func TestWorkerReconcile_CreatesDeployment(t *testing.T) {
	scheme := newWorkerScheme()

	cluster := &ojsv1alpha1.OJSCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "test-cluster", Namespace: "default"},
		Spec: ojsv1alpha1.OJSClusterSpec{
			Backend: ojsv1alpha1.BackendSpec{Type: "redis", URL: "redis://localhost:6379"},
		},
	}
	worker := &ojsv1alpha1.OJSWorker{
		ObjectMeta: metav1.ObjectMeta{Name: "test-worker", Namespace: "default"},
		Spec: ojsv1alpha1.OJSWorkerSpec{
			ClusterRef:  "test-cluster",
			JobTypes:    []string{"email.send"},
			Image:       "worker:latest",
			Replicas:    int32Ptr(2),
			Concurrency: 5,
		},
	}

	cl := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(cluster, worker).
		WithStatusSubresource(cluster, worker).
		Build()

	r := &OJSWorkerReconciler{Client: cl, Scheme: scheme}
	req := reconcile.Request{NamespacedName: types.NamespacedName{Name: "test-worker", Namespace: "default"}}

	// First reconcile: adds finalizer
	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("first reconcile failed: %v", err)
	}
	// Second reconcile: creates resources
	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("second reconcile failed: %v", err)
	}

	dep := &appsv1.Deployment{}
	if err := cl.Get(context.Background(), types.NamespacedName{Name: "test-worker", Namespace: "default"}, dep); err != nil {
		t.Fatalf("expected Deployment to be created: %v", err)
	}
	if *dep.Spec.Replicas != 2 {
		t.Errorf("expected 2 replicas, got %d", *dep.Spec.Replicas)
	}
	if dep.Spec.Template.Spec.Containers[0].Image != "worker:latest" {
		t.Errorf("expected image worker:latest, got %s", dep.Spec.Template.Spec.Containers[0].Image)
	}
}

func TestWorkerReconcile_DefaultReplicas(t *testing.T) {
	scheme := newWorkerScheme()

	cluster := &ojsv1alpha1.OJSCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "cluster", Namespace: "default"},
		Spec: ojsv1alpha1.OJSClusterSpec{
			Backend: ojsv1alpha1.BackendSpec{Type: "redis", URL: "redis://localhost:6379"},
		},
	}
	worker := &ojsv1alpha1.OJSWorker{
		ObjectMeta: metav1.ObjectMeta{Name: "default-rep-worker", Namespace: "default"},
		Spec: ojsv1alpha1.OJSWorkerSpec{
			ClusterRef: "cluster",
			JobTypes:   []string{"job.type"},
			Image:      "worker:latest",
		},
	}

	cl := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(cluster, worker).
		WithStatusSubresource(cluster, worker).
		Build()

	r := &OJSWorkerReconciler{Client: cl, Scheme: scheme}
	req := reconcile.Request{NamespacedName: types.NamespacedName{Name: "default-rep-worker", Namespace: "default"}}

	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("first reconcile failed: %v", err)
	}
	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("second reconcile failed: %v", err)
	}

	dep := &appsv1.Deployment{}
	if err := cl.Get(context.Background(), types.NamespacedName{Name: "default-rep-worker", Namespace: "default"}, dep); err != nil {
		t.Fatalf("expected Deployment: %v", err)
	}
	if *dep.Spec.Replicas != 1 {
		t.Errorf("expected default 1 replica, got %d", *dep.Spec.Replicas)
	}
}

func TestWorkerReconcile_NotFound(t *testing.T) {
	scheme := newWorkerScheme()
	cl := fake.NewClientBuilder().WithScheme(scheme).Build()

	r := &OJSWorkerReconciler{Client: cl, Scheme: scheme}
	req := reconcile.Request{NamespacedName: types.NamespacedName{Name: "nonexistent", Namespace: "default"}}

	result, err := r.Reconcile(context.Background(), req)
	if err != nil {
		t.Fatalf("expected no error for not-found, got: %v", err)
	}
	if result != (reconcile.Result{}) {
		t.Fatalf("expected empty result, got: %v", result)
	}
}

func TestWorkerReconcile_Deletion(t *testing.T) {
	scheme := newWorkerScheme()
	now := metav1.Now()

	worker := &ojsv1alpha1.OJSWorker{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "delete-worker",
			Namespace:         "default",
			DeletionTimestamp: &now,
			Finalizers:        []string{workerFinalizer},
		},
		Spec: ojsv1alpha1.OJSWorkerSpec{
			ClusterRef: "cluster",
			JobTypes:   []string{"job.type"},
			Image:      "worker:latest",
		},
	}

	cl := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(worker).
		Build()

	r := &OJSWorkerReconciler{Client: cl, Scheme: scheme}
	req := reconcile.Request{NamespacedName: types.NamespacedName{Name: "delete-worker", Namespace: "default"}}

	_, err := r.Reconcile(context.Background(), req)
	if err != nil {
		t.Fatalf("reconcile failed: %v", err)
	}

	// After finalizer removal with deletion timestamp set, the fake client
	// may garbage-collect the object. Either it's gone or the finalizer is removed.
	updated := &ojsv1alpha1.OJSWorker{}
	if err := cl.Get(context.Background(), req.NamespacedName, updated); err != nil {
		// Object was garbage-collected — finalizer removal succeeded
		return
	}
	for _, f := range updated.Finalizers {
		if f == workerFinalizer {
			t.Error("expected finalizer to be removed")
		}
	}
}

func TestWorkerReconcile_EnvVars(t *testing.T) {
	scheme := newWorkerScheme()

	cluster := &ojsv1alpha1.OJSCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "env-cluster", Namespace: "default"},
		Spec: ojsv1alpha1.OJSClusterSpec{
			Backend: ojsv1alpha1.BackendSpec{Type: "redis", URL: "redis://localhost:6379"},
		},
	}
	worker := &ojsv1alpha1.OJSWorker{
		ObjectMeta: metav1.ObjectMeta{Name: "env-worker", Namespace: "default"},
		Spec: ojsv1alpha1.OJSWorkerSpec{
			ClusterRef:  "env-cluster",
			JobTypes:    []string{"email.send", "sms.send"},
			Queues:      []string{"high", "low"},
			Concurrency: 15,
			Image:       "worker:latest",
			Env: []corev1.EnvVar{
				{Name: "CUSTOM_VAR", Value: "custom-value"},
			},
		},
	}

	cl := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(cluster, worker).
		WithStatusSubresource(cluster, worker).
		Build()

	r := &OJSWorkerReconciler{Client: cl, Scheme: scheme}
	req := reconcile.Request{NamespacedName: types.NamespacedName{Name: "env-worker", Namespace: "default"}}

	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("first reconcile failed: %v", err)
	}
	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("second reconcile failed: %v", err)
	}

	dep := &appsv1.Deployment{}
	if err := cl.Get(context.Background(), types.NamespacedName{Name: "env-worker", Namespace: "default"}, dep); err != nil {
		t.Fatalf("expected Deployment: %v", err)
	}

	envMap := map[string]string{}
	for _, env := range dep.Spec.Template.Spec.Containers[0].Env {
		envMap[env.Name] = env.Value
	}

	if envMap["OJS_QUEUES"] != "high,low" {
		t.Errorf("expected OJS_QUEUES=high,low, got %s", envMap["OJS_QUEUES"])
	}
	if envMap["OJS_JOB_TYPES"] != "email.send,sms.send" {
		t.Errorf("expected OJS_JOB_TYPES=email.send,sms.send, got %s", envMap["OJS_JOB_TYPES"])
	}
	if envMap["OJS_CONCURRENCY"] != "15" {
		t.Errorf("expected OJS_CONCURRENCY=15, got %s", envMap["OJS_CONCURRENCY"])
	}
	if envMap["CUSTOM_VAR"] != "custom-value" {
		t.Errorf("expected CUSTOM_VAR=custom-value, got %s", envMap["CUSTOM_VAR"])
	}
}

func TestWorkerReconcile_CommandOverride(t *testing.T) {
	scheme := newWorkerScheme()

	cluster := &ojsv1alpha1.OJSCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "cmd-cluster", Namespace: "default"},
		Spec: ojsv1alpha1.OJSClusterSpec{
			Backend: ojsv1alpha1.BackendSpec{Type: "redis", URL: "redis://localhost:6379"},
		},
	}
	worker := &ojsv1alpha1.OJSWorker{
		ObjectMeta: metav1.ObjectMeta{Name: "cmd-worker", Namespace: "default"},
		Spec: ojsv1alpha1.OJSWorkerSpec{
			ClusterRef: "cmd-cluster",
			JobTypes:   []string{"job.type"},
			Image:      "worker:latest",
			Command:    []string{"/bin/sh", "-c", "worker start"},
		},
	}

	cl := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(cluster, worker).
		WithStatusSubresource(cluster, worker).
		Build()

	r := &OJSWorkerReconciler{Client: cl, Scheme: scheme}
	req := reconcile.Request{NamespacedName: types.NamespacedName{Name: "cmd-worker", Namespace: "default"}}

	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("first reconcile failed: %v", err)
	}
	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("second reconcile failed: %v", err)
	}

	dep := &appsv1.Deployment{}
	if err := cl.Get(context.Background(), types.NamespacedName{Name: "cmd-worker", Namespace: "default"}, dep); err != nil {
		t.Fatalf("expected Deployment: %v", err)
	}

	container := dep.Spec.Template.Spec.Containers[0]
	if len(container.Command) != 3 || container.Command[0] != "/bin/sh" {
		t.Errorf("expected command override, got %v", container.Command)
	}
}

func TestWorkerReconcile_ResourceRequirements(t *testing.T) {
	scheme := newWorkerScheme()

	cluster := &ojsv1alpha1.OJSCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "res-cluster", Namespace: "default"},
		Spec: ojsv1alpha1.OJSClusterSpec{
			Backend: ojsv1alpha1.BackendSpec{Type: "redis", URL: "redis://localhost:6379"},
		},
	}
	worker := &ojsv1alpha1.OJSWorker{
		ObjectMeta: metav1.ObjectMeta{Name: "res-worker", Namespace: "default"},
		Spec: ojsv1alpha1.OJSWorkerSpec{
			ClusterRef: "res-cluster",
			JobTypes:   []string{"job.type"},
			Image:      "worker:latest",
			Resources: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("200m"),
					corev1.ResourceMemory: resource.MustParse("256Mi"),
				},
				Limits: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("1"),
					corev1.ResourceMemory: resource.MustParse("1Gi"),
				},
			},
		},
	}

	cl := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(cluster, worker).
		WithStatusSubresource(cluster, worker).
		Build()

	r := &OJSWorkerReconciler{Client: cl, Scheme: scheme}
	req := reconcile.Request{NamespacedName: types.NamespacedName{Name: "res-worker", Namespace: "default"}}

	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("first reconcile failed: %v", err)
	}
	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("second reconcile failed: %v", err)
	}

	dep := &appsv1.Deployment{}
	if err := cl.Get(context.Background(), types.NamespacedName{Name: "res-worker", Namespace: "default"}, dep); err != nil {
		t.Fatalf("expected Deployment: %v", err)
	}

	container := dep.Spec.Template.Spec.Containers[0]
	cpuReq := container.Resources.Requests[corev1.ResourceCPU]
	if cpuReq.String() != "200m" {
		t.Errorf("expected CPU request 200m, got %s", cpuReq.String())
	}
	memLimit := container.Resources.Limits[corev1.ResourceMemory]
	if memLimit.String() != "1Gi" {
		t.Errorf("expected memory limit 1Gi, got %s", memLimit.String())
	}
}

func TestWorkerReconcile_GracefulShutdown(t *testing.T) {
	scheme := newWorkerScheme()

	cluster := &ojsv1alpha1.OJSCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "gs-cluster", Namespace: "default"},
		Spec: ojsv1alpha1.OJSClusterSpec{
			Backend: ojsv1alpha1.BackendSpec{Type: "redis", URL: "redis://localhost:6379"},
		},
	}
	worker := &ojsv1alpha1.OJSWorker{
		ObjectMeta: metav1.ObjectMeta{Name: "gs-worker", Namespace: "default"},
		Spec: ojsv1alpha1.OJSWorkerSpec{
			ClusterRef: "gs-cluster",
			JobTypes:   []string{"job.type"},
			Image:      "worker:latest",
			GracefulShutdown: &ojsv1alpha1.GracefulShutdownSpec{
				TimeoutSeconds:      120,
				DrainBeforeShutdown: true,
			},
		},
	}

	cl := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(cluster, worker).
		WithStatusSubresource(cluster, worker).
		Build()

	r := &OJSWorkerReconciler{Client: cl, Scheme: scheme}
	req := reconcile.Request{NamespacedName: types.NamespacedName{Name: "gs-worker", Namespace: "default"}}

	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("first reconcile failed: %v", err)
	}
	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("second reconcile failed: %v", err)
	}

	dep := &appsv1.Deployment{}
	if err := cl.Get(context.Background(), types.NamespacedName{Name: "gs-worker", Namespace: "default"}, dep); err != nil {
		t.Fatalf("expected Deployment: %v", err)
	}

	if *dep.Spec.Template.Spec.TerminationGracePeriodSeconds != 120 {
		t.Errorf("expected termination grace period 120, got %d", *dep.Spec.Template.Spec.TerminationGracePeriodSeconds)
	}
}

func TestWorkerReconcile_OwnerReferences(t *testing.T) {
	scheme := newWorkerScheme()

	cluster := &ojsv1alpha1.OJSCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "own-cluster", Namespace: "default"},
		Spec: ojsv1alpha1.OJSClusterSpec{
			Backend: ojsv1alpha1.BackendSpec{Type: "redis", URL: "redis://localhost:6379"},
		},
	}
	worker := &ojsv1alpha1.OJSWorker{
		ObjectMeta: metav1.ObjectMeta{Name: "own-worker", Namespace: "default"},
		Spec: ojsv1alpha1.OJSWorkerSpec{
			ClusterRef: "own-cluster",
			JobTypes:   []string{"job.type"},
			Image:      "worker:latest",
		},
	}

	cl := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(cluster, worker).
		WithStatusSubresource(cluster, worker).
		Build()

	r := &OJSWorkerReconciler{Client: cl, Scheme: scheme}
	req := reconcile.Request{NamespacedName: types.NamespacedName{Name: "own-worker", Namespace: "default"}}

	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("first reconcile failed: %v", err)
	}
	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("second reconcile failed: %v", err)
	}

	dep := &appsv1.Deployment{}
	if err := cl.Get(context.Background(), types.NamespacedName{Name: "own-worker", Namespace: "default"}, dep); err != nil {
		t.Fatalf("expected Deployment: %v", err)
	}
	if len(dep.OwnerReferences) == 0 {
		t.Error("expected owner reference on deployment")
	}
}

func TestWorkerReconcile_HPACreatedWhenEnabled(t *testing.T) {
	scheme := newWorkerScheme()

	cluster := &ojsv1alpha1.OJSCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "hpa-cluster", Namespace: "default"},
		Spec: ojsv1alpha1.OJSClusterSpec{
			Backend: ojsv1alpha1.BackendSpec{Type: "redis", URL: "redis://localhost:6379"},
		},
	}
	worker := &ojsv1alpha1.OJSWorker{
		ObjectMeta: metav1.ObjectMeta{Name: "hpa-worker", Namespace: "default"},
		Spec: ojsv1alpha1.OJSWorkerSpec{
			ClusterRef: "hpa-cluster",
			JobTypes:   []string{"job.type"},
			Image:      "worker:latest",
			AutoScaling: &ojsv1alpha1.WorkerAutoScalingSpec{
				Enabled:     true,
				MinReplicas: 2,
				MaxReplicas: 8,
			},
		},
	}

	cl := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(cluster, worker).
		WithStatusSubresource(cluster, worker).
		Build()

	r := &OJSWorkerReconciler{Client: cl, Scheme: scheme}
	req := reconcile.Request{NamespacedName: types.NamespacedName{Name: "hpa-worker", Namespace: "default"}}

	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("first reconcile failed: %v", err)
	}
	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("second reconcile failed: %v", err)
	}

	hpa := &autoscalingv2.HorizontalPodAutoscaler{}
	if err := cl.Get(context.Background(), types.NamespacedName{Name: "hpa-worker-hpa", Namespace: "default"}, hpa); err != nil {
		t.Fatalf("expected HPA: %v", err)
	}
	if *hpa.Spec.MinReplicas != 2 {
		t.Errorf("expected minReplicas 2, got %d", *hpa.Spec.MinReplicas)
	}
	if hpa.Spec.MaxReplicas != 8 {
		t.Errorf("expected maxReplicas 8, got %d", hpa.Spec.MaxReplicas)
	}
}

func TestWorkerReconcile_NoHPAWhenDisabled(t *testing.T) {
	scheme := newWorkerScheme()

	cluster := &ojsv1alpha1.OJSCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "nohpa-cluster", Namespace: "default"},
		Spec: ojsv1alpha1.OJSClusterSpec{
			Backend: ojsv1alpha1.BackendSpec{Type: "redis", URL: "redis://localhost:6379"},
		},
	}
	worker := &ojsv1alpha1.OJSWorker{
		ObjectMeta: metav1.ObjectMeta{Name: "nohpa-worker", Namespace: "default"},
		Spec: ojsv1alpha1.OJSWorkerSpec{
			ClusterRef: "nohpa-cluster",
			JobTypes:   []string{"job.type"},
			Image:      "worker:latest",
		},
	}

	cl := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(cluster, worker).
		WithStatusSubresource(cluster, worker).
		Build()

	r := &OJSWorkerReconciler{Client: cl, Scheme: scheme}
	req := reconcile.Request{NamespacedName: types.NamespacedName{Name: "nohpa-worker", Namespace: "default"}}

	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("first reconcile failed: %v", err)
	}
	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("second reconcile failed: %v", err)
	}

	hpa := &autoscalingv2.HorizontalPodAutoscaler{}
	err := cl.Get(context.Background(), types.NamespacedName{Name: "nohpa-worker-hpa", Namespace: "default"}, hpa)
	if err == nil {
		t.Error("expected no HPA when autoscaling is nil")
	}
}

func TestWorkerReconcile_Labels(t *testing.T) {
	labels := labelsForWorker("my-worker", "my-cluster")

	expected := map[string]string{
		"app.kubernetes.io/name":       "ojs-worker",
		"app.kubernetes.io/instance":   "my-worker",
		"app.kubernetes.io/component":  "worker",
		"app.kubernetes.io/part-of":    "ojs",
		"app.kubernetes.io/managed-by": "ojs-k8s-operator",
		"ojs.openjobspec.dev/cluster":  "my-cluster",
	}

	for k, v := range expected {
		if labels[k] != v {
			t.Errorf("label %q = %q, want %q", k, labels[k], v)
		}
	}
}

func TestJoinStrings(t *testing.T) {
	tests := []struct {
		input    []string
		expected string
	}{
		{nil, ""},
		{[]string{}, ""},
		{[]string{"a"}, "a"},
		{[]string{"a", "b", "c"}, "a,b,c"},
	}
	for _, tt := range tests {
		got := joinStrings(tt.input)
		if got != tt.expected {
			t.Errorf("joinStrings(%v) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

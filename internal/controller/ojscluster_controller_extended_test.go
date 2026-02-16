package controller

import (
	"context"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	ojsv1alpha1 "github.com/openjobspec/ojs-k8s-operator/api/v1alpha1"
)

func TestReconcile_NotFound(t *testing.T) {
	scheme := newScheme()
	client := fake.NewClientBuilder().WithScheme(scheme).Build()

	r := &OJSClusterReconciler{Client: client, Scheme: scheme}
	req := reconcile.Request{
		NamespacedName: types.NamespacedName{Name: "nonexistent", Namespace: "default"},
	}

	result, err := r.Reconcile(context.Background(), req)
	if err != nil {
		t.Fatalf("expected no error for not-found, got: %v", err)
	}
	if result != (reconcile.Result{}) {
		t.Fatalf("expected empty result, got: %v", result)
	}
}

func TestReconcile_Deletion(t *testing.T) {
	scheme := newScheme()
	now := metav1.Now()

	cluster := &ojsv1alpha1.OJSCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "delete-me",
			Namespace:         "default",
			DeletionTimestamp: &now,
			Finalizers:        []string{ojsFinalizer},
		},
		Spec: ojsv1alpha1.OJSClusterSpec{
			Backend: ojsv1alpha1.BackendSpec{Type: "redis", URL: "redis://localhost:6379"},
		},
	}

	client := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(cluster).
		Build()

	r := &OJSClusterReconciler{Client: client, Scheme: scheme}
	req := reconcile.Request{
		NamespacedName: types.NamespacedName{Name: "delete-me", Namespace: "default"},
	}

	_, err := r.Reconcile(context.Background(), req)
	if err != nil {
		t.Fatalf("deletion reconcile failed: %v", err)
	}

	// Verify finalizer was removed
	updated := &ojsv1alpha1.OJSCluster{}
	if err := client.Get(context.Background(), req.NamespacedName, updated); err != nil {
		t.Fatalf("failed to get updated cluster: %v", err)
	}
	for _, f := range updated.Finalizers {
		if f == ojsFinalizer {
			t.Error("expected finalizer to be removed")
		}
	}
}

func TestReconcile_PostgresBackend(t *testing.T) {
	scheme := newScheme()

	cluster := &ojsv1alpha1.OJSCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pg-cluster",
			Namespace: "default",
		},
		Spec: ojsv1alpha1.OJSClusterSpec{
			Backend: ojsv1alpha1.BackendSpec{
				Type: "postgres",
				URL:  "postgres://localhost:5432/ojs",
			},
			Replicas: int32Ptr(1),
		},
	}

	client := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(cluster).
		WithStatusSubresource(cluster).
		Build()

	r := &OJSClusterReconciler{Client: client, Scheme: scheme}
	req := reconcile.Request{
		NamespacedName: types.NamespacedName{Name: "pg-cluster", Namespace: "default"},
	}

	// First reconcile: adds finalizer
	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("first reconcile failed: %v", err)
	}
	// Second reconcile: creates resources
	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("second reconcile failed: %v", err)
	}

	// Verify ConfigMap has postgres backend type
	cm := &corev1.ConfigMap{}
	if err := client.Get(context.Background(), types.NamespacedName{
		Name: "pg-cluster-config", Namespace: "default",
	}, cm); err != nil {
		t.Fatalf("expected ConfigMap: %v", err)
	}
	if cm.Data["BACKEND_TYPE"] != "postgres" {
		t.Errorf("expected backend type postgres, got %s", cm.Data["BACKEND_TYPE"])
	}
	if cm.Data["BACKEND_URL"] != "postgres://localhost:5432/ojs" {
		t.Errorf("expected backend URL postgres://localhost:5432/ojs, got %s", cm.Data["BACKEND_URL"])
	}
}

func TestReconcile_CustomImage(t *testing.T) {
	scheme := newScheme()

	cluster := &ojsv1alpha1.OJSCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "custom-image",
			Namespace: "default",
		},
		Spec: ojsv1alpha1.OJSClusterSpec{
			Backend: ojsv1alpha1.BackendSpec{
				Type: "redis",
				URL:  "redis://localhost:6379",
			},
			Image: "my-registry/ojs-server:v1.2.3",
		},
	}

	client := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(cluster).
		WithStatusSubresource(cluster).
		Build()

	r := &OJSClusterReconciler{Client: client, Scheme: scheme}
	req := reconcile.Request{
		NamespacedName: types.NamespacedName{Name: "custom-image", Namespace: "default"},
	}

	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("first reconcile failed: %v", err)
	}
	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("second reconcile failed: %v", err)
	}

	dep := &appsv1.Deployment{}
	if err := client.Get(context.Background(), types.NamespacedName{
		Name: "custom-image-server", Namespace: "default",
	}, dep); err != nil {
		t.Fatalf("expected Deployment: %v", err)
	}
	if dep.Spec.Template.Spec.Containers[0].Image != "my-registry/ojs-server:v1.2.3" {
		t.Errorf("expected custom image, got %s", dep.Spec.Template.Spec.Containers[0].Image)
	}
}

func TestReconcile_DefaultImage(t *testing.T) {
	scheme := newScheme()

	cluster := &ojsv1alpha1.OJSCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "default-image",
			Namespace: "default",
		},
		Spec: ojsv1alpha1.OJSClusterSpec{
			Backend: ojsv1alpha1.BackendSpec{
				Type: "redis",
				URL:  "redis://localhost:6379",
			},
		},
	}

	client := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(cluster).
		WithStatusSubresource(cluster).
		Build()

	r := &OJSClusterReconciler{Client: client, Scheme: scheme}
	req := reconcile.Request{
		NamespacedName: types.NamespacedName{Name: "default-image", Namespace: "default"},
	}

	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("first reconcile failed: %v", err)
	}
	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("second reconcile failed: %v", err)
	}

	dep := &appsv1.Deployment{}
	if err := client.Get(context.Background(), types.NamespacedName{
		Name: "default-image-server", Namespace: "default",
	}, dep); err != nil {
		t.Fatalf("expected Deployment: %v", err)
	}
	if dep.Spec.Template.Spec.Containers[0].Image != defaultImage {
		t.Errorf("expected default image %s, got %s", defaultImage, dep.Spec.Template.Spec.Containers[0].Image)
	}
}

func TestReconcile_ResourceRequirements(t *testing.T) {
	scheme := newScheme()

	cluster := &ojsv1alpha1.OJSCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "with-resources",
			Namespace: "default",
		},
		Spec: ojsv1alpha1.OJSClusterSpec{
			Backend: ojsv1alpha1.BackendSpec{
				Type: "redis",
				URL:  "redis://localhost:6379",
			},
			Resources: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("100m"),
					corev1.ResourceMemory: resource.MustParse("128Mi"),
				},
				Limits: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("500m"),
					corev1.ResourceMemory: resource.MustParse("512Mi"),
				},
			},
		},
	}

	client := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(cluster).
		WithStatusSubresource(cluster).
		Build()

	r := &OJSClusterReconciler{Client: client, Scheme: scheme}
	req := reconcile.Request{
		NamespacedName: types.NamespacedName{Name: "with-resources", Namespace: "default"},
	}

	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("first reconcile failed: %v", err)
	}
	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("second reconcile failed: %v", err)
	}

	dep := &appsv1.Deployment{}
	if err := client.Get(context.Background(), types.NamespacedName{
		Name: "with-resources-server", Namespace: "default",
	}, dep); err != nil {
		t.Fatalf("expected Deployment: %v", err)
	}

	container := dep.Spec.Template.Spec.Containers[0]
	cpuReq := container.Resources.Requests[corev1.ResourceCPU]
	if cpuReq.String() != "100m" {
		t.Errorf("expected CPU request 100m, got %s", cpuReq.String())
	}
	memLimit := container.Resources.Limits[corev1.ResourceMemory]
	if memLimit.String() != "512Mi" {
		t.Errorf("expected memory limit 512Mi, got %s", memLimit.String())
	}
}

func TestReconcile_ServiceCreated(t *testing.T) {
	scheme := newScheme()

	cluster := &ojsv1alpha1.OJSCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "svc-test",
			Namespace: "default",
		},
		Spec: ojsv1alpha1.OJSClusterSpec{
			Backend: ojsv1alpha1.BackendSpec{
				Type: "redis",
				URL:  "redis://localhost:6379",
			},
		},
	}

	client := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(cluster).
		WithStatusSubresource(cluster).
		Build()

	r := &OJSClusterReconciler{Client: client, Scheme: scheme}
	req := reconcile.Request{
		NamespacedName: types.NamespacedName{Name: "svc-test", Namespace: "default"},
	}

	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("first reconcile failed: %v", err)
	}
	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("second reconcile failed: %v", err)
	}

	svc := &corev1.Service{}
	if err := client.Get(context.Background(), types.NamespacedName{
		Name: "svc-test-server", Namespace: "default",
	}, svc); err != nil {
		t.Fatalf("expected Service: %v", err)
	}

	// Verify both HTTP and metrics ports are exposed
	if len(svc.Spec.Ports) != 2 {
		t.Fatalf("expected 2 service ports, got %d", len(svc.Spec.Ports))
	}

	portNames := map[string]int32{}
	for _, p := range svc.Spec.Ports {
		portNames[p.Name] = p.Port
	}
	if portNames["http"] != int32(defaultPort) {
		t.Errorf("expected http port %d, got %d", defaultPort, portNames["http"])
	}
	if portNames["metrics"] != int32(metricsPort) {
		t.Errorf("expected metrics port %d, got %d", metricsPort, portNames["metrics"])
	}
}

func TestReconcile_EmbeddedUnsupportedBackend(t *testing.T) {
	scheme := newScheme()

	cluster := &ojsv1alpha1.OJSCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "unsupported-embedded",
			Namespace:  "default",
			Finalizers: []string{ojsFinalizer},
		},
		Spec: ojsv1alpha1.OJSClusterSpec{
			Backend: ojsv1alpha1.BackendSpec{
				Type:     "nats",
				Embedded: true,
			},
		},
	}

	client := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(cluster).
		WithStatusSubresource(cluster).
		Build()

	r := &OJSClusterReconciler{Client: client, Scheme: scheme}
	req := reconcile.Request{
		NamespacedName: types.NamespacedName{Name: "unsupported-embedded", Namespace: "default"},
	}

	_, err := r.Reconcile(context.Background(), req)
	if err == nil {
		t.Fatal("expected error for unsupported embedded backend type")
	}
}

func TestBackendURLEnvVar(t *testing.T) {
	tests := []struct {
		backendType string
		expected    string
	}{
		{"redis", "REDIS_URL"},
		{"postgres", "DATABASE_URL"},
		{"nats", "REDIS_URL"}, // default case
	}

	for _, tt := range tests {
		t.Run(tt.backendType, func(t *testing.T) {
			got := backendURLEnvVar(tt.backendType)
			if got != tt.expected {
				t.Errorf("backendURLEnvVar(%q) = %q, want %q", tt.backendType, got, tt.expected)
			}
		})
	}
}

func TestLabelsForCluster(t *testing.T) {
	labels := labelsForCluster("my-cluster")

	expected := map[string]string{
		"app.kubernetes.io/name":       "ojs-server",
		"app.kubernetes.io/instance":   "my-cluster",
		"app.kubernetes.io/component":  "server",
		"app.kubernetes.io/part-of":    "ojs",
		"app.kubernetes.io/managed-by": "ojs-k8s-operator",
	}

	for k, v := range expected {
		if labels[k] != v {
			t.Errorf("label %q = %q, want %q", k, labels[k], v)
		}
	}
}

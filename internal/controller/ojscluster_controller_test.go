package controller

import (
	"context"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	ojsv1alpha1 "github.com/openjobspec/ojs-k8s-operator/api/v1alpha1"
)

func newScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(s)
	_ = ojsv1alpha1.AddToScheme(s)
	return s
}

func int32Ptr(i int32) *int32 { return &i }

func TestReconcile_CreatesResources(t *testing.T) {
	scheme := newScheme()

	cluster := &ojsv1alpha1.OJSCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cluster",
			Namespace: "default",
		},
		Spec: ojsv1alpha1.OJSClusterSpec{
			Backend: ojsv1alpha1.BackendSpec{
				Type: "redis",
				URL:  "redis://localhost:6379",
			},
			Replicas: int32Ptr(3),
			Image:    "ojs-server:test",
		},
	}

	client := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(cluster).
		WithStatusSubresource(cluster).
		Build()

	r := &OJSClusterReconciler{
		Client: client,
		Scheme: scheme,
	}

	req := reconcile.Request{
		NamespacedName: types.NamespacedName{
			Name:      "test-cluster",
			Namespace: "default",
		},
	}

	// First reconcile: adds finalizer
	result, err := r.Reconcile(context.Background(), req)
	if err != nil {
		t.Fatalf("first reconcile failed: %v", err)
	}
	if result != (ctrl.Result{}) {
		t.Fatalf("unexpected result: %v", result)
	}

	// Second reconcile: creates resources
	result, err = r.Reconcile(context.Background(), req)
	if err != nil {
		t.Fatalf("second reconcile failed: %v", err)
	}
	if result != (ctrl.Result{}) {
		t.Fatalf("unexpected result: %v", result)
	}

	// Verify Deployment was created
	dep := &appsv1.Deployment{}
	if err := client.Get(context.Background(), types.NamespacedName{
		Name: "test-cluster-server", Namespace: "default",
	}, dep); err != nil {
		t.Fatalf("expected Deployment to be created: %v", err)
	}
	if *dep.Spec.Replicas != 3 {
		t.Errorf("expected 3 replicas, got %d", *dep.Spec.Replicas)
	}
	if dep.Spec.Template.Spec.Containers[0].Image != "ojs-server:test" {
		t.Errorf("expected image ojs-server:test, got %s", dep.Spec.Template.Spec.Containers[0].Image)
	}

	// Verify Service was created
	svc := &corev1.Service{}
	if err := client.Get(context.Background(), types.NamespacedName{
		Name: "test-cluster-server", Namespace: "default",
	}, svc); err != nil {
		t.Fatalf("expected Service to be created: %v", err)
	}

	// Verify ConfigMap was created
	cm := &corev1.ConfigMap{}
	if err := client.Get(context.Background(), types.NamespacedName{
		Name: "test-cluster-config", Namespace: "default",
	}, cm); err != nil {
		t.Fatalf("expected ConfigMap to be created: %v", err)
	}
	if cm.Data["BACKEND_TYPE"] != "redis" {
		t.Errorf("expected backend type redis, got %s", cm.Data["BACKEND_TYPE"])
	}
	if cm.Data["BACKEND_URL"] != "redis://localhost:6379" {
		t.Errorf("expected backend URL redis://localhost:6379, got %s", cm.Data["BACKEND_URL"])
	}
}

func TestReconcile_EmbeddedRedis(t *testing.T) {
	scheme := newScheme()

	cluster := &ojsv1alpha1.OJSCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "embedded-cluster",
			Namespace: "default",
		},
		Spec: ojsv1alpha1.OJSClusterSpec{
			Backend: ojsv1alpha1.BackendSpec{
				Type:     "redis",
				Embedded: true,
			},
		},
	}

	client := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(cluster).
		WithStatusSubresource(cluster).
		Build()

	r := &OJSClusterReconciler{
		Client: client,
		Scheme: scheme,
	}

	req := reconcile.Request{
		NamespacedName: types.NamespacedName{
			Name:      "embedded-cluster",
			Namespace: "default",
		},
	}

	// Two reconciles: first adds finalizer, second creates resources
	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("first reconcile failed: %v", err)
	}
	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("second reconcile failed: %v", err)
	}

	// Verify embedded Redis Deployment
	dep := &appsv1.Deployment{}
	if err := client.Get(context.Background(), types.NamespacedName{
		Name: "embedded-cluster-redis", Namespace: "default",
	}, dep); err != nil {
		t.Fatalf("expected embedded Redis Deployment: %v", err)
	}
	if dep.Spec.Template.Spec.Containers[0].Image != "redis:7-alpine" {
		t.Errorf("expected redis:7-alpine, got %s", dep.Spec.Template.Spec.Containers[0].Image)
	}

	// Verify embedded Redis Service
	svc := &corev1.Service{}
	if err := client.Get(context.Background(), types.NamespacedName{
		Name: "embedded-cluster-redis", Namespace: "default",
	}, svc); err != nil {
		t.Fatalf("expected embedded Redis Service: %v", err)
	}

	// Verify ConfigMap has auto-generated URL
	cm := &corev1.ConfigMap{}
	if err := client.Get(context.Background(), types.NamespacedName{
		Name: "embedded-cluster-config", Namespace: "default",
	}, cm); err != nil {
		t.Fatalf("expected ConfigMap: %v", err)
	}
	expectedURL := "redis://embedded-cluster-redis.default.svc.cluster.local:6379"
	if cm.Data["BACKEND_URL"] != expectedURL {
		t.Errorf("expected URL %s, got %s", expectedURL, cm.Data["BACKEND_URL"])
	}
}

func TestReconcile_DefaultReplicas(t *testing.T) {
	scheme := newScheme()

	cluster := &ojsv1alpha1.OJSCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "default-replicas",
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

	r := &OJSClusterReconciler{
		Client: client,
		Scheme: scheme,
	}

	req := reconcile.Request{
		NamespacedName: types.NamespacedName{
			Name:      "default-replicas",
			Namespace: "default",
		},
	}

	// Two reconciles
	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("first reconcile failed: %v", err)
	}
	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("second reconcile failed: %v", err)
	}

	dep := &appsv1.Deployment{}
	if err := client.Get(context.Background(), types.NamespacedName{
		Name: "default-replicas-server", Namespace: "default",
	}, dep); err != nil {
		t.Fatalf("expected Deployment: %v", err)
	}
	if *dep.Spec.Replicas != 2 {
		t.Errorf("expected default 2 replicas, got %d", *dep.Spec.Replicas)
	}
}

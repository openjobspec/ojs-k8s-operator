package controller

import (
	"context"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	ojsv1alpha1 "github.com/openjobspec/ojs-k8s-operator/api/v1alpha1"
)

func TestClusterStatus_InitialPending(t *testing.T) {
	scheme := newScheme()

	cluster := &ojsv1alpha1.OJSCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "status-pending", Namespace: "default"},
		Spec: ojsv1alpha1.OJSClusterSpec{
			Backend: ojsv1alpha1.BackendSpec{Type: "redis", URL: "redis://localhost:6379"},
		},
	}

	cl := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(cluster).
		WithStatusSubresource(cluster).
		Build()

	r := &OJSClusterReconciler{Client: cl, Scheme: scheme}
	req := reconcile.Request{NamespacedName: types.NamespacedName{Name: "status-pending", Namespace: "default"}}

	// Reconcile twice (finalizer + create resources)
	reconcileClusterN(t, r, req, 2)

	updated := &ojsv1alpha1.OJSCluster{}
	if err := cl.Get(context.Background(), req.NamespacedName, updated); err != nil {
		t.Fatalf("failed to get cluster: %v", err)
	}

	// With fake client, deployment has 0 ready replicas, so phase should be Pending
	if updated.Status.Phase != "Pending" {
		t.Errorf("expected phase Pending, got %s", updated.Status.Phase)
	}
}

func TestClusterStatus_ConditionsSet(t *testing.T) {
	scheme := newScheme()

	cluster := &ojsv1alpha1.OJSCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "cond-cluster", Namespace: "default"},
		Spec: ojsv1alpha1.OJSClusterSpec{
			Backend: ojsv1alpha1.BackendSpec{Type: "redis", URL: "redis://localhost:6379"},
		},
	}

	cl := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(cluster).
		WithStatusSubresource(cluster).
		Build()

	r := &OJSClusterReconciler{Client: cl, Scheme: scheme}
	req := reconcile.Request{NamespacedName: types.NamespacedName{Name: "cond-cluster", Namespace: "default"}}

	reconcileClusterN(t, r, req, 2)

	updated := &ojsv1alpha1.OJSCluster{}
	if err := cl.Get(context.Background(), req.NamespacedName, updated); err != nil {
		t.Fatalf("failed to get cluster: %v", err)
	}

	// Check all four standard conditions exist
	condTypes := []string{condReady, condAvailable, condProgressing, condDegraded}
	for _, ct := range condTypes {
		cond := meta.FindStatusCondition(updated.Status.Conditions, ct)
		if cond == nil {
			t.Errorf("expected condition %s to be set", ct)
		}
	}
}

func TestClusterStatus_RunningPhaseWithReadyReplicas(t *testing.T) {
	scheme := newScheme()

	cluster := &ojsv1alpha1.OJSCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "running-cluster",
			Namespace:  "default",
			Finalizers: []string{ojsFinalizer},
		},
		Spec: ojsv1alpha1.OJSClusterSpec{
			Backend:  ojsv1alpha1.BackendSpec{Type: "redis", URL: "redis://localhost:6379"},
			Replicas: int32Ptr(2),
		},
	}

	// Pre-create a deployment with ready replicas to simulate running state
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "running-cluster-server",
			Namespace: "default",
		},
		Status: appsv1.DeploymentStatus{
			Replicas:      2,
			ReadyReplicas: 2,
		},
	}

	cl := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(cluster, dep).
		WithStatusSubresource(cluster).
		Build()

	r := &OJSClusterReconciler{Client: cl, Scheme: scheme}
	req := reconcile.Request{NamespacedName: types.NamespacedName{Name: "running-cluster", Namespace: "default"}}

	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("reconcile failed: %v", err)
	}

	updated := &ojsv1alpha1.OJSCluster{}
	if err := cl.Get(context.Background(), req.NamespacedName, updated); err != nil {
		t.Fatalf("failed to get cluster: %v", err)
	}

	if updated.Status.Phase != "Running" {
		t.Errorf("expected phase Running, got %s", updated.Status.Phase)
	}
	if updated.Status.Replicas != 2 {
		t.Errorf("expected 2 replicas in status, got %d", updated.Status.Replicas)
	}
	if updated.Status.ReadyReplicas != 2 {
		t.Errorf("expected 2 ready replicas, got %d", updated.Status.ReadyReplicas)
	}

	readyCond := meta.FindStatusCondition(updated.Status.Conditions, condReady)
	if readyCond == nil || readyCond.Status != metav1.ConditionTrue {
		t.Error("expected Ready=True for running cluster")
	}
}

func TestClusterStatus_ScalingPhase(t *testing.T) {
	scheme := newScheme()

	cluster := &ojsv1alpha1.OJSCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "scaling-cluster",
			Namespace:  "default",
			Finalizers: []string{ojsFinalizer},
		},
		Spec: ojsv1alpha1.OJSClusterSpec{
			Backend:  ojsv1alpha1.BackendSpec{Type: "redis", URL: "redis://localhost:6379"},
			Replicas: int32Ptr(3),
		},
	}

	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "scaling-cluster-server",
			Namespace: "default",
		},
		Status: appsv1.DeploymentStatus{
			Replicas:      3,
			ReadyReplicas: 1,
		},
	}

	cl := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(cluster, dep).
		WithStatusSubresource(cluster).
		Build()

	r := &OJSClusterReconciler{Client: cl, Scheme: scheme}
	req := reconcile.Request{NamespacedName: types.NamespacedName{Name: "scaling-cluster", Namespace: "default"}}

	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("reconcile failed: %v", err)
	}

	updated := &ojsv1alpha1.OJSCluster{}
	if err := cl.Get(context.Background(), req.NamespacedName, updated); err != nil {
		t.Fatalf("failed to get cluster: %v", err)
	}

	if updated.Status.Phase != "Scaling" {
		t.Errorf("expected phase Scaling, got %s", updated.Status.Phase)
	}

	degradedCond := meta.FindStatusCondition(updated.Status.Conditions, condDegraded)
	if degradedCond == nil || degradedCond.Status != metav1.ConditionTrue {
		t.Error("expected Degraded=True during scaling")
	}
}

func TestWorkerStatus_InitialPending(t *testing.T) {
	scheme := newScheme()
	_ = autoscalingv2.AddToScheme(scheme)

	cluster := &ojsv1alpha1.OJSCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "ws-cluster", Namespace: "default"},
		Spec: ojsv1alpha1.OJSClusterSpec{
			Backend: ojsv1alpha1.BackendSpec{Type: "redis", URL: "redis://localhost:6379"},
		},
	}
	worker := &ojsv1alpha1.OJSWorker{
		ObjectMeta: metav1.ObjectMeta{Name: "ws-worker", Namespace: "default"},
		Spec: ojsv1alpha1.OJSWorkerSpec{
			ClusterRef: "ws-cluster",
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
	req := reconcile.Request{NamespacedName: types.NamespacedName{Name: "ws-worker", Namespace: "default"}}

	reconcileWorkerN(t, r, req, 2)

	updated := &ojsv1alpha1.OJSWorker{}
	if err := cl.Get(context.Background(), req.NamespacedName, updated); err != nil {
		t.Fatalf("failed to get worker: %v", err)
	}

	if updated.Status.Phase != "Pending" {
		t.Errorf("expected phase Pending, got %s", updated.Status.Phase)
	}
}

func TestWorkerStatus_ConditionsSet(t *testing.T) {
	scheme := newScheme()
	_ = autoscalingv2.AddToScheme(scheme)

	cluster := &ojsv1alpha1.OJSCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "wc-cluster", Namespace: "default"},
		Spec: ojsv1alpha1.OJSClusterSpec{
			Backend: ojsv1alpha1.BackendSpec{Type: "redis", URL: "redis://localhost:6379"},
		},
	}
	worker := &ojsv1alpha1.OJSWorker{
		ObjectMeta: metav1.ObjectMeta{Name: "wc-worker", Namespace: "default"},
		Spec: ojsv1alpha1.OJSWorkerSpec{
			ClusterRef: "wc-cluster",
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
	req := reconcile.Request{NamespacedName: types.NamespacedName{Name: "wc-worker", Namespace: "default"}}

	reconcileWorkerN(t, r, req, 2)

	updated := &ojsv1alpha1.OJSWorker{}
	if err := cl.Get(context.Background(), req.NamespacedName, updated); err != nil {
		t.Fatalf("failed to get worker: %v", err)
	}

	condTypes := []string{condWorkerReady, condWorkerAvail, condWorkerProgress, condWorkerDegraded}
	for _, ct := range condTypes {
		cond := meta.FindStatusCondition(updated.Status.Conditions, ct)
		if cond == nil {
			t.Errorf("expected condition %s to be set", ct)
		}
	}
}

func TestWorkerStatus_RunningPhase(t *testing.T) {
	scheme := newScheme()
	_ = autoscalingv2.AddToScheme(scheme)

	cluster := &ojsv1alpha1.OJSCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "wr-cluster", Namespace: "default"},
		Spec: ojsv1alpha1.OJSClusterSpec{
			Backend: ojsv1alpha1.BackendSpec{Type: "redis", URL: "redis://localhost:6379"},
		},
	}
	worker := &ojsv1alpha1.OJSWorker{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "wr-worker",
			Namespace:  "default",
			Finalizers: []string{workerFinalizer},
		},
		Spec: ojsv1alpha1.OJSWorkerSpec{
			ClusterRef: "wr-cluster",
			JobTypes:   []string{"job.type"},
			Image:      "worker:latest",
		},
	}

	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "wr-worker",
			Namespace: "default",
		},
		Status: appsv1.DeploymentStatus{
			Replicas:      1,
			ReadyReplicas: 1,
		},
	}

	cl := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(cluster, worker, dep).
		WithStatusSubresource(cluster, worker).
		Build()

	r := &OJSWorkerReconciler{Client: cl, Scheme: scheme}
	req := reconcile.Request{NamespacedName: types.NamespacedName{Name: "wr-worker", Namespace: "default"}}

	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("reconcile failed: %v", err)
	}

	updated := &ojsv1alpha1.OJSWorker{}
	if err := cl.Get(context.Background(), req.NamespacedName, updated); err != nil {
		t.Fatalf("failed to get worker: %v", err)
	}

	if updated.Status.Phase != "Running" {
		t.Errorf("expected phase Running, got %s", updated.Status.Phase)
	}

	readyCond := meta.FindStatusCondition(updated.Status.Conditions, condWorkerReady)
	if readyCond == nil || readyCond.Status != metav1.ConditionTrue {
		t.Error("expected Ready=True for running worker")
	}
}

func TestWorkerStatus_ScalingPhase(t *testing.T) {
	scheme := newScheme()
	_ = autoscalingv2.AddToScheme(scheme)

	cluster := &ojsv1alpha1.OJSCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "wsp-cluster", Namespace: "default"},
		Spec: ojsv1alpha1.OJSClusterSpec{
			Backend: ojsv1alpha1.BackendSpec{Type: "redis", URL: "redis://localhost:6379"},
		},
	}
	worker := &ojsv1alpha1.OJSWorker{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "wsp-worker",
			Namespace:  "default",
			Finalizers: []string{workerFinalizer},
		},
		Spec: ojsv1alpha1.OJSWorkerSpec{
			ClusterRef: "wsp-cluster",
			JobTypes:   []string{"job.type"},
			Image:      "worker:latest",
			Replicas:   int32Ptr(3),
		},
	}

	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "wsp-worker",
			Namespace: "default",
		},
		Status: appsv1.DeploymentStatus{
			Replicas:      3,
			ReadyReplicas: 1,
		},
	}

	cl := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(cluster, worker, dep).
		WithStatusSubresource(cluster, worker).
		Build()

	r := &OJSWorkerReconciler{Client: cl, Scheme: scheme}
	req := reconcile.Request{NamespacedName: types.NamespacedName{Name: "wsp-worker", Namespace: "default"}}

	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("reconcile failed: %v", err)
	}

	updated := &ojsv1alpha1.OJSWorker{}
	if err := cl.Get(context.Background(), req.NamespacedName, updated); err != nil {
		t.Fatalf("failed to get worker: %v", err)
	}

	if updated.Status.Phase != "Scaling" {
		t.Errorf("expected phase Scaling, got %s", updated.Status.Phase)
	}

	degradedCond := meta.FindStatusCondition(updated.Status.Conditions, condWorkerDegraded)
	if degradedCond == nil || degradedCond.Status != metav1.ConditionTrue {
		t.Error("expected Degraded=True during scaling")
	}
}

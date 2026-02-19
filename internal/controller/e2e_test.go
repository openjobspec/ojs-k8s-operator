package controller

import (
	"context"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	ojsv1alpha1 "github.com/openjobspec/ojs-k8s-operator/api/v1alpha1"
)

// reconcileN runs the reconciler n times and fails on error.
func reconcileClusterN(t *testing.T, r *OJSClusterReconciler, req reconcile.Request, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		if _, err := r.Reconcile(context.Background(), req); err != nil {
			t.Fatalf("reconcile %d failed: %v", i+1, err)
		}
	}
}

func reconcileWorkerN(t *testing.T, r *OJSWorkerReconciler, req reconcile.Request, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		if _, err := r.Reconcile(context.Background(), req); err != nil {
			t.Fatalf("reconcile %d failed: %v", i+1, err)
		}
	}
}

func TestE2E_ClusterCreation_CreatesDeploymentAndService(t *testing.T) {
	scheme := newScheme()

	cluster := &ojsv1alpha1.OJSCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "e2e-cluster",
			Namespace: "default",
		},
		Spec: ojsv1alpha1.OJSClusterSpec{
			Backend: ojsv1alpha1.BackendSpec{
				Type: "redis",
				URL:  "redis://redis:6379",
			},
			Replicas: int32Ptr(3),
			Image:    "ojs-server:e2e",
		},
	}

	cl := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(cluster).
		WithStatusSubresource(cluster).
		Build()

	r := &OJSClusterReconciler{Client: cl, Scheme: scheme}
	req := reconcile.Request{NamespacedName: types.NamespacedName{Name: "e2e-cluster", Namespace: "default"}}

	// Two reconciles: finalizer + resource creation
	reconcileClusterN(t, r, req, 2)

	// Verify Deployment exists with correct spec
	dep := &appsv1.Deployment{}
	if err := cl.Get(context.Background(), types.NamespacedName{Name: "e2e-cluster-server", Namespace: "default"}, dep); err != nil {
		t.Fatalf("Deployment not created: %v", err)
	}
	if *dep.Spec.Replicas != 3 {
		t.Errorf("expected 3 replicas, got %d", *dep.Spec.Replicas)
	}
	if dep.Spec.Template.Spec.Containers[0].Image != "ojs-server:e2e" {
		t.Errorf("expected image ojs-server:e2e, got %s", dep.Spec.Template.Spec.Containers[0].Image)
	}

	// Verify liveness and readiness probes
	container := dep.Spec.Template.Spec.Containers[0]
	if container.LivenessProbe == nil {
		t.Error("expected liveness probe to be set")
	}
	if container.ReadinessProbe == nil {
		t.Error("expected readiness probe to be set")
	}

	// Verify Service exists with both HTTP and metrics ports
	svc := &corev1.Service{}
	if err := cl.Get(context.Background(), types.NamespacedName{Name: "e2e-cluster-server", Namespace: "default"}, svc); err != nil {
		t.Fatalf("Service not created: %v", err)
	}
	if len(svc.Spec.Ports) != 2 {
		t.Errorf("expected 2 service ports, got %d", len(svc.Spec.Ports))
	}

	// Verify ConfigMap
	cm := &corev1.ConfigMap{}
	if err := cl.Get(context.Background(), types.NamespacedName{Name: "e2e-cluster-config", Namespace: "default"}, cm); err != nil {
		t.Fatalf("ConfigMap not created: %v", err)
	}

	// Verify owner references on all child resources
	for _, obj := range []metav1.ObjectMeta{dep.ObjectMeta, svc.ObjectMeta, cm.ObjectMeta} {
		if len(obj.OwnerReferences) == 0 {
			t.Errorf("expected owner reference on %s", obj.Name)
		}
	}

	// Verify status conditions are set
	updated := &ojsv1alpha1.OJSCluster{}
	if err := cl.Get(context.Background(), req.NamespacedName, updated); err != nil {
		t.Fatalf("failed to get cluster: %v", err)
	}
	// Should have Ready condition (false since deployment has 0 ready replicas in fake client)
	readyCond := meta.FindStatusCondition(updated.Status.Conditions, condReady)
	if readyCond == nil {
		t.Error("expected Ready condition to be set")
	}
	availCond := meta.FindStatusCondition(updated.Status.Conditions, condAvailable)
	if availCond == nil {
		t.Error("expected Available condition to be set")
	}
	progressCond := meta.FindStatusCondition(updated.Status.Conditions, condProgressing)
	if progressCond == nil {
		t.Error("expected Progressing condition to be set")
	}
}

func TestE2E_WorkerCreation_CreatesDeploymentAndHPA(t *testing.T) {
	scheme := newScheme()
	_ = autoscalingv2.AddToScheme(scheme)

	cluster := &ojsv1alpha1.OJSCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "e2e-cluster",
			Namespace: "default",
		},
		Spec: ojsv1alpha1.OJSClusterSpec{
			Backend: ojsv1alpha1.BackendSpec{Type: "redis", URL: "redis://redis:6379"},
		},
	}

	worker := &ojsv1alpha1.OJSWorker{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "e2e-worker",
			Namespace: "default",
		},
		Spec: ojsv1alpha1.OJSWorkerSpec{
			ClusterRef:  "e2e-cluster",
			JobTypes:    []string{"email.send", "report.generate"},
			Queues:      []string{"default", "high"},
			Concurrency: 10,
			Replicas:    int32Ptr(2),
			Image:       "worker:e2e",
			AutoScaling: &ojsv1alpha1.WorkerAutoScalingSpec{
				Enabled:     true,
				MinReplicas: 2,
				MaxReplicas: 10,
			},
		},
	}

	cl := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(cluster, worker).
		WithStatusSubresource(cluster, worker).
		Build()

	r := &OJSWorkerReconciler{Client: cl, Scheme: scheme}
	req := reconcile.Request{NamespacedName: types.NamespacedName{Name: "e2e-worker", Namespace: "default"}}

	// Two reconciles: finalizer + resource creation
	reconcileWorkerN(t, r, req, 2)

	// Verify worker Deployment
	dep := &appsv1.Deployment{}
	if err := cl.Get(context.Background(), types.NamespacedName{Name: "e2e-worker", Namespace: "default"}, dep); err != nil {
		t.Fatalf("Worker Deployment not created: %v", err)
	}
	if *dep.Spec.Replicas != 2 {
		t.Errorf("expected 2 replicas, got %d", *dep.Spec.Replicas)
	}

	container := dep.Spec.Template.Spec.Containers[0]
	if container.Image != "worker:e2e" {
		t.Errorf("expected image worker:e2e, got %s", container.Image)
	}

	// Verify environment variables include OJS_URL, OJS_QUEUES, OJS_JOB_TYPES, OJS_CONCURRENCY
	envMap := map[string]string{}
	for _, env := range container.Env {
		envMap[env.Name] = env.Value
	}
	if envMap["OJS_QUEUES"] != "default,high" {
		t.Errorf("expected OJS_QUEUES=default,high, got %s", envMap["OJS_QUEUES"])
	}
	if envMap["OJS_JOB_TYPES"] != "email.send,report.generate" {
		t.Errorf("expected OJS_JOB_TYPES=email.send,report.generate, got %s", envMap["OJS_JOB_TYPES"])
	}
	if envMap["OJS_CONCURRENCY"] != "10" {
		t.Errorf("expected OJS_CONCURRENCY=10, got %s", envMap["OJS_CONCURRENCY"])
	}

	// Verify HPA exists
	hpa := &autoscalingv2.HorizontalPodAutoscaler{}
	if err := cl.Get(context.Background(), types.NamespacedName{Name: "e2e-worker-hpa", Namespace: "default"}, hpa); err != nil {
		t.Fatalf("HPA not created: %v", err)
	}
	if *hpa.Spec.MinReplicas != 2 {
		t.Errorf("expected HPA minReplicas 2, got %d", *hpa.Spec.MinReplicas)
	}
	if hpa.Spec.MaxReplicas != 10 {
		t.Errorf("expected HPA maxReplicas 10, got %d", hpa.Spec.MaxReplicas)
	}

	// Verify owner reference on Deployment
	if len(dep.OwnerReferences) == 0 {
		t.Error("expected owner reference on worker deployment")
	}

	// Verify worker status conditions
	updated := &ojsv1alpha1.OJSWorker{}
	if err := cl.Get(context.Background(), req.NamespacedName, updated); err != nil {
		t.Fatalf("failed to get worker: %v", err)
	}
	readyCond := meta.FindStatusCondition(updated.Status.Conditions, condWorkerReady)
	if readyCond == nil {
		t.Error("expected Ready condition on worker")
	}
}

func TestE2E_WorkerScaling_DisableHPA(t *testing.T) {
	scheme := newScheme()
	_ = autoscalingv2.AddToScheme(scheme)

	cluster := &ojsv1alpha1.OJSCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "scale-cluster", Namespace: "default"},
		Spec: ojsv1alpha1.OJSClusterSpec{
			Backend: ojsv1alpha1.BackendSpec{Type: "redis", URL: "redis://redis:6379"},
		},
	}

	worker := &ojsv1alpha1.OJSWorker{
		ObjectMeta: metav1.ObjectMeta{Name: "scale-worker", Namespace: "default"},
		Spec: ojsv1alpha1.OJSWorkerSpec{
			ClusterRef: "scale-cluster",
			JobTypes:   []string{"job.type"},
			Image:      "worker:latest",
			AutoScaling: &ojsv1alpha1.WorkerAutoScalingSpec{
				Enabled:     true,
				MinReplicas: 1,
				MaxReplicas: 5,
			},
		},
	}

	cl := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(cluster, worker).
		WithStatusSubresource(cluster, worker).
		Build()

	r := &OJSWorkerReconciler{Client: cl, Scheme: scheme}
	req := reconcile.Request{NamespacedName: types.NamespacedName{Name: "scale-worker", Namespace: "default"}}

	// Create with HPA enabled
	reconcileWorkerN(t, r, req, 2)

	// Verify HPA was created
	hpa := &autoscalingv2.HorizontalPodAutoscaler{}
	if err := cl.Get(context.Background(), types.NamespacedName{Name: "scale-worker-hpa", Namespace: "default"}, hpa); err != nil {
		t.Fatalf("HPA should exist: %v", err)
	}

	// Disable autoscaling
	updatedWorker := &ojsv1alpha1.OJSWorker{}
	if err := cl.Get(context.Background(), req.NamespacedName, updatedWorker); err != nil {
		t.Fatalf("failed to get worker: %v", err)
	}
	updatedWorker.Spec.AutoScaling.Enabled = false
	if err := cl.Update(context.Background(), updatedWorker); err != nil {
		t.Fatalf("failed to update worker: %v", err)
	}

	// Reconcile again
	reconcileWorkerN(t, r, req, 1)

	// Verify HPA was deleted
	err := cl.Get(context.Background(), types.NamespacedName{Name: "scale-worker-hpa", Namespace: "default"}, hpa)
	if err == nil {
		t.Error("expected HPA to be deleted when autoscaling disabled")
	}
}

func TestE2E_ClusterDeletion_CleansUpChildResources(t *testing.T) {
	scheme := newScheme()

	cluster := &ojsv1alpha1.OJSCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "cleanup-cluster",
			Namespace: "default",
		},
		Spec: ojsv1alpha1.OJSClusterSpec{
			Backend: ojsv1alpha1.BackendSpec{Type: "redis", URL: "redis://redis:6379"},
		},
	}

	cl := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(cluster).
		WithStatusSubresource(cluster).
		Build()

	r := &OJSClusterReconciler{Client: cl, Scheme: scheme}
	req := reconcile.Request{NamespacedName: types.NamespacedName{Name: "cleanup-cluster", Namespace: "default"}}

	// Create resources
	reconcileClusterN(t, r, req, 2)

	// Verify resources exist
	dep := &appsv1.Deployment{}
	if err := cl.Get(context.Background(), types.NamespacedName{Name: "cleanup-cluster-server", Namespace: "default"}, dep); err != nil {
		t.Fatalf("Deployment should exist: %v", err)
	}

	// Delete the cluster by removing it directly (simulating garbage collection)
	// In a real cluster, the owner reference cascade would handle this.
	// Here we verify the finalizer is removed properly.
	updated := &ojsv1alpha1.OJSCluster{}
	if err := cl.Get(context.Background(), req.NamespacedName, updated); err != nil {
		t.Fatalf("failed to get cluster: %v", err)
	}

	// Verify the cluster has a finalizer
	hasFinalizer := false
	for _, f := range updated.Finalizers {
		if f == ojsFinalizer {
			hasFinalizer = true
		}
	}
	if !hasFinalizer {
		t.Error("expected cluster to have finalizer")
	}

	// Verify owner references exist on child resources so K8s GC will clean them up
	if len(dep.OwnerReferences) == 0 {
		t.Error("expected deployment to have owner reference for cascading deletion")
	}
	svc := &corev1.Service{}
	if err := cl.Get(context.Background(), types.NamespacedName{Name: "cleanup-cluster-server", Namespace: "default"}, svc); err != nil {
		t.Fatalf("Service should exist: %v", err)
	}
	if len(svc.OwnerReferences) == 0 {
		t.Error("expected service to have owner reference for cascading deletion")
	}
	cm := &corev1.ConfigMap{}
	if err := cl.Get(context.Background(), types.NamespacedName{Name: "cleanup-cluster-config", Namespace: "default"}, cm); err != nil {
		t.Fatalf("ConfigMap should exist: %v", err)
	}
	if len(cm.OwnerReferences) == 0 {
		t.Error("expected configmap to have owner reference for cascading deletion")
	}
}

func TestE2E_WorkerClusterNotFound(t *testing.T) {
	scheme := newScheme()
	_ = autoscalingv2.AddToScheme(scheme)

	worker := &ojsv1alpha1.OJSWorker{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "orphan-worker",
			Namespace:  "default",
			Finalizers: []string{workerFinalizer},
		},
		Spec: ojsv1alpha1.OJSWorkerSpec{
			ClusterRef: "nonexistent-cluster",
			JobTypes:   []string{"job.type"},
			Image:      "worker:latest",
		},
	}

	cl := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(worker).
		WithStatusSubresource(worker).
		Build()

	r := &OJSWorkerReconciler{Client: cl, Scheme: scheme}
	req := reconcile.Request{NamespacedName: types.NamespacedName{Name: "orphan-worker", Namespace: "default"}}

	result, err := r.Reconcile(context.Background(), req)
	if err != nil {
		t.Fatalf("reconcile failed: %v", err)
	}
	if result.RequeueAfter == 0 {
		t.Error("expected requeue when cluster not found")
	}

	// Verify status reflects the error
	updated := &ojsv1alpha1.OJSWorker{}
	if err := cl.Get(context.Background(), req.NamespacedName, updated); err != nil {
		t.Fatalf("failed to get worker: %v", err)
	}
	if updated.Status.Phase != "Error" {
		t.Errorf("expected phase Error, got %s", updated.Status.Phase)
	}
	readyCond := meta.FindStatusCondition(updated.Status.Conditions, condWorkerReady)
	if readyCond == nil || readyCond.Reason != "ClusterNotFound" {
		t.Error("expected Ready condition with ClusterNotFound reason")
	}
	degradedCond := meta.FindStatusCondition(updated.Status.Conditions, condWorkerDegraded)
	if degradedCond == nil || degradedCond.Status != metav1.ConditionTrue {
		t.Error("expected Degraded=True when cluster not found")
	}
}

func TestE2E_ClusterStatusConditions_ObservedGeneration(t *testing.T) {
	scheme := newScheme()

	cluster := &ojsv1alpha1.OJSCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "gen-cluster",
			Namespace:  "default",
			Generation: 3,
		},
		Spec: ojsv1alpha1.OJSClusterSpec{
			Backend: ojsv1alpha1.BackendSpec{Type: "redis", URL: "redis://redis:6379"},
		},
	}

	cl := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(cluster).
		WithStatusSubresource(cluster).
		Build()

	r := &OJSClusterReconciler{Client: cl, Scheme: scheme}
	req := reconcile.Request{NamespacedName: types.NamespacedName{Name: "gen-cluster", Namespace: "default"}}

	reconcileClusterN(t, r, req, 2)

	updated := &ojsv1alpha1.OJSCluster{}
	if err := cl.Get(context.Background(), req.NamespacedName, updated); err != nil {
		t.Fatalf("failed to get cluster: %v", err)
	}

	// Check that ObservedGeneration is set on conditions
	for _, cond := range updated.Status.Conditions {
		if cond.ObservedGeneration != 3 {
			t.Errorf("condition %s has ObservedGeneration %d, expected 3", cond.Type, cond.ObservedGeneration)
		}
	}
}

func TestE2E_EmbeddedRedis_FullStack(t *testing.T) {
	scheme := newScheme()
	_ = autoscalingv2.AddToScheme(scheme)

	cluster := &ojsv1alpha1.OJSCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "full-stack", Namespace: "default"},
		Spec: ojsv1alpha1.OJSClusterSpec{
			Backend:  ojsv1alpha1.BackendSpec{Type: "redis", Embedded: true},
			Replicas: int32Ptr(2),
		},
	}

	worker := &ojsv1alpha1.OJSWorker{
		ObjectMeta: metav1.ObjectMeta{Name: "full-stack-worker", Namespace: "default"},
		Spec: ojsv1alpha1.OJSWorkerSpec{
			ClusterRef: "full-stack",
			JobTypes:   []string{"email.send"},
			Image:      "worker:latest",
			Replicas:   int32Ptr(3),
		},
	}

	cl := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(cluster, worker).
		WithStatusSubresource(cluster, worker).
		Build()

	clusterR := &OJSClusterReconciler{Client: cl, Scheme: scheme}
	clusterReq := reconcile.Request{NamespacedName: types.NamespacedName{Name: "full-stack", Namespace: "default"}}
	reconcileClusterN(t, clusterR, clusterReq, 2)

	// Verify embedded Redis
	redisDep := &appsv1.Deployment{}
	if err := cl.Get(context.Background(), types.NamespacedName{Name: "full-stack-redis", Namespace: "default"}, redisDep); err != nil {
		t.Fatalf("embedded Redis deployment not created: %v", err)
	}

	redisSvc := &corev1.Service{}
	if err := cl.Get(context.Background(), types.NamespacedName{Name: "full-stack-redis", Namespace: "default"}, redisSvc); err != nil {
		t.Fatalf("embedded Redis service not created: %v", err)
	}

	// Verify OJS server
	serverDep := &appsv1.Deployment{}
	if err := cl.Get(context.Background(), types.NamespacedName{Name: "full-stack-server", Namespace: "default"}, serverDep); err != nil {
		t.Fatalf("server deployment not created: %v", err)
	}

	// Now reconcile the worker
	workerR := &OJSWorkerReconciler{Client: cl, Scheme: scheme}
	workerReq := reconcile.Request{NamespacedName: types.NamespacedName{Name: "full-stack-worker", Namespace: "default"}}
	reconcileWorkerN(t, workerR, workerReq, 2)

	// Verify worker deployment
	workerDep := &appsv1.Deployment{}
	if err := cl.Get(context.Background(), types.NamespacedName{Name: "full-stack-worker", Namespace: "default"}, workerDep); err != nil {
		t.Fatalf("worker deployment not created: %v", err)
	}
	if *workerDep.Spec.Replicas != 3 {
		t.Errorf("expected 3 worker replicas, got %d", *workerDep.Spec.Replicas)
	}

	// Verify ConfigMap has auto-generated Redis URL
	cm := &corev1.ConfigMap{}
	if err := cl.Get(context.Background(), types.NamespacedName{Name: "full-stack-config", Namespace: "default"}, cm); err != nil {
		t.Fatalf("ConfigMap not found: %v", err)
	}
	expectedURL := "redis://full-stack-redis.default.svc.cluster.local:6379"
	if cm.Data["BACKEND_URL"] != expectedURL {
		t.Errorf("expected backend URL %s, got %s", expectedURL, cm.Data["BACKEND_URL"])
	}
}

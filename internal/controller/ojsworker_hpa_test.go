package controller

import (
	"context"
	"errors"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	ojsv1alpha1 "github.com/openjobspec/ojs-k8s-operator/api/v1alpha1"
)

func TestDeleteWorkerHPA_NotFoundIsNotAnError(t *testing.T) {
	scheme := newWorkerScheme()
	cl := fake.NewClientBuilder().WithScheme(scheme).Build()
	r := &OJSWorkerReconciler{Client: cl, Scheme: scheme}
	worker, _ := baseWorker()
	worker.UID = "worker-uid"

	hpa := &autoscalingv2.HorizontalPodAutoscaler{
		ObjectMeta: metav1.ObjectMeta{Name: "missing-hpa", Namespace: "default"},
	}
	if err := r.deleteWorkerHPA(context.Background(), worker, hpa); err != nil {
		t.Errorf("expected no error deleting an already-absent HPA, got: %v", err)
	}
}

func TestDeleteWorkerHPA_RemovesCurrentWorkerOwnedHPA(t *testing.T) {
	scheme := newWorkerScheme()
	worker, _ := baseWorker()
	worker.UID = "worker-uid"
	existing := &autoscalingv2.HorizontalPodAutoscaler{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "present-hpa",
			Namespace: "default",
			OwnerReferences: []metav1.OwnerReference{
				*metav1.NewControllerRef(worker, ojsv1alpha1.GroupVersion.WithKind("OJSWorker")),
			},
		},
	}
	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(existing).Build()
	r := &OJSWorkerReconciler{Client: cl, Scheme: scheme}

	hpa := &autoscalingv2.HorizontalPodAutoscaler{
		ObjectMeta: metav1.ObjectMeta{Name: "present-hpa", Namespace: "default"},
	}
	if err := r.deleteWorkerHPA(context.Background(), worker, hpa); err != nil {
		t.Fatalf("expected successful deletion, got: %v", err)
	}

	check := &autoscalingv2.HorizontalPodAutoscaler{}
	err := cl.Get(context.Background(), client.ObjectKey{Name: "present-hpa", Namespace: "default"}, check)
	if !apierrors.IsNotFound(err) {
		t.Errorf("expected HPA to be deleted, Get returned: %v", err)
	}
}

func TestDeleteWorkerHPA_PreservesHPAWithoutCurrentWorkerOwnership(t *testing.T) {
	scheme := newWorkerScheme()
	worker, _ := baseWorker()
	worker.UID = "worker-uid"
	otherWorker := worker.DeepCopy()
	otherWorker.Name = "other-worker"
	otherWorker.UID = "other-worker-uid"

	tests := []struct {
		name            string
		ownerReferences []metav1.OwnerReference
	}{
		{name: "ownerless"},
		{
			name: "controlled by another worker",
			ownerReferences: []metav1.OwnerReference{
				*metav1.NewControllerRef(otherWorker, ojsv1alpha1.GroupVersion.WithKind("OJSWorker")),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			existing := &autoscalingv2.HorizontalPodAutoscaler{
				ObjectMeta: metav1.ObjectMeta{
					Name:            worker.Name + "-hpa",
					Namespace:       worker.Namespace,
					OwnerReferences: tt.ownerReferences,
				},
			}
			cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(existing).Build()
			r := &OJSWorkerReconciler{Client: cl, Scheme: scheme}

			if err := r.reconcileWorkerHPA(context.Background(), worker); err != nil {
				t.Fatalf("disable autoscaling: %v", err)
			}

			check := &autoscalingv2.HorizontalPodAutoscaler{}
			if err := cl.Get(context.Background(), client.ObjectKeyFromObject(existing), check); err != nil {
				t.Fatalf("same-name HPA should be preserved: %v", err)
			}
		})
	}
}

// TestDeleteWorkerHPA_PropagatesNonNotFoundGetError is a regression test:
// previously, ANY error from the Get call (not just NotFound) was silently
// swallowed and treated as "nothing to delete". A transient API error (e.g.
// throttling, a network blip) must be surfaced to the caller so Reconcile
// retries, instead of the operator quietly believing HPA state is already
// as desired.
func TestDeleteWorkerHPA_PropagatesNonNotFoundGetError(t *testing.T) {
	scheme := newWorkerScheme()
	wantErr := errors.New("simulated transient API error")

	cl := fake.NewClientBuilder().
		WithScheme(scheme).
		WithInterceptorFuncs(interceptor.Funcs{
			Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
				if _, ok := obj.(*autoscalingv2.HorizontalPodAutoscaler); ok {
					return wantErr
				}
				return c.Get(ctx, key, obj, opts...)
			},
		}).
		Build()

	r := &OJSWorkerReconciler{Client: cl, Scheme: scheme}
	worker, _ := baseWorker()
	worker.UID = "worker-uid"
	hpa := &autoscalingv2.HorizontalPodAutoscaler{
		ObjectMeta: metav1.ObjectMeta{Name: "flaky-hpa", Namespace: "default"},
	}

	err := r.deleteWorkerHPA(context.Background(), worker, hpa)
	if !errors.Is(err, wantErr) {
		t.Errorf("expected non-NotFound Get error to be propagated, got: %v", err)
	}
}

func TestReconcileWorkerDeployment_AutoscalingLifecycle(t *testing.T) {
	scheme := newWorkerScheme()
	worker, cluster := baseWorker()
	worker.UID = "worker-uid"
	worker.Spec.Replicas = int32Ptr(5)
	worker.Spec.AutoScaling = &ojsv1alpha1.WorkerAutoScalingSpec{
		Enabled:     true,
		MinReplicas: 3,
		MaxReplicas: 10,
	}

	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(worker, cluster).Build()
	r := &OJSWorkerReconciler{Client: cl, Scheme: scheme}
	ctx := context.Background()

	if err := r.reconcileWorkerDeployment(ctx, worker, cluster); err != nil {
		t.Fatalf("create autoscaled Deployment: %v", err)
	}

	dep := &appsv1.Deployment{}
	key := client.ObjectKey{Name: worker.Name, Namespace: worker.Namespace}
	if err := cl.Get(ctx, key, dep); err != nil {
		t.Fatalf("get created Deployment: %v", err)
	}
	if dep.Spec.Replicas == nil || *dep.Spec.Replicas != 5 {
		t.Fatalf("created replicas = %v, want explicit initial value 5", dep.Spec.Replicas)
	}

	hpaReplicas := int32(8)
	dep.Spec.Replicas = &hpaReplicas
	if err := cl.Update(ctx, dep); err != nil {
		t.Fatalf("simulate HPA scale: %v", err)
	}
	worker.Spec.Image = "ojs-worker:updated"
	worker.Spec.Replicas = int32Ptr(2)
	if err := r.reconcileWorkerDeployment(ctx, worker, cluster); err != nil {
		t.Fatalf("update autoscaled Deployment: %v", err)
	}
	if err := cl.Get(ctx, key, dep); err != nil {
		t.Fatalf("get updated Deployment: %v", err)
	}
	if dep.Spec.Replicas == nil || *dep.Spec.Replicas != 8 {
		t.Errorf("autoscaling update replicas = %v, want HPA-owned value 8", dep.Spec.Replicas)
	}
	if got := dep.Spec.Template.Spec.Containers[0].Image; got != "ojs-worker:updated" {
		t.Errorf("image = %q, want non-replica fields reconciled", got)
	}

	worker.Spec.AutoScaling.Enabled = false
	worker.Spec.Replicas = int32Ptr(4)
	if err := r.reconcileWorkerDeployment(ctx, worker, cluster); err != nil {
		t.Fatalf("disable autoscaling: %v", err)
	}
	if err := cl.Get(ctx, key, dep); err != nil {
		t.Fatalf("get Deployment after disabling autoscaling: %v", err)
	}
	if dep.Spec.Replicas == nil || *dep.Spec.Replicas != 4 {
		t.Errorf("disabled autoscaling replicas = %v, want resumed spec value 4", dep.Spec.Replicas)
	}
}

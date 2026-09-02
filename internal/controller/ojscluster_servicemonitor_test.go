package controller

import (
	"context"
	"errors"
	"testing"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	ojsv1alpha1 "github.com/openjobspec/ojs-k8s-operator/api/v1alpha1"
)

func getServiceMonitor(t *testing.T, cl client.Client, name, namespace string) error {
	t.Helper()
	sm := &unstructured.Unstructured{}
	sm.SetGroupVersionKind(serviceMonitorGVK())
	return cl.Get(context.Background(), client.ObjectKey{Name: name, Namespace: namespace}, sm)
}

func serviceMonitorObject(name, namespace string, ownerRefs ...metav1.OwnerReference) *unstructured.Unstructured {
	sm := &unstructured.Unstructured{}
	sm.SetGroupVersionKind(serviceMonitorGVK())
	sm.SetName(name)
	sm.SetNamespace(namespace)
	sm.SetOwnerReferences(ownerRefs)
	return sm
}

func serviceMonitorNoMatchError() error {
	gvk := serviceMonitorGVK()
	return &apimeta.NoKindMatchError{
		GroupKind:        gvk.GroupKind(),
		SearchedVersions: []string{gvk.Version},
	}
}

func noServiceMonitorGVKClient(t *testing.T, cluster *ojsv1alpha1.OJSCluster) client.Client {
	t.Helper()
	return fake.NewClientBuilder().
		WithScheme(newScheme()).
		WithObjects(cluster).
		WithStatusSubresource(cluster).
		WithInterceptorFuncs(interceptor.Funcs{
			Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
				if obj.GetObjectKind().GroupVersionKind() == serviceMonitorGVK() {
					return serviceMonitorNoMatchError()
				}
				return c.Get(ctx, key, obj, opts...)
			},
		}).
		Build()
}

func assertNoEvent(t *testing.T, recorder *record.FakeRecorder) {
	t.Helper()
	select {
	case event := <-recorder.Events:
		t.Errorf("expected no event, got %q", event)
	default:
	}
}

func warningEventCount(recorder *record.FakeRecorder, reason string) int {
	count := 0
	for {
		select {
		case event := <-recorder.Events:
			if event == "" {
				return count
			}
			if len(event) >= len(corev1.EventTypeWarning+" "+reason) &&
				event[:len(corev1.EventTypeWarning+" "+reason)] == corev1.EventTypeWarning+" "+reason {
				count++
			}
		default:
			return count
		}
	}
}

// TestReconcileServiceMonitorPhase_CreatesWhenEnabled verifies the
// ServiceMonitor is created when spec.monitoring requests it.
func TestReconcileServiceMonitorPhase_CreatesWhenEnabled(t *testing.T) {
	scheme := newScheme()
	cluster := &ojsv1alpha1.OJSCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "sm-on", Namespace: "default"},
		Spec: ojsv1alpha1.OJSClusterSpec{
			Backend:    ojsv1alpha1.BackendSpec{Type: "redis", URL: "redis://localhost:6379"},
			Monitoring: &ojsv1alpha1.MonitoringSpec{Enabled: true, ServiceMonitor: true},
		},
	}
	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(cluster).Build()
	r := &OJSClusterReconciler{Client: cl, Scheme: scheme}

	if err := r.reconcileServiceMonitorPhase(context.Background(), cluster); err != nil {
		t.Fatalf("reconcileServiceMonitorPhase returned an error: %v", err)
	}

	if err := getServiceMonitor(t, cl, "sm-on-server", "default"); err != nil {
		t.Errorf("expected ServiceMonitor to be created, got: %v", err)
	}
}

// TestReconcileServiceMonitorPhase_RemovesWhenDisabled is a regression test:
// previously, once a ServiceMonitor was created it was never cleaned up if
// the user later disabled spec.monitoring, leaving an orphaned resource
// forever (unlike OJSWorker's HPA, which is deleted when autoscaling is
// disabled). Disabling monitoring must now remove any existing
// ServiceMonitor.
func TestReconcileServiceMonitorPhase_RemovesWhenDisabled(t *testing.T) {
	scheme := newScheme()
	cluster := &ojsv1alpha1.OJSCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "sm-off", Namespace: "default"},
		Spec: ojsv1alpha1.OJSClusterSpec{
			Backend:    ojsv1alpha1.BackendSpec{Type: "redis", URL: "redis://localhost:6379"},
			Monitoring: &ojsv1alpha1.MonitoringSpec{Enabled: true, ServiceMonitor: true},
		},
	}
	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(cluster).Build()
	r := &OJSClusterReconciler{Client: cl, Scheme: scheme}

	// First, create it while enabled.
	if err := r.reconcileServiceMonitorPhase(context.Background(), cluster); err != nil {
		t.Fatalf("initial reconcileServiceMonitorPhase returned an error: %v", err)
	}
	if err := getServiceMonitor(t, cl, "sm-off-server", "default"); err != nil {
		t.Fatalf("expected ServiceMonitor to be created first, got: %v", err)
	}

	// Then disable monitoring and reconcile again.
	cluster.Spec.Monitoring.ServiceMonitor = false
	if err := r.reconcileServiceMonitorPhase(context.Background(), cluster); err != nil {
		t.Fatalf("cleanup reconcileServiceMonitorPhase returned an error: %v", err)
	}

	err := getServiceMonitor(t, cl, "sm-off-server", "default")
	if !apierrors.IsNotFound(err) {
		t.Errorf("expected ServiceMonitor to be deleted after disabling, Get returned: %v", err)
	}
}

func TestDeleteServiceMonitor_LeavesUnownedResourceUntouched(t *testing.T) {
	scheme := newScheme()
	cluster := &ojsv1alpha1.OJSCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "sm-unowned",
			Namespace: "default",
			UID:       types.UID("current-cluster"),
		},
	}
	sm := serviceMonitorObject("sm-unowned-server", "default")
	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(cluster, sm).Build()
	r := &OJSClusterReconciler{Client: cl, Scheme: scheme}

	if err := r.deleteServiceMonitor(context.Background(), cluster); err != nil {
		t.Fatalf("deleteServiceMonitor returned an error: %v", err)
	}
	if err := getServiceMonitor(t, cl, sm.GetName(), sm.GetNamespace()); err != nil {
		t.Errorf("unowned ServiceMonitor should remain untouched, got: %v", err)
	}
}

func TestDeleteServiceMonitor_RemovesCurrentlyControlledResource(t *testing.T) {
	scheme := newScheme()
	cluster := &ojsv1alpha1.OJSCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "sm-owned",
			Namespace: "default",
			UID:       types.UID("current-cluster"),
		},
	}
	controller := true
	sm := serviceMonitorObject("sm-owned-server", "default", metav1.OwnerReference{
		APIVersion: ojsv1alpha1.GroupVersion.String(),
		Kind:       "OJSCluster",
		Name:       cluster.Name,
		UID:        cluster.UID,
		Controller: &controller,
	})
	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(cluster, sm).Build()
	r := &OJSClusterReconciler{Client: cl, Scheme: scheme}

	if err := r.deleteServiceMonitor(context.Background(), cluster); err != nil {
		t.Fatalf("deleteServiceMonitor returned an error: %v", err)
	}
	if err := getServiceMonitor(t, cl, sm.GetName(), sm.GetNamespace()); !apierrors.IsNotFound(err) {
		t.Errorf("currently controlled ServiceMonitor should be deleted, got: %v", err)
	}
}

func TestDeleteServiceMonitor_LeavesSiblingOwnedResourceUntouched(t *testing.T) {
	scheme := newScheme()
	cluster := &ojsv1alpha1.OJSCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "sm-sibling",
			Namespace: "default",
			UID:       types.UID("current-cluster"),
		},
	}
	controller := true
	sm := serviceMonitorObject("sm-sibling-server", "default", metav1.OwnerReference{
		APIVersion: ojsv1alpha1.GroupVersion.String(),
		Kind:       "OJSCluster",
		Name:       "other-cluster",
		UID:        types.UID("sibling-cluster"),
		Controller: &controller,
	})
	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(cluster, sm).Build()
	r := &OJSClusterReconciler{Client: cl, Scheme: scheme}

	if err := r.deleteServiceMonitor(context.Background(), cluster); err != nil {
		t.Fatalf("deleteServiceMonitor returned an error: %v", err)
	}
	if err := getServiceMonitor(t, cl, sm.GetName(), sm.GetNamespace()); err != nil {
		t.Errorf("sibling-owned ServiceMonitor should remain untouched, got: %v", err)
	}
}

// TestReconcileServiceMonitorPhase_NoOpWhenNeverEnabled verifies that
// clusters which never opt into monitoring don't error out (e.g. because
// the ServiceMonitor CRD/kind isn't registered in the cluster).
func TestReconcileServiceMonitorPhase_NoOpWhenNeverEnabled(t *testing.T) {
	scheme := newScheme()
	cluster := &ojsv1alpha1.OJSCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "sm-never", Namespace: "default"},
		Spec: ojsv1alpha1.OJSClusterSpec{
			Backend: ojsv1alpha1.BackendSpec{Type: "redis", URL: "redis://localhost:6379"},
		},
	}
	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(cluster).Build()
	r := &OJSClusterReconciler{Client: cl, Scheme: scheme}

	// Should not panic and should leave no ServiceMonitor behind.
	if err := r.reconcileServiceMonitorPhase(context.Background(), cluster); err != nil {
		t.Fatalf("reconcileServiceMonitorPhase returned an error: %v", err)
	}

	err := getServiceMonitor(t, cl, "sm-never-server", "default")
	if !apierrors.IsNotFound(err) {
		t.Errorf("expected no ServiceMonitor to exist, Get returned: %v", err)
	}
}

func TestReconcileServiceMonitorPhase_NoMatchIsOptionalCapabilityAbsence(t *testing.T) {
	cluster := &ojsv1alpha1.OJSCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "sm-no-crd-create", Namespace: "default"},
		Spec: ojsv1alpha1.OJSClusterSpec{
			Monitoring: &ojsv1alpha1.MonitoringSpec{Enabled: true, ServiceMonitor: true},
		},
	}
	cl := noServiceMonitorGVKClient(t, cluster)
	recorder := record.NewFakeRecorder(1)
	r := &OJSClusterReconciler{Client: cl, Scheme: newScheme(), Recorder: recorder}

	if err := r.reconcileServiceMonitor(context.Background(), cluster); err != nil {
		t.Fatalf("NoMatch should not be returned from ServiceMonitor reconcile: %v", err)
	}
	if err := r.reconcileServiceMonitorPhase(context.Background(), cluster); err != nil {
		t.Fatalf("NoMatch should not be returned from ServiceMonitor phase: %v", err)
	}
	assertNoEvent(t, recorder)
}

func TestDeleteServiceMonitor_NoMatchIsOptionalCapabilityAbsence(t *testing.T) {
	cluster := &ojsv1alpha1.OJSCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "sm-no-crd-delete", Namespace: "default"},
	}
	cl := noServiceMonitorGVKClient(t, cluster)
	recorder := record.NewFakeRecorder(1)
	r := &OJSClusterReconciler{Client: cl, Scheme: newScheme(), Recorder: recorder}

	if err := r.deleteServiceMonitor(context.Background(), cluster); err != nil {
		t.Fatalf("NoMatch should not be returned from ServiceMonitor cleanup: %v", err)
	}
	if err := r.reconcileServiceMonitorPhase(context.Background(), cluster); err != nil {
		t.Fatalf("NoMatch should not be returned from ServiceMonitor cleanup phase: %v", err)
	}
	assertNoEvent(t, recorder)
}

func TestReconcile_ServiceMonitorTransientFailureRetries(t *testing.T) {
	tests := []struct {
		name          string
		operation     string
		enabled       bool
		existing      bool
		warningReason string
	}{
		{
			name:          "create",
			operation:     "create",
			enabled:       true,
			warningReason: "ServiceMonitorFailed",
		},
		{
			name:          "update",
			operation:     "update",
			enabled:       true,
			existing:      true,
			warningReason: "ServiceMonitorFailed",
		},
		{
			name:          "delete",
			operation:     "delete",
			existing:      true,
			warningReason: "ServiceMonitorDeleteFailed",
		},
		{
			name:          "get during obsolete cleanup",
			operation:     "get",
			existing:      true,
			warningReason: "ServiceMonitorDeleteFailed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scheme := newScheme()
			cluster := &ojsv1alpha1.OJSCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "sm-retry-" + tt.operation,
					Namespace:  "default",
					UID:        types.UID("cluster-" + tt.operation),
					Finalizers: []string{ojsFinalizer},
				},
				Spec: ojsv1alpha1.OJSClusterSpec{
					Backend: ojsv1alpha1.BackendSpec{Type: "redis", URL: "redis://localhost:6379"},
					Monitoring: &ojsv1alpha1.MonitoringSpec{
						Enabled:        tt.enabled,
						ServiceMonitor: tt.enabled,
					},
				},
				Status: ojsv1alpha1.OJSClusterStatus{Phase: "Sentinel"},
			}

			objects := []client.Object{cluster}
			if tt.existing {
				controller := true
				sm := serviceMonitorObject(cluster.Name+"-server", cluster.Namespace, metav1.OwnerReference{
					APIVersion: ojsv1alpha1.GroupVersion.String(),
					Kind:       "OJSCluster",
					Name:       cluster.Name,
					UID:        cluster.UID,
					Controller: &controller,
				})
				sm.SetLabels(map[string]string{"stale": "true"})
				sm.Object["spec"] = map[string]interface{}{"stale": true}
				objects = append(objects, sm)
			}

			wantErr := errors.New("simulated transient ServiceMonitor " + tt.operation + " error")
			operationCalls := 0
			failOnce := func() error {
				operationCalls++
				if operationCalls == 1 {
					return wantErr
				}
				return nil
			}
			funcs := interceptor.Funcs{
				Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
					if tt.operation == "get" && obj.GetObjectKind().GroupVersionKind() == serviceMonitorGVK() {
						if err := failOnce(); err != nil {
							return err
						}
					}
					return c.Get(ctx, key, obj, opts...)
				},
				Create: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
					if tt.operation == "create" && obj.GetObjectKind().GroupVersionKind() == serviceMonitorGVK() {
						if err := failOnce(); err != nil {
							return err
						}
					}
					return c.Create(ctx, obj, opts...)
				},
				Update: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.UpdateOption) error {
					if tt.operation == "update" && obj.GetObjectKind().GroupVersionKind() == serviceMonitorGVK() {
						if err := failOnce(); err != nil {
							return err
						}
					}
					return c.Update(ctx, obj, opts...)
				},
				Delete: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.DeleteOption) error {
					if tt.operation == "delete" && obj.GetObjectKind().GroupVersionKind() == serviceMonitorGVK() {
						if err := failOnce(); err != nil {
							return err
						}
					}
					return c.Delete(ctx, obj, opts...)
				},
			}

			cl := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(objects...).
				WithStatusSubresource(cluster).
				WithInterceptorFuncs(funcs).
				Build()
			recorder := record.NewFakeRecorder(10)
			r := &OJSClusterReconciler{Client: cl, Scheme: scheme, Recorder: recorder}
			req := ctrl.Request{NamespacedName: types.NamespacedName{Name: cluster.Name, Namespace: cluster.Namespace}}

			result, err := r.Reconcile(context.Background(), req)
			if !errors.Is(err, wantErr) {
				t.Fatalf("first Reconcile error = %v, want %v", err, wantErr)
			}
			if result != (ctrl.Result{}) {
				t.Fatalf("first Reconcile result = %v, want zero result with error", result)
			}

			updatedCluster := &ojsv1alpha1.OJSCluster{}
			if err := cl.Get(context.Background(), req.NamespacedName, updatedCluster); err != nil {
				t.Fatalf("get cluster after failed reconcile: %v", err)
			}
			if updatedCluster.Status.Phase != "Pending" {
				t.Errorf("status phase after ServiceMonitor failure = %q, want Pending", updatedCluster.Status.Phase)
			}

			result, err = r.Reconcile(context.Background(), req)
			if err != nil {
				t.Fatalf("retry Reconcile returned an error: %v", err)
			}
			if result != (ctrl.Result{}) {
				t.Fatalf("retry Reconcile result = %v, want zero result", result)
			}
			if operationCalls != 2 {
				t.Fatalf("%s calls = %d, want 2 (failed attempt plus retry)", tt.operation, operationCalls)
			}

			smErr := getServiceMonitor(t, cl, cluster.Name+"-server", cluster.Namespace)
			if tt.enabled {
				if smErr != nil {
					t.Fatalf("ServiceMonitor should exist after retry: %v", smErr)
				}
			} else if !apierrors.IsNotFound(smErr) {
				t.Fatalf("obsolete ServiceMonitor should be deleted after retry, got: %v", smErr)
			}
			if got := warningEventCount(recorder, tt.warningReason); got != 1 {
				t.Errorf("%s warning events = %d, want exactly 1", tt.warningReason, got)
			}
		})
	}
}

func TestReconcile_ServiceMonitorNoMatchDoesNotError(t *testing.T) {
	for _, enabled := range []bool{true, false} {
		name := "cleanup"
		if enabled {
			name = "create"
		}
		t.Run(name, func(t *testing.T) {
			cluster := &ojsv1alpha1.OJSCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "sm-no-match-" + name,
					Namespace:  "default",
					Finalizers: []string{ojsFinalizer},
				},
				Spec: ojsv1alpha1.OJSClusterSpec{
					Backend: ojsv1alpha1.BackendSpec{Type: "redis", URL: "redis://localhost:6379"},
					Monitoring: &ojsv1alpha1.MonitoringSpec{
						Enabled:        enabled,
						ServiceMonitor: enabled,
					},
				},
				Status: ojsv1alpha1.OJSClusterStatus{Phase: "Pending"},
			}
			cl := noServiceMonitorGVKClient(t, cluster)
			recorder := record.NewFakeRecorder(10)
			r := &OJSClusterReconciler{Client: cl, Scheme: newScheme(), Recorder: recorder}

			result, err := r.Reconcile(context.Background(), ctrl.Request{
				NamespacedName: types.NamespacedName{Name: cluster.Name, Namespace: cluster.Namespace},
			})
			if err != nil {
				t.Fatalf("Reconcile returned NoMatch error: %v", err)
			}
			if result != (ctrl.Result{}) {
				t.Fatalf("Reconcile result = %v, want zero result", result)
			}
			assertNoEvent(t, recorder)
		})
	}
}

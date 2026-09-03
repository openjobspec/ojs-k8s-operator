package controller

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	ojsv1alpha1 "github.com/openjobspec/ojs-k8s-operator/api/v1alpha1"
)

func assignedServiceSpec() corev1.ServiceSpec {
	return corev1.ServiceSpec{
		Type:                          corev1.ServiceTypeLoadBalancer,
		ClusterIP:                     "10.96.0.42",
		ClusterIPs:                    []string{"10.96.0.42"},
		IPFamilies:                    []corev1.IPFamily{corev1.IPv4Protocol},
		IPFamilyPolicy:                ptr.To(corev1.IPFamilyPolicySingleStack),
		HealthCheckNodePort:           32042,
		ExternalTrafficPolicy:         corev1.ServiceExternalTrafficPolicyLocal,
		InternalTrafficPolicy:         ptr.To(corev1.ServiceInternalTrafficPolicyLocal),
		AllocateLoadBalancerNodePorts: ptr.To(true),
		LoadBalancerClass:             ptr.To("example.com/load-balancer"),
		SessionAffinity:               corev1.ServiceAffinityClientIP,
		SessionAffinityConfig: &corev1.SessionAffinityConfig{
			ClientIP: &corev1.ClientIPConfig{TimeoutSeconds: ptr.To(int32(600))},
		},
	}
}

func withoutOperatorOwnedServiceFields(spec corev1.ServiceSpec) corev1.ServiceSpec {
	spec.Selector = nil
	spec.Ports = nil
	return spec
}

func serviceUpdatePreservesAssignedFields(want corev1.ServiceSpec) interceptor.Funcs {
	return interceptor.Funcs{
		Update: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.UpdateOption) error {
			svc, ok := obj.(*corev1.Service)
			if ok {
				gotAssigned := withoutOperatorOwnedServiceFields(svc.Spec)
				wantAssigned := withoutOperatorOwnedServiceFields(want)
				if !reflect.DeepEqual(gotAssigned, wantAssigned) {
					return fmt.Errorf("Service update changed API-assigned fields: got %+v, want %+v", gotAssigned, wantAssigned)
				}
			}
			return c.Update(ctx, obj, opts...)
		},
	}
}

func assertAssignedServiceFieldsPreserved(t *testing.T, got *corev1.Service, want corev1.ServiceSpec) {
	t.Helper()
	gotAssigned := withoutOperatorOwnedServiceFields(got.Spec)
	wantAssigned := withoutOperatorOwnedServiceFields(want)
	if !reflect.DeepEqual(gotAssigned, wantAssigned) {
		t.Errorf("API-assigned Service fields changed: got %+v, want %+v", gotAssigned, wantAssigned)
	}
}

func TestReconcileService_SecondReconcilePreservesAssignedFields(t *testing.T) {
	scheme := newScheme()
	cluster := &ojsv1alpha1.OJSCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "server-service",
			Namespace: "default",
			UID:       types.UID("server-cluster-uid"),
		},
	}

	assigned := assignedServiceSpec()
	assigned.Selector = map[string]string{"stale": "selector"}
	assigned.Ports = []corev1.ServicePort{
		{Name: "http", Protocol: corev1.ProtocolTCP, Port: 80, TargetPort: intstr.FromString("old-http"), NodePort: 30080},
		{Name: "metrics", Protocol: corev1.ProtocolTCP, Port: 90, TargetPort: intstr.FromString("old-metrics"), NodePort: 30090},
	}
	existing := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "server-service-server", Namespace: "default"},
		Spec:       assigned,
	}

	cl := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(cluster, existing).
		WithInterceptorFuncs(serviceUpdatePreservesAssignedFields(assigned)).
		Build()
	r := &OJSClusterReconciler{Client: cl, Scheme: scheme}

	if err := r.reconcileService(context.Background(), cluster); err != nil {
		t.Fatalf("second Service reconcile failed: %v", err)
	}

	got := &corev1.Service{}
	if err := cl.Get(context.Background(), client.ObjectKeyFromObject(existing), got); err != nil {
		t.Fatalf("get reconciled Service: %v", err)
	}
	assertAssignedServiceFieldsPreserved(t, got, assigned)

	want := desiredServerServiceSpec(labelsForCluster(cluster.Name))
	if !reflect.DeepEqual(got.Spec.Selector, want.Selector) {
		t.Errorf("selector = %v, want %v", got.Spec.Selector, want.Selector)
	}
	if !reflect.DeepEqual(got.Spec.Ports, []corev1.ServicePort{
		{Name: "http", Protocol: corev1.ProtocolTCP, Port: int32(defaultPort), TargetPort: intstr.FromString("http"), NodePort: 30080},
		{Name: "metrics", Protocol: corev1.ProtocolTCP, Port: int32(metricsPort), TargetPort: intstr.FromString("metrics"), NodePort: 30090},
	}) {
		t.Errorf("ports were not updated while preserving node ports: %+v", got.Spec.Ports)
	}
}

func TestReconcileEmbeddedRedis_SecondReconcilePreservesAssignedFields(t *testing.T) {
	scheme := newScheme()
	cluster := &ojsv1alpha1.OJSCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "redis-service",
			Namespace: "default",
			UID:       types.UID("redis-cluster-uid"),
		},
		Spec: ojsv1alpha1.OJSClusterSpec{
			Backend: ojsv1alpha1.BackendSpec{Type: "redis", Embedded: true},
		},
	}

	assigned := assignedServiceSpec()
	assigned.Selector = map[string]string{"stale": "selector"}
	assigned.Ports = []corev1.ServicePort{
		{Name: "redis", Protocol: corev1.ProtocolTCP, Port: 6380, TargetPort: intstr.FromString("old-redis"), NodePort: 30379},
	}
	existing := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "redis-service-redis", Namespace: "default"},
		Spec:       assigned,
	}

	cl := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(cluster, existing).
		WithInterceptorFuncs(serviceUpdatePreservesAssignedFields(assigned)).
		Build()
	r := &OJSClusterReconciler{Client: cl, Scheme: scheme}

	if err := r.reconcileEmbeddedRedis(context.Background(), cluster); err != nil {
		t.Fatalf("second embedded Redis reconcile failed: %v", err)
	}

	got := &corev1.Service{}
	if err := cl.Get(context.Background(), client.ObjectKeyFromObject(existing), got); err != nil {
		t.Fatalf("get reconciled Redis Service: %v", err)
	}
	assertAssignedServiceFieldsPreserved(t, got, assigned)

	want := desiredRedisServiceSpec(redisLabels(cluster.Name))
	if !reflect.DeepEqual(got.Spec.Selector, want.Selector) {
		t.Errorf("selector = %v, want %v", got.Spec.Selector, want.Selector)
	}
	if !reflect.DeepEqual(got.Spec.Ports, []corev1.ServicePort{
		{Name: "redis", Protocol: corev1.ProtocolTCP, Port: 6379, TargetPort: intstr.FromString("redis"), NodePort: 30379},
	}) {
		t.Errorf("ports were not updated while preserving node port: %+v", got.Spec.Ports)
	}
}

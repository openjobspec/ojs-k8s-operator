package controller

import (
	"context"
	"testing"

	policyv1 "k8s.io/api/policy/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	ojsv1alpha1 "github.com/openjobspec/ojs-k8s-operator/api/v1alpha1"
)

func pdbOwnerReference(cluster *ojsv1alpha1.OJSCluster) metav1.OwnerReference {
	controller := true
	return metav1.OwnerReference{
		APIVersion: ojsv1alpha1.GroupVersion.String(),
		Kind:       "OJSCluster",
		Name:       cluster.Name,
		UID:        cluster.UID,
		Controller: &controller,
	}
}

func TestReconcilePDB_RemovesOwnedPDBWhenNoLongerDesired(t *testing.T) {
	tests := []struct {
		name    string
		cluster *ojsv1alpha1.OJSCluster
	}{
		{
			name: "single replica",
			cluster: &ojsv1alpha1.OJSCluster{
				ObjectMeta: metav1.ObjectMeta{Name: "single", Namespace: "default", UID: types.UID("single-uid")},
				Spec:       ojsv1alpha1.OJSClusterSpec{Replicas: int32Ptr(1)},
			},
		},
		{
			name: "explicitly disabled",
			cluster: &ojsv1alpha1.OJSCluster{
				ObjectMeta: metav1.ObjectMeta{Name: "disabled", Namespace: "default", UID: types.UID("disabled-uid")},
				Spec: ojsv1alpha1.OJSClusterSpec{
					Replicas:            int32Ptr(3),
					PodDisruptionBudget: &ojsv1alpha1.PDBSpec{Enabled: ptr.To(false)},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pdb := &policyv1.PodDisruptionBudget{
				ObjectMeta: metav1.ObjectMeta{
					Name:            tt.cluster.Name + "-server",
					Namespace:       tt.cluster.Namespace,
					OwnerReferences: []metav1.OwnerReference{pdbOwnerReference(tt.cluster)},
				},
			}
			scheme := newScheme()
			cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(tt.cluster, pdb).Build()
			r := &OJSClusterReconciler{Client: cl, Scheme: scheme}

			if err := r.reconcilePDB(context.Background(), tt.cluster); err != nil {
				t.Fatalf("reconcilePDB returned an error: %v", err)
			}

			err := cl.Get(context.Background(), client.ObjectKeyFromObject(pdb), &policyv1.PodDisruptionBudget{})
			if !apierrors.IsNotFound(err) {
				t.Errorf("expected owned PDB to be deleted, Get returned: %v", err)
			}
		})
	}
}

func TestReconcilePDB_LeavesUnownedSameNamePDB(t *testing.T) {
	cluster := &ojsv1alpha1.OJSCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "unowned", Namespace: "default", UID: types.UID("cluster-uid")},
		Spec:       ojsv1alpha1.OJSClusterSpec{Replicas: int32Ptr(1)},
	}
	pdb := &policyv1.PodDisruptionBudget{
		ObjectMeta: metav1.ObjectMeta{Name: "unowned-server", Namespace: "default"},
	}
	scheme := newScheme()
	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(cluster, pdb).Build()
	r := &OJSClusterReconciler{Client: cl, Scheme: scheme}

	if err := r.reconcilePDB(context.Background(), cluster); err != nil {
		t.Fatalf("reconcilePDB returned an error: %v", err)
	}
	if err := cl.Get(context.Background(), client.ObjectKeyFromObject(pdb), &policyv1.PodDisruptionBudget{}); err != nil {
		t.Errorf("expected unowned same-name PDB to remain, got: %v", err)
	}
}

func TestDeletePDB_MissingAndNoMatchAreNoOps(t *testing.T) {
	cluster := &ojsv1alpha1.OJSCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "absent", Namespace: "default"},
	}
	scheme := newScheme()

	t.Run("not found", func(t *testing.T) {
		cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(cluster).Build()
		r := &OJSClusterReconciler{Client: cl, Scheme: scheme}
		if err := r.deletePDB(context.Background(), cluster); err != nil {
			t.Errorf("expected NotFound to be a no-op, got: %v", err)
		}
	})

	t.Run("no match", func(t *testing.T) {
		noMatch := &apimeta.NoKindMatchError{
			GroupKind:        policyv1.SchemeGroupVersion.WithKind("PodDisruptionBudget").GroupKind(),
			SearchedVersions: []string{policyv1.SchemeGroupVersion.Version},
		}
		cl := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(cluster).
			WithInterceptorFuncs(interceptor.Funcs{
				Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
					if _, ok := obj.(*policyv1.PodDisruptionBudget); ok {
						return noMatch
					}
					return c.Get(ctx, key, obj, opts...)
				},
			}).
			Build()
		r := &OJSClusterReconciler{Client: cl, Scheme: scheme}
		if err := r.deletePDB(context.Background(), cluster); err != nil {
			t.Errorf("expected NoMatch to be a no-op, got: %v", err)
		}
	})
}

func TestDeletePDB_DeleteNotFoundAndNoMatchAreNoOps(t *testing.T) {
	cluster := &ojsv1alpha1.OJSCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "delete-race",
			Namespace: "default",
			UID:       types.UID("delete-race-uid"),
		},
	}
	pdb := &policyv1.PodDisruptionBudget{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "delete-race-server",
			Namespace:       "default",
			OwnerReferences: []metav1.OwnerReference{pdbOwnerReference(cluster)},
		},
	}
	noMatch := &apimeta.NoKindMatchError{
		GroupKind:        policyv1.SchemeGroupVersion.WithKind("PodDisruptionBudget").GroupKind(),
		SearchedVersions: []string{policyv1.SchemeGroupVersion.Version},
	}

	tests := []struct {
		name      string
		deleteErr error
	}{
		{
			name: "not found",
			deleteErr: apierrors.NewNotFound(
				schema.GroupResource{Group: policyv1.GroupName, Resource: "poddisruptionbudgets"},
				pdb.Name,
			),
		},
		{name: "no match", deleteErr: noMatch},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scheme := newScheme()
			cl := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(cluster.DeepCopy(), pdb.DeepCopy()).
				WithInterceptorFuncs(interceptor.Funcs{
					Delete: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.DeleteOption) error {
						if _, ok := obj.(*policyv1.PodDisruptionBudget); ok {
							return tt.deleteErr
						}
						return c.Delete(ctx, obj, opts...)
					},
				}).
				Build()
			r := &OJSClusterReconciler{Client: cl, Scheme: scheme}

			if err := r.deletePDB(context.Background(), cluster); err != nil {
				t.Errorf("expected delete-time %s to be a no-op, got: %v", tt.name, err)
			}
		})
	}
}

package webhook

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	ojsv1alpha1 "github.com/openjobspec/ojs-k8s-operator/api/v1alpha1"
)

// ValidBackendTypes lists all supported OJS backend types.
//
// It is exported for backward-compatible introspection (e.g. callers that
// want to enumerate supported types) but is NOT consulted by
// validateOJSCluster: a mutable package-level map read concurrently by
// admission requests (with no synchronization) would be a data race the
// moment any caller wrote to it. Validation instead checks against
// validBackendTypes, an unexported, never-mutated-after-init set built from
// the same values. Mutating ValidBackendTypes at runtime has always been
// unsafe and now has no effect on validation; this is a deliberate,
// documented behavior change from silently trusting caller mutations.
var ValidBackendTypes = map[string]bool{
	"redis":    true,
	"postgres": true,
	"nats":     true,
	"kafka":    true,
	"sqs":      true,
	"lite":     true,
}

// validBackendTypes is the immutable set actually consulted by validation.
// It is derived from ValidBackendTypes once at package init and never
// written to again, so concurrent admission requests can read it without
// synchronization or risk of a data race.
var validBackendTypes = func() map[string]struct{} {
	set := make(map[string]struct{}, len(ValidBackendTypes))
	for backendType, valid := range ValidBackendTypes {
		if valid {
			set[backendType] = struct{}{}
		}
	}
	return set
}()

// OJSClusterValidator validates OJSCluster resources.
type OJSClusterValidator struct{}

var _ admission.CustomValidator = &OJSClusterValidator{}

func (v *OJSClusterValidator) ValidateCreate(_ context.Context, obj runtime.Object) (admission.Warnings, error) {
	cluster, ok := obj.(*ojsv1alpha1.OJSCluster)
	if !ok {
		return nil, fmt.Errorf("expected OJSCluster, got %T", obj)
	}
	return nil, validateOJSCluster(cluster)
}

func (v *OJSClusterValidator) ValidateUpdate(_ context.Context, _, newObj runtime.Object) (admission.Warnings, error) {
	cluster, ok := newObj.(*ojsv1alpha1.OJSCluster)
	if !ok {
		return nil, fmt.Errorf("expected OJSCluster, got %T", newObj)
	}
	return nil, validateOJSCluster(cluster)
}

func (v *OJSClusterValidator) ValidateDelete(_ context.Context, _ runtime.Object) (admission.Warnings, error) {
	return nil, nil
}

func validateOJSCluster(cluster *ojsv1alpha1.OJSCluster) error {
	if cluster.Spec.Backend.Type == "" {
		return fmt.Errorf("spec.backend.type is required")
	}
	if _, ok := validBackendTypes[cluster.Spec.Backend.Type]; !ok {
		return fmt.Errorf("spec.backend.type %q is not valid; must be one of: redis, postgres, nats, kafka, sqs, lite",
			cluster.Spec.Backend.Type)
	}
	if cluster.Spec.Replicas != nil && *cluster.Spec.Replicas < 1 {
		return fmt.Errorf("spec.replicas must be >= 1, got %d", *cluster.Spec.Replicas)
	}
	if cluster.Spec.AutoScaling != nil && cluster.Spec.AutoScaling.Enabled {
		if cluster.Spec.AutoScaling.MinReplicas < 1 {
			return fmt.Errorf("spec.autoScaling.minReplicas must be >= 1")
		}
		if cluster.Spec.AutoScaling.MaxReplicas < cluster.Spec.AutoScaling.MinReplicas {
			return fmt.Errorf("spec.autoScaling.maxReplicas must be >= minReplicas")
		}
	}
	return nil
}

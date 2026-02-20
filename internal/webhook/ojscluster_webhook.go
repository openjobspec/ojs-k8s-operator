package webhook

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	ojsv1alpha1 "github.com/openjobspec/ojs-k8s-operator/api/v1alpha1"
)

// ValidBackendTypes lists all supported OJS backend types.
var ValidBackendTypes = map[string]bool{
	"redis":    true,
	"postgres": true,
	"nats":     true,
	"kafka":    true,
	"sqs":      true,
	"lite":     true,
}

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
	if !ValidBackendTypes[cluster.Spec.Backend.Type] {
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

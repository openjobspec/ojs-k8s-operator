package webhook

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	ojsv1alpha1 "github.com/openjobspec/ojs-k8s-operator/api/v1alpha1"
)

// OJSWorkerValidator validates OJSWorker resources.
type OJSWorkerValidator struct{}

var _ admission.CustomValidator = &OJSWorkerValidator{}

func (v *OJSWorkerValidator) ValidateCreate(_ context.Context, obj runtime.Object) (admission.Warnings, error) {
	worker, ok := obj.(*ojsv1alpha1.OJSWorker)
	if !ok {
		return nil, fmt.Errorf("expected OJSWorker, got %T", obj)
	}
	return nil, validateOJSWorker(worker)
}

func (v *OJSWorkerValidator) ValidateUpdate(_ context.Context, _, newObj runtime.Object) (admission.Warnings, error) {
	worker, ok := newObj.(*ojsv1alpha1.OJSWorker)
	if !ok {
		return nil, fmt.Errorf("expected OJSWorker, got %T", newObj)
	}
	return nil, validateOJSWorker(worker)
}

func (v *OJSWorkerValidator) ValidateDelete(_ context.Context, _ runtime.Object) (admission.Warnings, error) {
	return nil, nil
}

func validateOJSWorker(worker *ojsv1alpha1.OJSWorker) error {
	if worker.Spec.ClusterRef == "" {
		return fmt.Errorf("spec.clusterRef is required")
	}
	if worker.Spec.Image == "" {
		return fmt.Errorf("spec.image is required")
	}
	if len(worker.Spec.JobTypes) == 0 {
		return fmt.Errorf("spec.jobTypes must contain at least one job type")
	}
	for i, jt := range worker.Spec.JobTypes {
		if jt == "" {
			return fmt.Errorf("spec.jobTypes[%d] must not be empty", i)
		}
	}
	if worker.Spec.Concurrency < 0 {
		return fmt.Errorf("spec.concurrency must be >= 0, got %d", worker.Spec.Concurrency)
	}
	if len(worker.Spec.Queues) > 0 {
		for i, q := range worker.Spec.Queues {
			if q == "" {
				return fmt.Errorf("spec.queues[%d] must not be empty", i)
			}
		}
	}
	if worker.Spec.AutoScaling != nil && worker.Spec.AutoScaling.Enabled {
		if worker.Spec.AutoScaling.MinReplicas < 1 {
			return fmt.Errorf("spec.autoScaling.minReplicas must be >= 1")
		}
		if worker.Spec.AutoScaling.MaxReplicas < worker.Spec.AutoScaling.MinReplicas {
			return fmt.Errorf("spec.autoScaling.maxReplicas must be >= minReplicas")
		}
	}
	return nil
}

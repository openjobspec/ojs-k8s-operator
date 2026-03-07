package webhook

import (
	"context"
	"testing"

	ojsv1alpha1 "github.com/openjobspec/ojs-k8s-operator/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestOJSClusterValidator_ValidateCreate(t *testing.T) {
	v := &OJSClusterValidator{}
	cluster := &ojsv1alpha1.OJSCluster{
		Spec: ojsv1alpha1.OJSClusterSpec{
			Backend:  ojsv1alpha1.BackendSpec{Type: "redis"},
			Replicas: int32Ptr(2),
		},
	}
	warnings, err := v.ValidateCreate(context.Background(), cluster)
	if err != nil {
		t.Errorf("expected valid, got: %v", err)
	}
	if len(warnings) > 0 {
		t.Errorf("unexpected warnings: %v", warnings)
	}
}

func TestOJSClusterValidator_ValidateCreate_Invalid(t *testing.T) {
	v := &OJSClusterValidator{}
	cluster := &ojsv1alpha1.OJSCluster{
		Spec: ojsv1alpha1.OJSClusterSpec{
			Backend: ojsv1alpha1.BackendSpec{},
		},
	}
	_, err := v.ValidateCreate(context.Background(), cluster)
	if err == nil {
		t.Error("expected error for empty backend type")
	}
}

func TestOJSClusterValidator_ValidateCreate_WrongType(t *testing.T) {
	v := &OJSClusterValidator{}
	worker := &ojsv1alpha1.OJSWorker{}
	_, err := v.ValidateCreate(context.Background(), worker)
	if err == nil {
		t.Error("expected error for wrong type")
	}
}

func TestOJSClusterValidator_ValidateUpdate(t *testing.T) {
	v := &OJSClusterValidator{}
	old := &ojsv1alpha1.OJSCluster{
		Spec: ojsv1alpha1.OJSClusterSpec{
			Backend: ojsv1alpha1.BackendSpec{Type: "redis"},
		},
	}
	new := &ojsv1alpha1.OJSCluster{
		Spec: ojsv1alpha1.OJSClusterSpec{
			Backend:  ojsv1alpha1.BackendSpec{Type: "postgres"},
			Replicas: int32Ptr(3),
		},
	}
	_, err := v.ValidateUpdate(context.Background(), old, new)
	if err != nil {
		t.Errorf("expected valid update, got: %v", err)
	}
}

func TestOJSClusterValidator_ValidateUpdate_WrongType(t *testing.T) {
	v := &OJSClusterValidator{}
	_, err := v.ValidateUpdate(context.Background(), &ojsv1alpha1.OJSWorker{}, &ojsv1alpha1.OJSWorker{})
	if err == nil {
		t.Error("expected error for wrong type")
	}
}

func TestOJSClusterValidator_ValidateDelete(t *testing.T) {
	v := &OJSClusterValidator{}
	warnings, err := v.ValidateDelete(context.Background(), &ojsv1alpha1.OJSCluster{})
	if err != nil {
		t.Errorf("expected no error on delete, got: %v", err)
	}
	if len(warnings) > 0 {
		t.Errorf("unexpected warnings: %v", warnings)
	}
}

func TestOJSWorkerValidator_ValidateCreate(t *testing.T) {
	v := &OJSWorkerValidator{}
	worker := &ojsv1alpha1.OJSWorker{
		ObjectMeta: metav1.ObjectMeta{Name: "test"},
		Spec: ojsv1alpha1.OJSWorkerSpec{
			ClusterRef: "cluster",
			JobTypes:   []string{"job.type"},
			Image:      "worker:latest",
		},
	}
	warnings, err := v.ValidateCreate(context.Background(), worker)
	if err != nil {
		t.Errorf("expected valid, got: %v", err)
	}
	if len(warnings) > 0 {
		t.Errorf("unexpected warnings: %v", warnings)
	}
}

func TestOJSWorkerValidator_ValidateCreate_Invalid(t *testing.T) {
	v := &OJSWorkerValidator{}
	worker := &ojsv1alpha1.OJSWorker{
		Spec: ojsv1alpha1.OJSWorkerSpec{},
	}
	_, err := v.ValidateCreate(context.Background(), worker)
	if err == nil {
		t.Error("expected error for empty worker spec")
	}
}

func TestOJSWorkerValidator_ValidateCreate_WrongType(t *testing.T) {
	v := &OJSWorkerValidator{}
	cluster := &ojsv1alpha1.OJSCluster{}
	_, err := v.ValidateCreate(context.Background(), cluster)
	if err == nil {
		t.Error("expected error for wrong type")
	}
}

func TestOJSWorkerValidator_ValidateUpdate(t *testing.T) {
	v := &OJSWorkerValidator{}
	old := &ojsv1alpha1.OJSWorker{
		Spec: ojsv1alpha1.OJSWorkerSpec{
			ClusterRef: "cluster",
			JobTypes:   []string{"job.type"},
			Image:      "worker:v1",
		},
	}
	new := &ojsv1alpha1.OJSWorker{
		Spec: ojsv1alpha1.OJSWorkerSpec{
			ClusterRef: "cluster",
			JobTypes:   []string{"job.type", "other.type"},
			Image:      "worker:v2",
		},
	}
	_, err := v.ValidateUpdate(context.Background(), old, new)
	if err != nil {
		t.Errorf("expected valid update, got: %v", err)
	}
}

func TestOJSWorkerValidator_ValidateUpdate_WrongType(t *testing.T) {
	v := &OJSWorkerValidator{}
	_, err := v.ValidateUpdate(context.Background(), &ojsv1alpha1.OJSCluster{}, &ojsv1alpha1.OJSCluster{})
	if err == nil {
		t.Error("expected error for wrong type")
	}
}

func TestOJSWorkerValidator_ValidateDelete(t *testing.T) {
	v := &OJSWorkerValidator{}
	warnings, err := v.ValidateDelete(context.Background(), &ojsv1alpha1.OJSWorker{})
	if err != nil {
		t.Errorf("expected no error on delete, got: %v", err)
	}
	if len(warnings) > 0 {
		t.Errorf("unexpected warnings: %v", warnings)
	}
}

func TestValidateOJSCluster_AllBackendTypes(t *testing.T) {
	for _, bt := range []string{"redis", "postgres", "nats", "kafka", "sqs", "lite"} {
		t.Run(bt, func(t *testing.T) {
			cluster := &ojsv1alpha1.OJSCluster{
				Spec: ojsv1alpha1.OJSClusterSpec{
					Backend: ojsv1alpha1.BackendSpec{Type: bt},
				},
			}
			if err := validateOJSCluster(cluster); err != nil {
				t.Errorf("backend type %q should be valid, got: %v", bt, err)
			}
		})
	}
}

func TestValidateOJSCluster_NegativeReplicas(t *testing.T) {
	cluster := &ojsv1alpha1.OJSCluster{
		Spec: ojsv1alpha1.OJSClusterSpec{
			Backend:  ojsv1alpha1.BackendSpec{Type: "redis"},
			Replicas: int32Ptr(-1),
		},
	}
	if err := validateOJSCluster(cluster); err == nil {
		t.Error("expected error for negative replicas")
	}
}

func TestValidateOJSCluster_AutoScalingMinZero(t *testing.T) {
	cluster := &ojsv1alpha1.OJSCluster{
		Spec: ojsv1alpha1.OJSClusterSpec{
			Backend: ojsv1alpha1.BackendSpec{Type: "redis"},
			AutoScaling: &ojsv1alpha1.AutoScalingSpec{
				Enabled:     true,
				MinReplicas: 0,
				MaxReplicas: 5,
			},
		},
	}
	if err := validateOJSCluster(cluster); err == nil {
		t.Error("expected error for minReplicas < 1")
	}
}

func TestValidateOJSCluster_AutoScalingDisabled(t *testing.T) {
	cluster := &ojsv1alpha1.OJSCluster{
		Spec: ojsv1alpha1.OJSClusterSpec{
			Backend: ojsv1alpha1.BackendSpec{Type: "redis"},
			AutoScaling: &ojsv1alpha1.AutoScalingSpec{
				Enabled:     false,
				MinReplicas: 0,
				MaxReplicas: 0,
			},
		},
	}
	if err := validateOJSCluster(cluster); err != nil {
		t.Errorf("disabled autoscaling should not validate min/max, got: %v", err)
	}
}

func TestValidateOJSWorker_NegativeConcurrency(t *testing.T) {
	worker := &ojsv1alpha1.OJSWorker{
		Spec: ojsv1alpha1.OJSWorkerSpec{
			ClusterRef:  "cluster",
			JobTypes:    []string{"job.type"},
			Image:       "worker:latest",
			Concurrency: -1,
		},
	}
	if err := validateOJSWorker(worker); err == nil {
		t.Error("expected error for negative concurrency")
	}
}

func TestValidateOJSWorker_EmptyQueues(t *testing.T) {
	worker := &ojsv1alpha1.OJSWorker{
		Spec: ojsv1alpha1.OJSWorkerSpec{
			ClusterRef: "cluster",
			JobTypes:   []string{"job.type"},
			Image:      "worker:latest",
			Queues:     []string{},
		},
	}
	if err := validateOJSWorker(worker); err != nil {
		t.Errorf("empty queues list should be valid, got: %v", err)
	}
}

func TestValidateOJSWorker_EmptyQueueEntry(t *testing.T) {
	worker := &ojsv1alpha1.OJSWorker{
		Spec: ojsv1alpha1.OJSWorkerSpec{
			ClusterRef: "cluster",
			JobTypes:   []string{"job.type"},
			Image:      "worker:latest",
			Queues:     []string{"default", ""},
		},
	}
	if err := validateOJSWorker(worker); err == nil {
		t.Error("expected error for empty queue entry")
	}
}

func TestValidateOJSWorker_AutoScalingMaxLessThanMin(t *testing.T) {
	worker := &ojsv1alpha1.OJSWorker{
		Spec: ojsv1alpha1.OJSWorkerSpec{
			ClusterRef: "cluster",
			JobTypes:   []string{"job.type"},
			Image:      "worker:latest",
			AutoScaling: &ojsv1alpha1.WorkerAutoScalingSpec{
				Enabled:     true,
				MinReplicas: 10,
				MaxReplicas: 5,
			},
		},
	}
	if err := validateOJSWorker(worker); err == nil {
		t.Error("expected error for maxReplicas < minReplicas")
	}
}

func TestValidateOJSWorker_AutoScalingDisabledSkipsValidation(t *testing.T) {
	worker := &ojsv1alpha1.OJSWorker{
		Spec: ojsv1alpha1.OJSWorkerSpec{
			ClusterRef: "cluster",
			JobTypes:   []string{"job.type"},
			Image:      "worker:latest",
			AutoScaling: &ojsv1alpha1.WorkerAutoScalingSpec{
				Enabled:     false,
				MinReplicas: 0,
				MaxReplicas: 0,
			},
		},
	}
	if err := validateOJSWorker(worker); err != nil {
		t.Errorf("disabled autoscaling should not validate, got: %v", err)
	}
}

func TestValidateOJSWorker_MultipleJobTypes(t *testing.T) {
	worker := &ojsv1alpha1.OJSWorker{
		Spec: ojsv1alpha1.OJSWorkerSpec{
			ClusterRef: "cluster",
			JobTypes:   []string{"email.send", "sms.send", "push.notify"},
			Image:      "worker:latest",
		},
	}
	if err := validateOJSWorker(worker); err != nil {
		t.Errorf("multiple job types should be valid, got: %v", err)
	}
}

package webhook

import (
	"testing"

	ojsv1alpha1 "github.com/openjobspec/ojs-k8s-operator/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func int32Ptr(i int32) *int32 { return &i }

func TestValidateOJSCluster_ValidBackendTypes(t *testing.T) {
	for _, bt := range []string{"redis", "postgres", "nats", "kafka", "sqs", "lite"} {
		cluster := &ojsv1alpha1.OJSCluster{
			Spec: ojsv1alpha1.OJSClusterSpec{
				Backend:  ojsv1alpha1.BackendSpec{Type: bt},
				Replicas: int32Ptr(1),
			},
		}
		if err := validateOJSCluster(cluster); err != nil {
			t.Errorf("backend type %q should be valid, got: %v", bt, err)
		}
	}
}

func TestValidateOJSCluster_InvalidBackendType(t *testing.T) {
	cluster := &ojsv1alpha1.OJSCluster{
		Spec: ojsv1alpha1.OJSClusterSpec{
			Backend: ojsv1alpha1.BackendSpec{Type: "mongodb"},
		},
	}
	if err := validateOJSCluster(cluster); err == nil {
		t.Error("expected error for invalid backend type")
	}
}

func TestValidateOJSCluster_EmptyBackendType(t *testing.T) {
	cluster := &ojsv1alpha1.OJSCluster{
		Spec: ojsv1alpha1.OJSClusterSpec{
			Backend: ojsv1alpha1.BackendSpec{},
		},
	}
	if err := validateOJSCluster(cluster); err == nil {
		t.Error("expected error for empty backend type")
	}
}

func TestValidateOJSCluster_ReplicasZero(t *testing.T) {
	cluster := &ojsv1alpha1.OJSCluster{
		Spec: ojsv1alpha1.OJSClusterSpec{
			Backend:  ojsv1alpha1.BackendSpec{Type: "redis"},
			Replicas: int32Ptr(0),
		},
	}
	if err := validateOJSCluster(cluster); err == nil {
		t.Error("expected error for replicas < 1")
	}
}

func TestValidateOJSCluster_ReplicasNil(t *testing.T) {
	cluster := &ojsv1alpha1.OJSCluster{
		Spec: ojsv1alpha1.OJSClusterSpec{
			Backend: ojsv1alpha1.BackendSpec{Type: "redis"},
		},
	}
	if err := validateOJSCluster(cluster); err != nil {
		t.Errorf("nil replicas should be valid (uses default), got: %v", err)
	}
}

func TestValidateOJSCluster_AutoScalingInvalid(t *testing.T) {
	cluster := &ojsv1alpha1.OJSCluster{
		Spec: ojsv1alpha1.OJSClusterSpec{
			Backend: ojsv1alpha1.BackendSpec{Type: "redis"},
			AutoScaling: &ojsv1alpha1.AutoScalingSpec{
				Enabled:     true,
				MinReplicas: 5,
				MaxReplicas: 2,
			},
		},
	}
	if err := validateOJSCluster(cluster); err == nil {
		t.Error("expected error for maxReplicas < minReplicas")
	}
}

func TestValidateOJSWorker_Valid(t *testing.T) {
	worker := &ojsv1alpha1.OJSWorker{
		ObjectMeta: metav1.ObjectMeta{Name: "test"},
		Spec: ojsv1alpha1.OJSWorkerSpec{
			ClusterRef:  "my-cluster",
			JobTypes:    []string{"email.send"},
			Queues:      []string{"default"},
			Concurrency: 5,
			Image:       "worker:latest",
		},
	}
	if err := validateOJSWorker(worker); err != nil {
		t.Errorf("expected valid, got: %v", err)
	}
}

func TestValidateOJSWorker_NoJobTypes(t *testing.T) {
	worker := &ojsv1alpha1.OJSWorker{
		Spec: ojsv1alpha1.OJSWorkerSpec{
			ClusterRef: "my-cluster",
			Image:      "worker:latest",
		},
	}
	if err := validateOJSWorker(worker); err == nil {
		t.Error("expected error for empty jobTypes")
	}
}

func TestValidateOJSWorker_EmptyJobType(t *testing.T) {
	worker := &ojsv1alpha1.OJSWorker{
		Spec: ojsv1alpha1.OJSWorkerSpec{
			ClusterRef: "my-cluster",
			JobTypes:   []string{"email.send", ""},
			Image:      "worker:latest",
		},
	}
	if err := validateOJSWorker(worker); err == nil {
		t.Error("expected error for empty job type entry")
	}
}

func TestValidateOJSWorker_NoClusterRef(t *testing.T) {
	worker := &ojsv1alpha1.OJSWorker{
		Spec: ojsv1alpha1.OJSWorkerSpec{
			JobTypes: []string{"email.send"},
			Image:    "worker:latest",
		},
	}
	if err := validateOJSWorker(worker); err == nil {
		t.Error("expected error for empty clusterRef")
	}
}

func TestValidateOJSWorker_NoImage(t *testing.T) {
	worker := &ojsv1alpha1.OJSWorker{
		Spec: ojsv1alpha1.OJSWorkerSpec{
			ClusterRef: "my-cluster",
			JobTypes:   []string{"email.send"},
		},
	}
	if err := validateOJSWorker(worker); err == nil {
		t.Error("expected error for empty image")
	}
}

func TestValidateOJSWorker_AutoScalingInvalid(t *testing.T) {
	worker := &ojsv1alpha1.OJSWorker{
		Spec: ojsv1alpha1.OJSWorkerSpec{
			ClusterRef: "my-cluster",
			JobTypes:   []string{"email.send"},
			Image:      "worker:latest",
			AutoScaling: &ojsv1alpha1.WorkerAutoScalingSpec{
				Enabled:     true,
				MinReplicas: 0,
				MaxReplicas: 5,
			},
		},
	}
	if err := validateOJSWorker(worker); err == nil {
		t.Error("expected error for minReplicas < 1")
	}
}

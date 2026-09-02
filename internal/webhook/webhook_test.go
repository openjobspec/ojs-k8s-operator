package webhook

import (
	"sync"
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

// TestValidateOJSCluster_ExportedMapMutationHasNoEffect documents the
// intentional decoupling between the exported ValidBackendTypes map and the
// validation logic: validateOJSCluster consults an internal immutable
// snapshot, so mutating ValidBackendTypes at runtime does not change
// validation outcomes (and, crucially, cannot introduce a data race with
// concurrent admission requests).
func TestValidateOJSCluster_ExportedMapMutationHasNoEffect(t *testing.T) {
	// Adding a bogus "true" entry must not make it valid.
	ValidBackendTypes["mongodb"] = true
	defer delete(ValidBackendTypes, "mongodb")

	cluster := &ojsv1alpha1.OJSCluster{
		Spec: ojsv1alpha1.OJSClusterSpec{
			Backend: ojsv1alpha1.BackendSpec{Type: "mongodb"},
		},
	}
	if err := validateOJSCluster(cluster); err == nil {
		t.Error("expected mutation of the exported ValidBackendTypes map to have no effect on validation")
	}

	// Removing a previously-valid entry must not make it invalid.
	delete(ValidBackendTypes, "redis")
	defer func() { ValidBackendTypes["redis"] = true }()

	redisCluster := &ojsv1alpha1.OJSCluster{
		Spec: ojsv1alpha1.OJSClusterSpec{
			Backend:  ojsv1alpha1.BackendSpec{Type: "redis"},
			Replicas: int32Ptr(1),
		},
	}
	if err := validateOJSCluster(redisCluster); err != nil {
		t.Errorf("expected 'redis' to remain valid despite deletion from ValidBackendTypes, got: %v", err)
	}
}

// TestValidateOJSCluster_ConcurrentValidationRace exercises validateOJSCluster
// from many goroutines concurrently; run with -race to confirm the backend
// type check has no data race (it reads only the immutable validBackendTypes
// snapshot, never the exported mutable ValidBackendTypes map).
func TestValidateOJSCluster_ConcurrentValidationRace(t *testing.T) {
	var wg sync.WaitGroup
	backendTypes := []string{"redis", "postgres", "nats", "kafka", "sqs", "lite", "invalid"}

	for i := 0; i < 20; i++ {
		wg.Add(1)
		bt := backendTypes[i%len(backendTypes)]
		go func(backendType string) {
			defer wg.Done()
			cluster := &ojsv1alpha1.OJSCluster{
				Spec: ojsv1alpha1.OJSClusterSpec{
					Backend:  ojsv1alpha1.BackendSpec{Type: backendType},
					Replicas: int32Ptr(1),
				},
			}
			_ = validateOJSCluster(cluster)
		}(bt)
	}
	wg.Wait()
}

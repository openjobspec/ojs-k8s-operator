package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// OJSWorkerSpec defines the desired state of an OJS worker deployment.
type OJSWorkerSpec struct {
	// ClusterRef references the OJSCluster this worker connects to.
	ClusterRef string `json:"clusterRef"`
	// JobTypes lists the job types this worker handles.
	JobTypes []string `json:"jobTypes"`
	// Queues lists the queues this worker processes (default: ["default"]).
	Queues []string `json:"queues,omitempty"`
	// Concurrency is the number of concurrent jobs per worker pod.
	Concurrency int32 `json:"concurrency,omitempty"`
	// Replicas is the desired number of worker pods.
	Replicas *int32 `json:"replicas,omitempty"`
	// Image for the worker container.
	Image string `json:"image"`
	// Command override for the worker container.
	Command []string `json:"command,omitempty"`
	// Env additional environment variables for the worker pods.
	Env []corev1.EnvVar `json:"env,omitempty"`
	// Resources defines resource requirements for worker pods.
	Resources corev1.ResourceRequirements `json:"resources,omitempty"`
	// AutoScaling configures queue-depth-based auto-scaling.
	AutoScaling *WorkerAutoScalingSpec `json:"autoScaling,omitempty"`
	// GracefulShutdown configures graceful shutdown behavior.
	GracefulShutdown *GracefulShutdownSpec `json:"gracefulShutdown,omitempty"`
}

// WorkerAutoScalingSpec defines auto-scaling for worker pods based on queue metrics.
type WorkerAutoScalingSpec struct {
	// Enabled enables auto-scaling.
	Enabled bool `json:"enabled"`
	// MinReplicas is the minimum number of worker pods.
	MinReplicas int32 `json:"minReplicas"`
	// MaxReplicas is the maximum number of worker pods.
	MaxReplicas int32 `json:"maxReplicas"`
	// TargetJobsPerWorker is the desired number of pending jobs per worker replica.
	TargetJobsPerWorker int64 `json:"targetJobsPerWorker"`
	// ScaleUpThreshold triggers scale-up when queue depth exceeds this value.
	ScaleUpThreshold int64 `json:"scaleUpThreshold,omitempty"`
	// ScaleDownDelay prevents scale-down for this duration after the last scale-up (e.g., "5m").
	ScaleDownDelay string `json:"scaleDownDelay,omitempty"`
	// PollingInterval is how often to check queue metrics (e.g., "30s").
	PollingInterval string `json:"pollingInterval,omitempty"`
}

// GracefulShutdownSpec configures how workers drain during shutdown.
type GracefulShutdownSpec struct {
	// TimeoutSeconds is the maximum time to wait for active jobs to complete.
	TimeoutSeconds int32 `json:"timeoutSeconds,omitempty"`
	// DrainBeforeShutdown waits for active jobs to complete before terminating.
	DrainBeforeShutdown bool `json:"drainBeforeShutdown,omitempty"`
}

// OJSWorkerStatus defines the observed state of an OJS worker deployment.
type OJSWorkerStatus struct {
	// Phase of the worker: Pending, Running, Scaling, Draining, Error.
	Phase string `json:"phase"`
	// Replicas is the total number of worker pods.
	Replicas int32 `json:"replicas"`
	// ReadyReplicas is the number of ready worker pods.
	ReadyReplicas int32 `json:"readyReplicas"`
	// ActiveJobs is the total number of jobs being processed by this worker.
	ActiveJobs int64 `json:"activeJobs"`
	// QueueDepth is the current pending job count for this worker's queues.
	QueueDepth int64 `json:"queueDepth"`
	// LastScaleTime records when the last scaling event occurred.
	LastScaleTime *metav1.Time `json:"lastScaleTime,omitempty"`
	// Conditions represent the latest available observations.
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:subresource:scale:specpath=.spec.replicas,statuspath=.status.replicas
// +kubebuilder:printcolumn:name="Cluster",type=string,JSONPath=`.spec.clusterRef`
// +kubebuilder:printcolumn:name="Replicas",type=integer,JSONPath=`.status.replicas`
// +kubebuilder:printcolumn:name="Ready",type=integer,JSONPath=`.status.readyReplicas`
// +kubebuilder:printcolumn:name="Queue Depth",type=integer,JSONPath=`.status.queueDepth`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// OJSWorker is the Schema for the ojsworkers API.
type OJSWorker struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   OJSWorkerSpec   `json:"spec,omitempty"`
	Status OJSWorkerStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// OJSWorkerList contains a list of OJSWorker.
type OJSWorkerList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []OJSWorker `json:"items"`
}

func init() {
	SchemeBuilder.Register(&OJSWorker{}, &OJSWorkerList{})
}

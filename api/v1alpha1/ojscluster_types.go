package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// OJSClusterSpec defines the desired state of an OJS cluster.
type OJSClusterSpec struct {
	// Backend configuration.
	Backend BackendSpec `json:"backend"`
	// Server replicas (default 2).
	Replicas *int32 `json:"replicas,omitempty"`
	// Image to use for the OJS server.
	Image string `json:"image,omitempty"`
	// Worker auto-scaling configuration.
	AutoScaling *AutoScalingSpec `json:"autoScaling,omitempty"`
	// Resource requirements for the OJS server pods.
	Resources corev1.ResourceRequirements `json:"resources,omitempty"`
	// Monitoring configuration.
	Monitoring *MonitoringSpec `json:"monitoring,omitempty"`
}

// BackendSpec defines the backend storage configuration.
type BackendSpec struct {
	// Type of backend: "redis", "postgres", or "nats".
	Type string `json:"type"`
	// Connection URL for the backend.
	URL string `json:"url,omitempty"`
	// Reference to a Secret key containing the connection URL.
	URLSecretRef *SecretKeyRef `json:"urlSecretRef,omitempty"`
	// Embedded enables auto-deployment of the backend (e.g., Redis).
	Embedded bool `json:"embedded,omitempty"`
}

// SecretKeyRef references a key within a Secret.
type SecretKeyRef struct {
	// Name of the Secret.
	Name string `json:"name"`
	// Key within the Secret.
	Key string `json:"key"`
}

// AutoScalingSpec defines worker auto-scaling configuration.
type AutoScalingSpec struct {
	// Enabled enables auto-scaling.
	Enabled bool `json:"enabled"`
	// MinReplicas is the lower bound for auto-scaling.
	MinReplicas int32 `json:"minReplicas"`
	// MaxReplicas is the upper bound for auto-scaling.
	MaxReplicas int32 `json:"maxReplicas"`
	// TargetQueueDepth is the desired queue depth per replica.
	TargetQueueDepth int64 `json:"targetQueueDepth"`
	// TargetJobsPerWorker is the desired active jobs per worker.
	TargetJobsPerWorker int64 `json:"targetJobsPerWorker,omitempty"`
	// ScaleUpCooldown is the cooldown period after a scale-up (e.g. "60s").
	ScaleUpCooldown string `json:"scaleUpCooldown,omitempty"`
	// ScaleDownCooldown is the cooldown period after a scale-down (e.g. "300s").
	ScaleDownCooldown string `json:"scaleDownCooldown,omitempty"`
}

// MonitoringSpec defines monitoring configuration.
type MonitoringSpec struct {
	// Enabled enables Prometheus metrics scraping.
	Enabled bool `json:"enabled"`
	// ServiceMonitor creates a Prometheus ServiceMonitor resource.
	ServiceMonitor bool `json:"serviceMonitor,omitempty"`
	// GrafanaDashboard creates a Grafana dashboard ConfigMap.
	GrafanaDashboard bool `json:"grafanaDashboard,omitempty"`
}

// OJSClusterStatus defines the observed state of an OJS cluster.
type OJSClusterStatus struct {
	// Phase of the cluster: Pending, Running, Scaling, Error.
	Phase string `json:"phase"`
	// Replicas is the total number of server pods.
	Replicas int32 `json:"replicas"`
	// ReadyReplicas is the number of ready server pods.
	ReadyReplicas int32 `json:"readyReplicas"`
	// QueueDepth is the current number of queued jobs.
	QueueDepth int64 `json:"queueDepth"`
	// ActiveJobs is the current number of active jobs.
	ActiveJobs int64 `json:"activeJobs"`
	// Conditions represent the latest available observations.
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Backend",type=string,JSONPath=`.spec.backend.type`
// +kubebuilder:printcolumn:name="Replicas",type=integer,JSONPath=`.status.replicas`
// +kubebuilder:printcolumn:name="Ready",type=integer,JSONPath=`.status.readyReplicas`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// OJSCluster is the Schema for the ojsclusters API.
type OJSCluster struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   OJSClusterSpec   `json:"spec,omitempty"`
	Status OJSClusterStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// OJSClusterList contains a list of OJSCluster.
type OJSClusterList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []OJSCluster `json:"items"`
}

func init() {
	SchemeBuilder.Register(&OJSCluster{}, &OJSClusterList{})
}

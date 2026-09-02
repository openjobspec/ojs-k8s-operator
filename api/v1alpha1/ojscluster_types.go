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
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:default=2
	Replicas *int32 `json:"replicas,omitempty"`
	// Image to use for the OJS server.
	// +kubebuilder:default="ghcr.io/openjobspec/ojs-server:latest"
	Image string `json:"image,omitempty"`
	// Worker auto-scaling configuration.
	AutoScaling *AutoScalingSpec `json:"autoScaling,omitempty"`
	// Resource requirements for the OJS server pods.
	Resources corev1.ResourceRequirements `json:"resources,omitempty"`
	// Monitoring configuration.
	Monitoring *MonitoringSpec `json:"monitoring,omitempty"`
	// SecurityContext configures pod-level security settings.
	SecurityContext *PodSecuritySpec `json:"securityContext,omitempty"`
	// PodDisruptionBudget configures disruption budgets for HA.
	PodDisruptionBudget *PDBSpec `json:"podDisruptionBudget,omitempty"`
	// TopologySpreadConstraints configures pod topology spread for HA.
	TopologySpreadConstraints []corev1.TopologySpreadConstraint `json:"topologySpreadConstraints,omitempty"`
	// ServiceAccountName overrides the service account for the server pods.
	ServiceAccountName string `json:"serviceAccountName,omitempty"`
}

// PodSecuritySpec defines security settings applied to the OJS pods.
type PodSecuritySpec struct {
	// RunAsNonRoot enforces non-root execution (default true).
	// +kubebuilder:default=true
	RunAsNonRoot *bool `json:"runAsNonRoot,omitempty"`
	// RunAsUser sets the UID for the container process.
	// +kubebuilder:default=65534
	RunAsUser *int64 `json:"runAsUser,omitempty"`
	// RunAsGroup sets the GID for the container process.
	// +kubebuilder:default=65534
	RunAsGroup *int64 `json:"runAsGroup,omitempty"`
	// FSGroup sets the filesystem group for volumes.
	FSGroup *int64 `json:"fsGroup,omitempty"`
	// ReadOnlyRootFilesystem mounts the root filesystem as read-only (default true).
	// +kubebuilder:default=true
	ReadOnlyRootFilesystem *bool `json:"readOnlyRootFilesystem,omitempty"`
}

// PDBSpec defines PodDisruptionBudget settings.
type PDBSpec struct {
	// Enabled creates a PodDisruptionBudget for the server deployment (default true for replicas > 1).
	Enabled *bool `json:"enabled,omitempty"`
	// MinAvailable is the minimum number of pods that must remain available.
	// +kubebuilder:validation:Minimum=0
	MinAvailable *int32 `json:"minAvailable,omitempty"`
	// MaxUnavailable is the maximum number of pods that can be unavailable.
	// +kubebuilder:validation:Minimum=0
	MaxUnavailable *int32 `json:"maxUnavailable,omitempty"`
}

// BackendSpec defines the backend storage configuration.
type BackendSpec struct {
	// Type of backend: "redis", "postgres", "nats", "kafka", "sqs", or "lite".
	// +kubebuilder:validation:Enum=redis;postgres;nats;kafka;sqs;lite
	Type string `json:"type"`
	// Connection URL for the backend.
	URL string `json:"url,omitempty"`
	// Reference to a Secret key containing the connection URL.
	URLSecretRef *SecretKeyRef `json:"urlSecretRef,omitempty"`
	// Embedded enables auto-deployment of the backend (e.g., Redis).
	// +kubebuilder:default=false
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
	// +kubebuilder:validation:Minimum=1
	MinReplicas int32 `json:"minReplicas"`
	// MaxReplicas is the upper bound for auto-scaling.
	// +kubebuilder:validation:Minimum=1
	MaxReplicas int32 `json:"maxReplicas"`
	// TargetQueueDepth is the desired queue depth per replica.
	// +kubebuilder:validation:Minimum=1
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
	// +kubebuilder:validation:Enum=Pending;Running;Scaling;Error
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

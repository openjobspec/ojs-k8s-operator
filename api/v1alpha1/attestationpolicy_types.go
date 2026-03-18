package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// HardwareRequirement specifies an allowed TEE hardware type and minimum version.
type HardwareRequirement struct {
	// Type of hardware attestation environment.
	// +kubebuilder:validation:Enum=aws-nitro;intel-tdx;amd-sev-snp
	Type string `json:"type"`
	// MinVersion is the minimum firmware/microcode version required.
	MinVersion string `json:"minVersion,omitempty"`
}

// ModelPolicy constrains which ML models are permitted in attested jobs.
type ModelPolicy struct {
	// AllowedModels is a list of model identifiers (registry URLs or names).
	AllowedModels []string `json:"allowedModels,omitempty"`
	// RequireFingerprint mandates that every attested result includes a model SHA-256 fingerprint.
	RequireFingerprint bool `json:"requireFingerprint,omitempty"`
}

// OJSAttestationPolicySpec defines the desired attestation policy.
type OJSAttestationPolicySpec struct {
	// Mode controls how attestation is enforced.
	// +kubebuilder:validation:Enum=required;preferred;forbidden
	Mode string `json:"mode"`
	// JobTypes lists job type patterns (glob) this policy applies to (e.g. "payments.*").
	// +optional
	JobTypes []string `json:"jobTypes,omitempty"`
	// Hardware lists allowed TEE hardware types and minimum versions.
	// +optional
	Hardware []HardwareRequirement `json:"hardware,omitempty"`
	// Regions lists allowed geographic regions for attestation.
	// +optional
	Regions []string `json:"regions,omitempty"`
	// MinKeyClass is the minimum acceptable signing key class.
	// +kubebuilder:validation:Enum=ed25519;ml-dsa-65;hybrid
	// +optional
	MinKeyClass string `json:"minKeyClass,omitempty"`
	// ModelPolicy defines constraints for ML model attestation.
	// +optional
	ModelPolicy *ModelPolicy `json:"modelPolicy,omitempty"`
}

// OJSAttestationPolicyStatus defines the observed state of an OJSAttestationPolicy.
type OJSAttestationPolicyStatus struct {
	// Active indicates whether the policy is currently enforced.
	Active bool `json:"active"`
	// Conditions represent the latest available observations.
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:resource:shortName=ojsap
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Mode",type=string,JSONPath=`.spec.mode`
// +kubebuilder:printcolumn:name="Active",type=boolean,JSONPath=`.status.active`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// OJSAttestationPolicy defines a cluster-wide or per-namespace attestation
// policy for OJS jobs (M1 Verifiable Compute).
type OJSAttestationPolicy struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   OJSAttestationPolicySpec   `json:"spec,omitempty"`
	Status OJSAttestationPolicyStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// OJSAttestationPolicyList contains a list of OJSAttestationPolicy.
type OJSAttestationPolicyList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []OJSAttestationPolicy `json:"items"`
}

func init() {
	SchemeBuilder.Register(&OJSAttestationPolicy{}, &OJSAttestationPolicyList{})
}

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ClustRegCredSpec defines the desired state of ClustRegCred
type ClustRegCredSpec struct {
	// Registry is the container registry URL (e.g., docker.io, gcr.io)
	// +kubebuilder:validation:MinLength=1
	Registry string `json:"registry"`

	// Username for authenticating to the container registry
	// +kubebuilder:validation:MinLength=1
	Username string `json:"username"`

	// Password for authenticating to the container registry
	// +kubebuilder:validation:MinLength=1
	Password string `json:"password"`

	// Email associated with the container registry account
	// +kubebuilder:default=""
	Email string `json:"email,omitempty"`

	// SecretName is the name to be used for the image pull secret in each namespace
	// +kubebuilder:validation:MinLength=1
	SecretName string `json:"secretName"`
}

// ClustRegCredStatus defines the observed state of ClustRegCred
type ClustRegCredStatus struct {
	// SyncedNamespaces is the list of namespaces where the secret has been synced
	SyncedNamespaces []string `json:"syncedNamespaces,omitempty"`

	// LastSyncTime is the timestamp of the last successful sync
	LastSyncTime *metav1.Time `json:"lastSyncTime,omitempty"`

	// Conditions represent the latest available observations of ClustRegCred's state
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster,shortName=crc
// +kubebuilder:printcolumn:name="Registry",type=string,JSONPath=`.spec.registry`
// +kubebuilder:printcolumn:name="SecretName",type=string,JSONPath=`.spec.secretName`
// +kubebuilder:printcolumn:name="Synced",type=integer,JSONPath=`.status.syncedNamespaces`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// ClustRegCred is the Schema for the clustregcreds API
type ClustRegCred struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ClustRegCredSpec   `json:"spec,omitempty"`
	Status ClustRegCredStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// ClustRegCredList contains a list of ClustRegCred
type ClustRegCredList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ClustRegCred `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ClustRegCred{}, &ClustRegCredList{})
}

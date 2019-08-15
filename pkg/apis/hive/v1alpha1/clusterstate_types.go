package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	configv1 "github.com/openshift/api/config/v1"
)

// ClusterStateSpec defines the desired state of ClusterState
type ClusterStateSpec struct {
	ClusterDeployment corev1.LocalObjectReference `json:"clusterDeployment"`
}

// ClusterStateStatus defines the observed state of ClusterState
type ClusterStateStatus struct {
	ClusterOperators []ClusterOperatorState `json:"clusterOperators,omitempty"`
}

// ClusterOperatorState summarizes the status of a single cluster operator
type ClusterOperatorState struct {
	Name       string                                    `json:"name"`
	Conditions []configv1.ClusterOperatorStatusCondition `json:"conditions,omitempty"`
}

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// ClusterState is the Schema for the clusterstates API
// +k8s:openapi-gen=true
// +kubebuilder:subresource:status
type ClusterState struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ClusterStateSpec   `json:"spec,omitempty"`
	Status ClusterStateStatus `json:"status,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// ClusterStateList contains a list of ClusterState
type ClusterStateList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ClusterState `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ClusterState{}, &ClusterStateList{})
}

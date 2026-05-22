package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type EgressGatewaySpec struct {
	LeaseName         string           `json:"leaseName"`
	LeaseNamespace    string           `json:"leaseNamespace"`
	NodeLabelKey      string           `json:"nodeLabelKey"`
	NodeLabelValue    string           `json:"nodeLabelValue,omitempty"`
	Candidates        []string         `json:"candidates,omitempty"`
	FallbackCandidate string           `json:"fallbackCandidate,omitempty"`
	DebounceDuration  *metav1.Duration `json:"debounceDuration,omitempty"`
	RequeueInterval   *metav1.Duration `json:"requeueInterval,omitempty"`
}

type EgressGatewayStatus struct {
	CurrentGatewayNode string       `json:"currentGatewayNode,omitempty"`
	DesiredGatewayNode string       `json:"desiredGatewayNode,omitempty"`
	DesiredSince       *metav1.Time `json:"desiredSince,omitempty"`
	LastSwitchTime     *metav1.Time `json:"lastSwitchTime,omitempty"`
	ObservedGeneration int64        `json:"observedGeneration,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster,shortName=egw

type EgressGateway struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   EgressGatewaySpec   `json:"spec,omitempty"`
	Status EgressGatewayStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

type EgressGatewayList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`

	Items []EgressGateway `json:"items"`
}

func init() {
	SchemeBuilder.Register(&EgressGateway{}, &EgressGatewayList{})
}

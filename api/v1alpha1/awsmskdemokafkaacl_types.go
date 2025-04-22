/*
Copyright 2025 NTT DATA.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// AwsMSKDemoKafkaACLSpec defines the desired state of AwsMSKDemoKafkaACL
type AwsMSKDemoKafkaACLSpec struct {
	TopicName string `json:"topicName"`
	Principal string `json:"principal"`
	Operation string `json:"operation"`
}

// AwsMSKDemoKafkaACLStatus defines the observed state of AwsMSKDemoKafkaACL
type AwsMSKDemoKafkaACLStatus struct {
	Status string `json:"status"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// AwsMSKDemoKafkaACL is the Schema for the awsmskdemokafkaacls API
type AwsMSKDemoKafkaACL struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   AwsMSKDemoKafkaACLSpec   `json:"spec,omitempty"`
	Status AwsMSKDemoKafkaACLStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// AwsMSKDemoKafkaACLList contains a list of AwsMSKDemoKafkaACL
type AwsMSKDemoKafkaACLList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []AwsMSKDemoKafkaACL `json:"items"`
}

func init() {
	SchemeBuilder.Register(&AwsMSKDemoKafkaACL{}, &AwsMSKDemoKafkaACLList{})
}

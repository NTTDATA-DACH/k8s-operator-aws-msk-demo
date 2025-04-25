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

// Define constants for RDS instance state values
const (
	StateCreating = "creating"
	StateUpdating = "updating"
	StateDeleting = "deleting"
	StateCreated  = "created"
	StateUpdated  = "updated"
	StateDeleted  = "deleted"
)

// AwsMSKDemoKafkaTopicSpec defines the desired state of AwsMSKDemoKafkaTopic
type AwsMSKDemoKafkaTopicSpec struct {
	ClusterArn        string               `json:"clusterArn"`
	Name              string               `json:"name"`
	Partitions        int32                `json:"partitions"`
	ReplicationFactor int16                `json:"replicationFactor"`
	ACLs              []AwsMSKDemoKafkaACL `json:"acls"`
}

type AwsMSKDemoKafkaACL struct {
	Principal      string `json:"principal"`
	PermissionType string `json:"permissionType"`
	Operation      string `json:"operation"`
}

// AwsMSKDemoKafkaTopicStatus defines the observed state of AwsMSKDemoKafkaTopic
type AwsMSKDemoKafkaTopicStatus struct {
	Status string `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// AwsMSKDemoKafkaTopic is the Schema for the awsmskdemokafkatopics API
type AwsMSKDemoKafkaTopic struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   AwsMSKDemoKafkaTopicSpec   `json:"spec,omitempty"`
	Status AwsMSKDemoKafkaTopicStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// AwsMSKDemoKafkaTopicList contains a list of AwsMSKDemoKafkaTopic
type AwsMSKDemoKafkaTopicList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []AwsMSKDemoKafkaTopic `json:"items"`
}

func init() {
	SchemeBuilder.Register(&AwsMSKDemoKafkaTopic{}, &AwsMSKDemoKafkaTopicList{})
}

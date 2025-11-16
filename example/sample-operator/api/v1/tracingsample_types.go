/*
Copyright 2025.

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

package v1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// TracingSampleSpec defines the desired state of TracingSample.
type TracingSampleSpec struct {
	// Value is the incremental counter used to trigger Sample reconciles.
	Value int `json:"value,omitempty"`
}

// TracingSampleStatus defines the observed state of TracingSample.
type TracingSampleStatus struct {
	Ready bool `json:"ready,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// TracingSample is the Schema for the tracingsamples API.
type TracingSample struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   TracingSampleSpec   `json:"spec,omitempty"`
	Status TracingSampleStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// TracingSampleList contains a list of TracingSample.
type TracingSampleList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []TracingSample `json:"items"`
}

func init() {
	SchemeBuilder.Register(&TracingSample{}, &TracingSampleList{})
}

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

// TracingPingerSpec defines the desired state of TracingPinger.
type TracingPingerSpec struct {
	// Value is incremented by the controller until it reaches the configured limit.
	Value int `json:"value,omitempty"`
}

// TracingPingerStatus defines the observed state of TracingPinger.
type TracingPingerStatus struct {
	Ready bool `json:"ready,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// TracingPinger is the Schema for the tracingpingers API.
type TracingPinger struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   TracingPingerSpec   `json:"spec,omitempty"`
	Status TracingPingerStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// TracingPingerList contains a list of TracingPinger.
type TracingPingerList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []TracingPinger `json:"items"`
}

func init() {
	SchemeBuilder.Register(&TracingPinger{}, &TracingPingerList{})
}

/*
Copyright 2026.

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

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// HostSpec defines the desired state of Host.
type HostSpec struct {
	// Matches is a struct of constraints for when selecting a host from the inventory
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="field is immutable"
	Matches MatchExpressions `json:"matches"`
	// NetworkConfig specifies host networking (e.g. which network/VLAN to attach).
	NetworkConfig *NetworkConfig `json:"networkConfig,omitempty"`
	// Online is the desired power state (true = on, false = off).
	Online bool `json:"online"`
	// Image to provision; when set, triggers provisioning. When cleared, triggers deprovisioning.
	Image *Image `json:"image,omitempty"`
}

type MatchExpressions struct {
	// ManagedBy identifies the controller or system that manages this host.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="field is immutable"
	ManagedBy string `json:"managedBy"`
	// HostClass is the class/capability tier of the host.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="field is immutable"
	HostClass string `json:"hostClass"`
	// Query is a map of additional values to filter through
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="field is immutable"
	Query map[string]string `json:"query,omitempty"`
}

// NetworkConfig holds host network configuration.
type NetworkConfig struct {
	// Network identifies the network (e.g. private-vlan-network) the host should use.
	Network string `json:"network"`
}

// Image holds the image to provision.
type Image struct {
	// URL of the live ISO.
	URL string `json:"url"`
	// Format must be live-iso (default).
	// +kubebuilder:default:=live-iso
	Format string `json:"format,omitempty"`
	// Checksum (md5 hex) of the .iso file.
	Checksum string `json:"checksum,omitempty"`
}

// ProvisioningState defines the states the provisioner will report
// the host has having.
type ProvisioningState string

const (
	StateUnmanaged      ProvisioningState = "unmanaged"
	StateAvailable      ProvisioningState = "available"
	StateProvisioning   ProvisioningState = "provisioning"
	StateProvisioned    ProvisioningState = "provisioned"
	StateDeprovisioning ProvisioningState = "deprovisioning"
)

// ProvisionStatus holds current provisioning state from the backend.
type ProvisionStatus struct {
	// ID is the host ID in the Bare Metal Management system.
	ID string `json:"id,omitempty"`
	// Image is the currently provisioned image (if any).
	Image Image `json:"image,omitempty"`
	// State is the current provisioning state.
	State ProvisioningState `json:"state,omitempty"`
}

// HostStatus defines the observed state of Host.
type HostStatus struct {
	// NodeID is the Ironic node UUID (or name) this Host represents. Required for power management.
	NodeID string `json:"nodeId"`
	// PoweredOn is the current power state.
	PoweredOn *bool `json:"poweredOn,omitempty"`
	// Provisioning holds ID, current image, and state from the backend.
	Provisioning ProvisionStatus `json:"provisioning,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// Host is the Schema for the hosts API.
type Host struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   HostSpec   `json:"spec,omitempty"`
	Status HostStatus `json:"status,omitempty"`
}

func (h *Host) GetPoolID() (string, bool) {
	for _, ownerReference := range h.OwnerReferences {
		if !*ownerReference.Controller {
			continue
		}
		if ownerReference.APIVersion == h.APIVersion && ownerReference.Kind == "BareMetalPool" {
			return string(ownerReference.UID), true
		}
	}
	return "", false
}

// +kubebuilder:object:root=true

// HostList contains a list of Host.
type HostList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Host `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Host{}, &HostList{})
}

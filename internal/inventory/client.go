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

package inventory

import (
	"context"
)

// Host is the common return type all clients must use
type Host struct {
	BareMetalPoolID     string
	BareMetalPoolHostID string
	InventoryHostID     string
	Name                string
	HostClass           string
	ManagementClass     string
	NetworkClass        string
	ProvisionState      string
	ManagedBy           string
}

// Client interface for inventory implementations
type Client interface {
	// Returns a host with matching fields that is not already assigned
	FindFreeHost(ctx context.Context, matchExpressions map[string]string) (*Host, error)

	// Updates the host by marking it as assigned, returns true if the update request was performed
	AssignHost(ctx context.Context, inventoryHostID string, bareMetalPoolID string, bareMetalPoolHostID string, labels map[string]string) (*Host, error)

	// Updates the host by undoing the assign operation
	UnassignHost(ctx context.Context, inventoryHostID string, labels []string) error
}

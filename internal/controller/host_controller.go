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

package controller

import (
	"context"
	"errors"

	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/ajamias/bare-metal-host-inventory-operator/api/v1alpha1"
	"github.com/ajamias/bare-metal-host-inventory-operator/internal/inventory"
)

// HostReconciler reconciles a Host object
type HostReconciler struct {
	client.Client
	Scheme          *runtime.Scheme
	InventoryClient *inventory.InventoryClient
}

const HostInventoryFinalizer = "osac.openshift.io/inventory"

// +kubebuilder:rbac:groups=osac.openshift.io,resources=hosts,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=osac.openshift.io,resources=hosts/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=osac.openshift.io,resources=hosts/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the pool closer to the desired state.
func (r *HostReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	host := &v1alpha1.Host{}
	err := r.Get(ctx, req.NamespacedName, host)
	if err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if !host.DeletionTimestamp.IsZero() {
		return r.handleDeletion(ctx, host)
	}

	return r.handleUpdate(ctx, host)
}

// handleUpdate assigns an inventory node to the Host CR and marks it as acquired.
func (r *HostReconciler) handleUpdate(ctx context.Context, host *v1alpha1.Host) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	if controllerutil.AddFinalizer(host, HostInventoryFinalizer) {
		if err := r.Update(ctx, host); err != nil {
			log.Error(err, "Failed to add finalizer", "host", host.Name)
			return ctrl.Result{}, err
		}
	}

	poolID, ok := host.GetPoolID()
	if !ok {
		log.Info("Host is orphaned so delete it")
		if err := r.Delete(ctx, host); err != nil {
			log.Error(err, "Failed to delete Host", "host", host.Name)
			return ctrl.Result{}, err
		}
	}

	// If NodeID is already set, the host has been assigned
	if host.Status.NodeID != "" {
		return ctrl.Result{}, nil
	}

	hostClass := host.Spec.Matches.HostClass
	managedBy := host.Spec.Matches.ManagedBy

	// First check if there's already a host acquired for this pool.
	// This makes the reconciliation idempotent - if we previously marked a host as acquired
	// but failed to update the CR, we'll find it again here.
	acquiredHosts, err := r.InventoryClient.GetHosts(
		ctx,
		poolID,
		inventory.WithHostClass(hostClass),
		inventory.WithMatchType(managedBy),
		inventory.WithCount(1),
	)
	if err != nil {
		log.Error(err, "Failed to query inventory for acquired hosts", "host", host.Name)
		return ctrl.Result{}, err
	}

	var inventoryHost inventory.Host
	if len(acquiredHosts) > 0 {
		inventoryHost = acquiredHosts[0]
	} else {
		availableHosts, err := r.InventoryClient.GetHosts(
			ctx,
			"",
			inventory.WithHostClass(hostClass),
			inventory.WithMatchType(managedBy),
			inventory.WithCount(1),
		)
		if err != nil {
			log.Error(err, "Failed to query inventory for available hosts", "host", host.Name)
			return ctrl.Result{}, err
		}
		if len(availableHosts) == 0 {
			log.Info("No available hosts in inventory", "host", host.Name, "hostClass", hostClass)
			return ctrl.Result{}, errors.New("no available hosts in inventory")
		}

		inventoryHost = availableHosts[0]

		err = r.InventoryClient.PatchInventoryHostPoolID(ctx, inventoryHost.NodeID, poolID)
		if err != nil {
			log.Error(err, "Failed to mark host as acquired in inventory", "host", host.Name, "nodeID", inventoryHost.NodeID)
			return ctrl.Result{}, err
		}
	}

	host.Status.NodeID = inventoryHost.NodeID
	if err := r.Update(ctx, host); err != nil {
		log.Error(err, "Failed to update Host CR with NodeID", "host", host.Name, "nodeID", inventoryHost.NodeID)
		return ctrl.Result{}, err
	}

	log.Info("Successfully assigned and acquired host", "host", host.Name, "nodeID", inventoryHost.NodeID)
	return ctrl.Result{}, nil
}

// handleDeletion frees the host in the inventory and removes the finalizer.
func (r *HostReconciler) handleDeletion(ctx context.Context, host *v1alpha1.Host) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	if !controllerutil.ContainsFinalizer(host, HostInventoryFinalizer) {
		return ctrl.Result{}, nil
	}

	// Only free in inventory if a NodeID was assigned
	if host.Status.NodeID != "" {
		err := r.InventoryClient.PatchInventoryHostPoolID(ctx, host.Status.NodeID, "")
		if err != nil {
			log.Error(err, "Failed to free host in inventory", "host", host.Name)
			return ctrl.Result{}, err
		}
	}

	controllerutil.RemoveFinalizer(host, HostInventoryFinalizer)
	if err := r.Update(ctx, host); err != nil {
		log.Error(err, "Failed to remove finalizer", "host", host.Name)
		return ctrl.Result{}, err
	}

	log.Info("Successfully freed host in inventory", "host", host.Name)
	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *HostReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.Host{}).
		Named("host").
		Complete(r)
}

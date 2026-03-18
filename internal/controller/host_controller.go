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
	"time"

	"github.com/DanNiESh/host-operator/api/v1alpha1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/ajamias/bare-metal-host-inventory-operator/internal/inventory"
	"github.com/ajamias/bare-metal-host-inventory-operator/internal/lock"
)

// HostReconciler reconciles a Host object
type HostReconciler struct {
	client.Client
	Scheme          *runtime.Scheme
	InventoryClient *inventory.InventoryClient
	HostLocker      lock.Locker
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
	if host.Status.ID != "" {
		log.Info("Host is already acquired, nothing to do")
		return ctrl.Result{}, nil
	}

	hostClass := host.Spec.Matches.HostClass
	managedBy := host.Spec.Matches.ManagedBy

	// Get available inventory host
	availableHosts, err := r.InventoryClient.GetHosts(
		ctx,
		"",
		inventory.WithHostClass(hostClass),
		inventory.WithManagedBy(managedBy),
		inventory.WithCount(1),
	)
	if err != nil {
		log.Error(err, "Failed to query inventory for available hosts", "host", host.Name)
		return ctrl.Result{}, err
	}
	if len(availableHosts) < 1 {
		log.Info("No available hosts in inventory", "host", host.Name, "hostClass", hostClass, "managedBy", managedBy)
		return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
	}

	inventoryHost := availableHosts[0]

	// Lock inventory host
	log.Info("Attempting to acquire lock for inventory host", "host", host.Name, "nodeID", inventoryHost.NodeID)
	acquiredLock, err := r.HostLocker.TryLock(ctx, inventoryHost.NodeID)
	if err != nil {
		log.Error(err, "Failed to acquire lock for inventory host", "host", host.Name, "nodeID", inventoryHost.NodeID)
		return ctrl.Result{}, err
	}
	if !acquiredLock {
		log.Info("Inventory host already locked, retrying...",
			"host", host.Name,
			"nodeID", inventoryHost.NodeID)
		return ctrl.Result{RequeueAfter: 1 * time.Second}, nil
	}

	// Ensure we release the lock if we fail to claim the node
	defer func() {
		if err != nil {
			if unlockErr := r.HostLocker.Unlock(ctx, inventoryHost.NodeID); unlockErr != nil {
				log.Error(unlockErr, "Failed to release lock after error", "nodeID", inventoryHost.NodeID)
			}
		}
	}()

	err = r.InventoryClient.PatchInventoryHostPoolID(ctx, inventoryHost.NodeID, poolID)
	if err != nil {
		log.Error(err, "Failed to mark host as acquired in inventory", "host", host.Name, "nodeID", inventoryHost.NodeID)
		return ctrl.Result{}, err
	}

	host.Status.ID = inventoryHost.NodeID
	host.Status.HostManagementClass = "openstack" // TODO: HARDCODED
	if err := r.Status().Update(ctx, host); err != nil {
		log.Error(err, "Failed to update Host CR status with NodeID", "host", host.Name, "nodeID", inventoryHost.NodeID)
		return ctrl.Result{}, err
	}

	// Send update event for the next Host management operator
	if err := r.Update(ctx, host); err != nil {
		log.Error(err, "Failed to update Host CR with NodeID", "host", host.Name, "nodeID", inventoryHost.NodeID)
		return ctrl.Result{}, err
	}

	// Successfully claimed the node, now release the lock
	// The lock was only needed during the claiming process
	if err := r.HostLocker.Unlock(ctx, inventoryHost.NodeID); err != nil {
		log.Error(err, "Failed to release lock after successful claim", "nodeID", inventoryHost.NodeID)
		// Don't return error here, the node was successfully claimed
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
	if host.Status.ID != "" {
		err := r.InventoryClient.PatchInventoryHostPoolID(ctx, host.Status.ID, "")
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

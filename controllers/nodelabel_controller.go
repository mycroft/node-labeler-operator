/*
Copyright 2023.

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

package controllers

import (
	"context"
	"time"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	corev1helpers "k8s.io/component-helpers/scheduling/corev1"

	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/api/core/v1"

	infrav1alpha1 "github.com/mycroft/node-labeler-operator/api/v1alpha1"
)

// NodeLabelReconciler reconciles a NodeLabel object
type NodeLabelReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

const nodeLabelFinalizer = "infra.mkz.me/finalizer"

//+kubebuilder:rbac:groups=infra.mkz.me,resources=nodelabels,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=infra.mkz.me,resources=nodelabels/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=infra.mkz.me,resources=nodelabels/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *NodeLabelReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	logger.Info("NodeLabel Reconcile started")

	// retrieve NodeLabel
	nodeLabelObj := &infrav1alpha1.NodeLabel{}
	err := r.Get(ctx, req.NamespacedName, nodeLabelObj)
	if err != nil {
		if errors.IsNotFound(err) {
			logger.Info("Could not find NodeLabel object", "error", err)
			return reconcile.Result{}, nil
		}
		logger.Error(err, "Could not retrieve NodeLabel")
	}

	// a finalizer is required to not delete the object before removing set labels.
	if !controllerutil.ContainsFinalizer(nodeLabelObj, nodeLabelFinalizer) {
		logger.Info("Adding Finalizer for NodeLabel")
		if ok := controllerutil.AddFinalizer(nodeLabelObj, nodeLabelFinalizer); !ok {
			logger.Error(err, "Failed to add finalizer into the custom resource")
			return ctrl.Result{Requeue: true}, nil
		}

		if err = r.Update(ctx, nodeLabelObj); err != nil {
			logger.Error(err, "Failed to update custom resource to add finalizer")
			return ctrl.Result{}, err
		}
	}

	isDeleted := nodeLabelObj.GetDeletionTimestamp() != nil

	// retrieve nodes
	nodeList := &corev1.NodeList{}
	err = r.List(
		context.TODO(),
		nodeList,
	)
	if errors.IsNotFound(err) {
		logger.Error(nil, "Could not find Nodes")
		return reconcile.Result{}, nil
	}

	for _, node := range nodeList.Items {
		matching, err := corev1helpers.MatchNodeSelectorTerms(&node, &nodeLabelObj.Spec.NodeSelector)
		if err != nil {
			logger.Error(err, "Could not match NodeSelectorTerms")
			return reconcile.Result{}, nil
		}

		if !matching {
			continue
		}

		key := client.ObjectKeyFromObject(&node)
		err = r.Get(context.TODO(), key, &node)
		if err != nil {
			logger.Error(nil, "Could not find Node", "name", node.Name)
		}

		// Fix Annotations
		for k, v := range nodeLabelObj.Spec.Annotations {
			if !isDeleted {
				node.Annotations[k] = v
			} else {
				delete(node.Annotations, k)
			}
		}

		// Fix Labels
		for k, v := range nodeLabelObj.Spec.Labels {
			if !isDeleted {
				node.Labels[k] = v
			} else {
				delete(node.Labels, k)
			}
		}

		// Fix Taints
		for _, taint := range nodeLabelObj.Spec.Taints {
			if !isDeleted {
				patched := false
				for idx, existingTaint := range node.Spec.Taints {
					if existingTaint.Key == taint.Key {
						node.Spec.Taints[idx] = taint
						patched = true
						break
					}
				}

				if !patched {
					node.Spec.Taints = append(node.Spec.Taints, taint)
				}
			} else {
				taints := []v1.Taint{}

				for _, existingTaint := range node.Spec.Taints {
					if existingTaint.Key != taint.Key {
						taints = append(taints, existingTaint)
					}
				}

				node.Spec.Taints = taints
			}
		}

		// Update node
		err = r.Update(ctx, &node)
		if err != nil {
			logger.Error(err, "Could not update Node", "node", node.Name)
		}
	}

	time.Sleep(time.Second)

	if isDeleted {
		// It is assumed the tags were removed. Removing the finalizer.
		if ok := controllerutil.RemoveFinalizer(nodeLabelObj, nodeLabelFinalizer); !ok {
			logger.Error(err, "Failed to remove finalizer for NodeLabel")
			return ctrl.Result{Requeue: true}, nil
		}

		if err := r.Update(ctx, nodeLabelObj); err != nil {
			logger.Error(err, "Failed to remove finalizer for NodeLabel")
			return ctrl.Result{}, err
		}
	}

	return ctrl.Result{RequeueAfter: 2 * time.Minute}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *NodeLabelReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&infrav1alpha1.NodeLabel{}).
		Complete(r)
}

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

package controller

import (
	"context"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	appv1 "github.com/Azure/operatortrace/example/example-operator/api/v1"
	operatortrace "github.com/Azure/operatortrace/operatortrace-go/pkg/client"
	tracinghandler "github.com/Azure/operatortrace/operatortrace-go/pkg/handler"
	tracingpredicates "github.com/Azure/operatortrace/operatortrace-go/pkg/predicates"
	tracingreconcile "github.com/Azure/operatortrace/operatortrace-go/pkg/reconcile"
	tracingtypes "github.com/Azure/operatortrace/operatortrace-go/pkg/types"
)

// SampleReconciler reconciles a Sample object.
type SampleReconciler struct {
	Client operatortrace.TracingClient
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=app.azure.microsoft.com,resources=samples,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=app.azure.microsoft.com,resources=samples/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=app.azure.microsoft.com,resources=samples/finalizers,verbs=update
// +kubebuilder:rbac:groups=app.azure.microsoft.com,resources=tracingsamples,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=app.azure.microsoft.com,resources=tracingsamples/status,verbs=get

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *SampleReconciler) Reconcile(ctx context.Context, obj *appv1.Sample) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithValues("sample", obj.Name)
	logger.V(1).Info("reconciling Sample")

	tracingSample := &appv1.TracingSample{}
	err := r.Client.Get(ctx, types.NamespacedName{Name: obj.Name, Namespace: obj.Namespace}, tracingSample)
	if err != nil {
		if apierrors.IsNotFound(err) {
			logger.V(1).Info("creating tracing sample peer")
			tracingSample = &appv1.TracingSample{
				TypeMeta: metav1.TypeMeta{
					APIVersion: appv1.GroupVersion.String(),
					Kind:       "TracingSample",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      obj.Name,
					Namespace: obj.Namespace,
				},
				Spec: appv1.TracingSampleSpec{
					Value: obj.Spec.Bar,
				},
			}
			if err := r.Client.Create(ctx, tracingSample); err != nil {
				return ctrl.Result{}, err
			}
			return ctrl.Result{Requeue: true}, nil
		}
		return ctrl.Result{}, err
	}

	tracingSample.TypeMeta = metav1.TypeMeta{
		APIVersion: appv1.GroupVersion.String(),
		Kind:       "TracingSample",
	}

	updatedTracingSample := false
	if tracingSample.Spec.Value < pingPongMaxValue && tracingSample.Spec.Value <= obj.Spec.Bar {
		tracingSample.Spec.Value++
		updatedTracingSample = true
		logger.V(1).Info("incrementing tracing sample", "value", tracingSample.Spec.Value)
	} else {
		logger.V(1).Info("tracing sample has reached max or waiting for sample", "sample", obj.Spec.Bar, "tracingSample", tracingSample.Spec.Value)
	}

	if updatedTracingSample {
		if err := r.Client.Update(ctx, tracingSample); err != nil {
			return ctrl.Result{}, err
		}
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *SampleReconciler) SetupWithManager(mgr ctrl.Manager, tracingClient operatortrace.TracingClient) error {
	options := tracingreconcile.TracingOptions()

	return builder.TypedControllerManagedBy[tracingtypes.RequestWithTraceID](mgr).
		Named("sample").
		WithOptions(options).
		Watches(
			&appv1.Sample{},
			&tracinghandler.TypedEnqueueRequestForObject[client.Object]{
				Scheme: r.Scheme,
			},
			builder.WithPredicates(
				tracingpredicates.IgnoreTraceAnnotationUpdatePredicate{},
				predicate.ResourceVersionChangedPredicate{},
			),
		).
		Complete(tracingreconcile.AsTracingReconciler(tracingClient, r))
}

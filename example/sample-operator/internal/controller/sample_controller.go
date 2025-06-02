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

	"k8s.io/apimachinery/pkg/runtime"
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

// SampleReconciler reconciles a Sample object
type SampleReconciler struct {
	Client operatortrace.TracingClient
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=app.azure.microsoft.com,resources=samples,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=app.azure.microsoft.com,resources=samples/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=app.azure.microsoft.com,resources=samples/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the Sample object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.20.4/pkg/reconcile
func (r *SampleReconciler) Reconcile(ctx context.Context, obj *appv1.Sample) (ctrl.Result, error) {
	log := log.FromContext(ctx).WithValues("sample", obj.Name)
	log.V(1).Info("reconciling Sample")

	// create a new sample object if spec.bar is less than 5, increment spec.bar
	if obj.Spec.Bar < 5 {
		obj.Spec.Bar++
		log.V(1).Info("incrementing bar", "bar", obj.Spec.Bar)
	} else {
		log.V(1).Info("bar is greater than 5, not incrementing")
	}
	// update the sample object
	r.Client.Update(ctx, obj)

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *SampleReconciler) SetupWithManager(mgr ctrl.Manager, tracingClient operatortrace.TracingClient) error {

	return builder.TypedControllerManagedBy[tracingtypes.RequestWithTraceID](mgr).
		Named("sample").
		WithOptions(tracingreconcile.TracingOptions()).
		Watches(
			&appv1.Sample{},
			&tracinghandler.TypedEnqueueRequestForObject[client.Object]{},
			builder.WithPredicates(
				tracingpredicates.IgnoreTraceAnnotationUpdatePredicate{},
				predicate.ResourceVersionChangedPredicate{},
			),
		).
		Complete(tracingreconcile.AsTracingReconciler(tracingClient, r))
}

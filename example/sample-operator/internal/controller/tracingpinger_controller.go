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

// TracingPingerReconciler reconciles a TracingPinger object.
type TracingPingerReconciler struct {
	Client operatortrace.TracingClient
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=app.azure.microsoft.com,resources=tracingpingers,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=app.azure.microsoft.com,resources=tracingpingers/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=app.azure.microsoft.com,resources=tracingpingers/finalizers,verbs=update

// Reconcile increments the TracingPinger spec until it reaches the cap.
func (r *TracingPingerReconciler) Reconcile(ctx context.Context, obj *appv1.TracingPinger) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithValues("tracingPinger", obj.Name)
	logger.V(1).Info("reconciling TracingPinger", "value", obj.Spec.Value)

	if obj.Spec.Value >= pingPongMaxValue {
		logger.V(1).Info("tracing pinger reached max value", "max", pingPongMaxValue)
		return ctrl.Result{}, nil
	}

	obj.Spec.Value++
	logger.V(1).Info("incrementing tracing pinger", "value", obj.Spec.Value)

	if err := r.Client.Update(ctx, obj); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

// SetupWithManager wires the controller with tracing-aware reconciliation.
func (r *TracingPingerReconciler) SetupWithManager(mgr ctrl.Manager, tracingClient operatortrace.TracingClient) error {
	options := tracingreconcile.TracingOptions()
	options.MaxConcurrentReconciles = 1

	return builder.TypedControllerManagedBy[tracingtypes.RequestWithTraceID](mgr).
		Named("tracing-pinger").
		WithOptions(options).
		Watches(
			&appv1.TracingPinger{},
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

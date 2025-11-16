package controller

import (
	"context"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
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

// TracingSampleReconciler reconciles a TracingSample object.
type TracingSampleReconciler struct {
	Client operatortrace.TracingClient
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=app.azure.microsoft.com,resources=tracingsamples,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=app.azure.microsoft.com,resources=tracingsamples/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=app.azure.microsoft.com,resources=tracingsamples/finalizers,verbs=update
// +kubebuilder:rbac:groups=app.azure.microsoft.com,resources=samples,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=app.azure.microsoft.com,resources=samples/status,verbs=get

// Reconcile moves TracingSample objects toward the desired state while ping-ponging updates with Sample.
func (r *TracingSampleReconciler) Reconcile(ctx context.Context, obj *appv1.TracingSample) (ctrl.Result, error) {
	log := log.FromContext(ctx).WithValues("tracingSample", obj.Name)
	log.V(1).Info("reconciling TracingSample")

	sample := &appv1.Sample{}
	err := r.Client.Get(ctx, types.NamespacedName{Name: obj.Name, Namespace: obj.Namespace}, sample)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	updatedSample := false
	if sample.Spec.Bar < pingPongMaxValue && sample.Spec.Bar <= obj.Spec.Value {
		sample.Spec.Bar++
		updatedSample = true
		log.V(1).Info("incrementing sample", "bar", sample.Spec.Bar)
	} else {
		log.V(1).Info("sample waiting or maxed", "value", sample.Spec.Bar, "tracingSample", obj.Spec.Value)
	}

	if updatedSample {
		if err := r.Client.Update(ctx, sample); err != nil {
			return ctrl.Result{}, err
		}
	}

	return ctrl.Result{}, nil
}

// SetupWithManager wires the controller into the manager with tracing aware reconcilers.
func (r *TracingSampleReconciler) SetupWithManager(mgr ctrl.Manager, tracingClient operatortrace.TracingClient) error {
	options := tracingreconcile.TracingOptions()
	options.MaxConcurrentReconciles = 1

	return builder.TypedControllerManagedBy[tracingtypes.RequestWithTraceID](mgr).
		Named("tracing-sample").
		WithOptions(options).
		Watches(
			&appv1.TracingSample{},
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

// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// pkg/reconcile/reconcile.go

package reconcile

import (
	"context"
	"reflect"

	tracingclient "github.com/Azure/operatortrace/operatortrace-go/pkg/client"
	"github.com/Azure/operatortrace/operatortrace-go/pkg/tracingqueue"
	tracingtypes "github.com/Azure/operatortrace/operatortrace-go/pkg/types"
	"k8s.io/client-go/util/workqueue"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	ctrlreconcile "sigs.k8s.io/controller-runtime/pkg/reconcile"
)

type Reconciler = ctrlreconcile.TypedReconciler[tracingtypes.RequestWithTraceID]

// ReconcilerBuilder builds a tracing reconciler with configurable options
type ReconcilerBuilder[T ctrlclient.Object] struct {
	client          tracingclient.TracingClient
	objReconciler   ctrlreconcile.ObjectReconciler[T]
	disableEndTrace bool
}

// NewReconcilerBuilder creates a new builder for a tracing reconciler
func NewReconcilerBuilder[T ctrlclient.Object](client tracingclient.TracingClient, rec ctrlreconcile.ObjectReconciler[T]) *ReconcilerBuilder[T] {
	return &ReconcilerBuilder[T]{
		client:        client,
		objReconciler: rec,
	}
}

// WithDisableEndTrace disables the automatic EndTrace call at the end of Reconcile.
// Use this when you want to manage trace lifecycle manually.
func (b *ReconcilerBuilder[T]) WithDisableEndTrace() *ReconcilerBuilder[T] {
	b.disableEndTrace = true
	return b
}

// Build constructs the final TypedReconciler
func (b *ReconcilerBuilder[T]) Build() ctrlreconcile.TypedReconciler[tracingtypes.RequestWithTraceID] {
	return &objectReconcilerAdapter[T]{
		objReconciler:   b.objReconciler,
		client:          b.client,
		disableEndTrace: b.disableEndTrace,
	}
}

func TracingOptions() controller.TypedOptions[tracingtypes.RequestWithTraceID] {
	myQueueFactory := func(name string, rl workqueue.TypedRateLimiter[tracingtypes.RequestWithTraceID]) workqueue.TypedRateLimitingInterface[tracingtypes.RequestWithTraceID] {
		return tracingqueue.NewTracingQueue()
	}
	opt := controller.TypedOptions[tracingtypes.RequestWithTraceID]{
		NewQueue: myQueueFactory,
	}
	return opt
}

// AsTracingReconciler creates a Reconciler based on the given ObjectReconciler.
// For simple cases with default configuration.
// For advanced configuration, use NewReconcilerBuilder instead.
func AsTracingReconciler[T ctrlclient.Object](client tracingclient.TracingClient, rec ctrlreconcile.ObjectReconciler[T]) ctrlreconcile.TypedReconciler[tracingtypes.RequestWithTraceID] {
	return NewReconcilerBuilder(client, rec).Build()
}

// objectReconcilerAdapter is the object for creating a reconcile request as a converted object.
type objectReconcilerAdapter[T ctrlclient.Object] struct {
	objReconciler   ctrlreconcile.ObjectReconciler[T]
	client          tracingclient.TracingClient
	disableEndTrace bool // If true, the EndTrace call is NOT made at the end of Reconcile. (default is false - EndTrace is called)
}

// Reconcile implements Reconciler.
func (a *objectReconcilerAdapter[T]) Reconcile(ctx context.Context, req tracingtypes.RequestWithTraceID) (ctrlreconcile.Result, error) {
	o := reflect.New(reflect.TypeOf(*new(T)).Elem()).Interface().(T)

	ctx, span, err := a.client.StartTrace(ctx, &req, o)
	defer span.End()
	if err != nil {
		span.RecordError(err)
		return ctrlreconcile.Result{}, ctrlclient.IgnoreNotFound(err)
	}

	result, err := a.objReconciler.Reconcile(ctx, o)

	if err != nil {
		// Record the error in the span
		span.RecordError(err)
	}

	if !a.disableEndTrace {
		// errors from EndTrace are recorded in the span
		a.client.EndTrace(ctx, o)
	}

	return result, err
}

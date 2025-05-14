// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// pkg/reconcile/reconcile.go

package reconcile

import (
	"context"
	"reflect"

	tracingclient "github.com/Azure/operatortrace/operatortrace-go/pkg/client"
	tracingtypes "github.com/Azure/operatortrace/operatortrace-go/pkg/types"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	ctrlreconcile "sigs.k8s.io/controller-runtime/pkg/reconcile"
)

type Reconciler = ctrlreconcile.TypedReconciler[tracingtypes.RequestWithTraceID]

// AsReconciler creates a Reconciler based on the given ObjectReconciler.
func AsTracingReconciler[T ctrlclient.Object](client tracingclient.TracingClient, rec ctrlreconcile.ObjectReconciler[T]) ctrlreconcile.TypedReconciler[tracingtypes.RequestWithTraceID] {
	return &objectReconcilerAdapter[T]{
		objReconciler: rec,
		client:        client,
	}
}

// objectReconcilerAdapter is the object for creating a reconcile request as a converted object.
type objectReconcilerAdapter[T ctrlclient.Object] struct {
	objReconciler ctrlreconcile.ObjectReconciler[T]
	client        tracingclient.TracingClient
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

	return a.objReconciler.Reconcile(ctx, o)
}

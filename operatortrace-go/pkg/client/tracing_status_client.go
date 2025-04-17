// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// pkg/client/tracing_status_client.go

package client

import (
	"context"
	"fmt"

	"github.com/Azure/operatortrace/operatortrace-go/pkg/predicates"
	"github.com/go-logr/logr"
	"go.opentelemetry.io/otel/trace"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
)

type tracingStatusClient struct {
	scheme *runtime.Scheme
	Client client.Client
	client.StatusWriter
	trace.Tracer
	Logger logr.Logger
}

var _ client.StatusWriter = (*tracingStatusClient)(nil)

func (tc *tracingClient) Status() client.StatusWriter {
	return &tracingStatusClient{
		scheme:       tc.scheme,
		Client:       tc.Client,
		StatusWriter: tc.Client.Status(),
		Tracer:       tc.Tracer,
		Logger:       tc.Logger,
	}
}

func (ts *tracingStatusClient) Update(ctx context.Context, obj client.Object, opts ...client.SubResourceUpdateOption) error {
	gvk, err := apiutil.GVKForObject(obj, ts.scheme)
	if err != nil {
		return fmt.Errorf("problem getting the scheme: %w", err)
	}

	existingObj := obj.DeepCopyObject().(client.Object)
	if err := ts.Client.Get(ctx, client.ObjectKeyFromObject(obj), existingObj); err != nil {
		return err
	}

	if !predicates.HasSignificantUpdate(existingObj, obj) {
		ts.Logger.Info("Skipping update as object content has not changed", "object", obj.GetName())
		return nil
	}

	kind := gvk.GroupKind().Kind

	ctx, span := startSpanFromContext(ctx, ts.Logger, ts.Tracer, obj, ts.scheme, fmt.Sprintf("StatusUpdate %s %s", kind, obj.GetName()))
	defer span.End()

	setConditionMessage("TraceID", span.SpanContext().TraceID().String(), obj, ts.scheme)
	setConditionMessage("SpanID", span.SpanContext().SpanID().String(), obj, ts.scheme)

	ts.Logger.Info("updating status object", "object", obj.GetName())
	err = ts.StatusWriter.Update(ctx, obj, opts...)
	if err != nil {
		span.RecordError(err)
	}
	return err
}

func (ts *tracingStatusClient) Patch(ctx context.Context, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
	gvk, err := apiutil.GVKForObject(obj, ts.scheme)
	if err != nil {
		return fmt.Errorf("problem getting the scheme: %w", err)
	}

	existingObj := obj.DeepCopyObject().(client.Object)
	if err := ts.Client.Get(ctx, client.ObjectKeyFromObject(obj), existingObj); err != nil {
		return err
	}

	if !predicates.HasSignificantUpdate(existingObj, obj) {
		ts.Logger.Info("Skipping update as object content has not changed", "object", obj.GetName())
		return nil
	}

	kind := gvk.GroupKind().Kind

	ctx, span := startSpanFromContext(ctx, ts.Logger, ts.Tracer, obj, ts.scheme, fmt.Sprintf("StatusPatch %s %s", kind, obj.GetName()))
	defer span.End()

	setConditionMessage("TraceID", span.SpanContext().TraceID().String(), obj, ts.scheme)
	setConditionMessage("SpanID", span.SpanContext().SpanID().String(), obj, ts.scheme)

	ts.Logger.Info("patching status object", "object", obj.GetName())
	err = ts.StatusWriter.Patch(ctx, obj, patch, opts...)
	if err != nil {
		span.RecordError(err)
	}

	return err
}

func (ts *tracingStatusClient) Create(ctx context.Context, obj client.Object, subResource client.Object, opts ...client.SubResourceCreateOption) error {
	gvk, err := apiutil.GVKForObject(obj, ts.scheme)
	if err != nil {
		return fmt.Errorf("problem getting the scheme: %w", err)
	}

	kind := gvk.GroupKind().Kind

	ctx, span := startSpanFromContext(ctx, ts.Logger, ts.Tracer, obj, ts.scheme, fmt.Sprintf("StatusCreate %s %s", kind, obj.GetName()))
	defer span.End()

	setConditionMessage("TraceID", span.SpanContext().TraceID().String(), obj, ts.scheme)
	setConditionMessage("SpanID", span.SpanContext().SpanID().String(), obj, ts.scheme)

	ts.Logger.Info("creating status object", "object", obj.GetName())
	err = ts.StatusWriter.Create(ctx, obj, subResource, opts...)
	if err != nil {
		span.RecordError(err)
	}
	return err
}

// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// pkg/client/tracing_client.go

package client

import (
	"context"
	"fmt"

	"github.com/Azure/operatortrace/operatortrace-go/pkg/predicates"
	"github.com/Azure/operatortrace/operatortrace-go/pkg/tracecontext"
	tracingtypes "github.com/Azure/operatortrace/operatortrace-go/pkg/types"
	"github.com/go-logr/logr"
	"go.opentelemetry.io/otel/trace"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
)

// TracingClient wraps the Kubernetes client to add tracing functionality
type tracingClient struct {
	scheme *runtime.Scheme
	client.Client
	client.Reader
	trace.Tracer
	Logger  logr.Logger
	options Options
}

var _ TracingClient = (*tracingClient)(nil)

// NewTracingClient initializes and returns a new TracingClient
// optional scheme.  If not, it will use client-go scheme
func NewTracingClient(c client.Client, r client.Reader, t trace.Tracer, l logr.Logger, scheme ...*runtime.Scheme) TracingClient {
	tracingScheme := clientgoscheme.Scheme
	if len(scheme) > 0 && scheme[0] != nil {
		tracingScheme = scheme[0]
	}

	return newTracingClientWithOptions(c, r, t, l, tracingScheme)
}

// NewTracingClientWithOptions allows callers to customize operatortrace behavior via Option functions.
func NewTracingClientWithOptions(c client.Client, r client.Reader, t trace.Tracer, l logr.Logger, scheme *runtime.Scheme, optFns ...Option) TracingClient {
	tracingScheme := scheme
	if tracingScheme == nil {
		tracingScheme = clientgoscheme.Scheme
	}
	return newTracingClientWithOptions(c, r, t, l, tracingScheme, optFns...)
}

func newTracingClientWithOptions(c client.Client, r client.Reader, t trace.Tracer, l logr.Logger, scheme *runtime.Scheme, optFns ...Option) TracingClient {
	return &tracingClient{
		scheme:  scheme,
		Client:  c,
		Reader:  r,
		Tracer:  t,
		Logger:  l,
		options: newOptions(optFns...),
	}
}

// Create adds tracing and traceID annotation around the original client's Create method
func (tc *tracingClient) Create(ctx context.Context, obj client.Object, opts ...client.CreateOption) error {
	gvk, err := apiutil.GVKForObject(obj, tc.scheme)
	if err != nil {
		return fmt.Errorf("problem getting the scheme: %w", err)
	}

	kind := gvk.GroupKind().Kind

	createSpanOpts := []trace.SpanStartOption{trace.WithSpanKind(trace.SpanKindProducer)}
	ctx, spanCreate := startSpanFromContext(ctx, tc.Logger, tc.Tracer, obj, tc.scheme, tc.options, fmt.Sprintf("Create %s %s", kind, obj.GetName()), [10]tracingtypes.LinkedSpan{}, createSpanOpts...)
	defer spanCreate.End()

	addTraceAnnotations(ctx, obj, tc.options)
	tc.Logger.Info("Creating object", "object", obj.GetName())
	err = tc.Client.Create(ctx, obj, opts...)
	if err != nil {
		spanCreate.RecordError(err)
	}

	return err
}

// Update adds tracing and traceID annotation around the original client's Update method
func (tc *tracingClient) Update(ctx context.Context, obj client.Object, opts ...client.UpdateOption) error {
	gvk, err := apiutil.GVKForObject(obj, tc.scheme)
	if err != nil {
		return fmt.Errorf("problem getting the scheme: %w", err)
	}

	kind := gvk.GroupKind().Kind

	// Prepare span (internal) for diff / significance check
	ctx, spanPrepare := startSpanFromContext(ctx, tc.Logger, tc.Tracer, obj, tc.scheme, tc.options, fmt.Sprintf("Prepare Update %s %s", kind, obj.GetName()), [10]tracingtypes.LinkedSpan{})
	defer spanPrepare.End()

	existingObj := obj.DeepCopyObject().(client.Object)
	if err := tc.Client.Get(ctx, client.ObjectKeyFromObject(obj), existingObj); err != nil {
		return err
	}

	if !predicates.HasSignificantUpdate(existingObj, obj) {
		tc.Logger.Info("Skipping update as object content has not changed", "object", obj.GetName())
		return nil
	}

	// Second span (producer) only for the actual mutation
	updateSpanOpts := []trace.SpanStartOption{trace.WithSpanKind(trace.SpanKindProducer)}
	ctx, spanUpdate := startSpanFromContext(ctx, tc.Logger, tc.Tracer, obj, tc.scheme, tc.options, fmt.Sprintf("Update %s %s", kind, obj.GetName()), [10]tracingtypes.LinkedSpan{}, updateSpanOpts...)
	defer spanUpdate.End()

	addTraceAnnotations(ctx, obj, tc.options)
	tc.Logger.Info("Updating object", "object", obj.GetName())

	// if resource version has changed, and there are no significant updates, we should do a patch instead of an update. This means probably just the traceID has changed / been removed.
	if existingObj.GetResourceVersion() != obj.GetResourceVersion() {
		tc.Logger.Info("Resource version has changed, using Patch instead of Update", "object", obj.GetName())
		err = tc.Patch(ctx, obj, client.MergeFrom(existingObj))
		if err != nil {
			spanUpdate.RecordError(err)
		}
		return err
	}

	// If the resource version has not changed, we can do a full update
	err = tc.Client.Update(ctx, obj, opts...)
	if err != nil {
		spanUpdate.RecordError(err)
	}

	return err
}

func (tc *tracingClient) StartSpan(ctx context.Context, operationName string) (context.Context, trace.Span) {
	return startSpanFromContext(ctx, tc.Logger, tc.Tracer, nil, tc.scheme, tc.options, operationName, [10]tracingtypes.LinkedSpan{})
}

// EmbedTraceIDInNamespacedName embeds the traceID and spanID in the key.Name
func (tc *tracingClient) EmbedTraceIDInRequest(requestWithTraceID *tracingtypes.RequestWithTraceID, obj client.Object) error {
	stored, ok := extractTraceContextFromAnnotations(obj.GetAnnotations(), tc.options)
	if !ok || stored.TraceParent == "" {
		return nil
	}
	spanContext, err := tracecontext.SpanContextFromTraceData(stored.TraceParent, stored.TraceState)
	if err != nil {
		return nil
	}

	gvk, err := apiutil.GVKForObject(obj, tc.scheme)
	if err != nil {
		return fmt.Errorf("problem getting the scheme: %w", err)
	}
	objectKind := gvk.GroupKind().Kind
	objectName := obj.GetName()

	requestWithTraceID.Parent.TraceID = spanContext.TraceID().String()
	requestWithTraceID.Parent.SpanID = spanContext.SpanID().String()
	requestWithTraceID.Parent.Kind = objectKind
	requestWithTraceID.Parent.Name = objectName

	tc.Logger.Info("EmbedTraceIDInNamespacedName", "objectName", requestWithTraceID.Name)

	return nil
}

// Get adds tracing around the original client's Get method
// IMPORTANT: Caller MUST call `defer span.End()` to end the trace from the calling function
func (tc *tracingClient) StartTrace(ctx context.Context, requestWithTraceID *tracingtypes.RequestWithTraceID, obj client.Object, opts ...client.GetOption) (context.Context, trace.Span, error) {
	// All StartTrace call spans will be Consumer spans
	spanOpts := []trace.SpanStartOption{
		trace.WithSpanKind(trace.SpanKindConsumer),
	}

	// Create or retrieve the span from the context
	getErr := tc.Reader.Get(ctx, requestWithTraceID.NamespacedName, obj, opts...)
	if getErr != nil {
		ctx, span := startSpanFromContext(ctx, tc.Logger, tc.Tracer, obj, tc.scheme, tc.options, fmt.Sprintf("StartTrace Unknown Object %s", requestWithTraceID.NamespacedName), requestWithTraceID.LinkedSpans, spanOpts...)
		return trace.ContextWithSpan(ctx, span), span, getErr
	}
	overrideTraceContextFromRequest(*requestWithTraceID, obj, tc.options)

	gvk, err := apiutil.GVKForObject(obj, tc.scheme)
	objectKind := ""
	if err == nil {
		objectKind = gvk.GroupKind().Kind
	}
	name := requestWithTraceID.Name
	callerName := requestWithTraceID.Parent.Name
	callerKind := requestWithTraceID.Parent.Kind

	operationName := ""

	if callerKind != "" && callerName != "" {
		operationName = fmt.Sprintf("StartTrace %s/%s Triggered By Changed Object %s/%s", objectKind, name, callerKind, callerName)
	} else {
		operationName = fmt.Sprintf("StartTrace %s %s", objectKind, name)
	}

	ctx, span := startSpanFromContext(ctx, tc.Logger, tc.Tracer, obj, tc.scheme, tc.options, operationName, requestWithTraceID.LinkedSpans, spanOpts...)

	if err != nil {
		span.RecordError(err)
	}

	tc.Logger.Info("Getting object", "object", name)
	return trace.ContextWithSpan(ctx, span), span, err
}

// Ends the trace by clearing the traceid from the object
func (tc *tracingClient) EndTrace(ctx context.Context, obj client.Object, opts ...client.PatchOption) error {
	ctx, span := startSpanFromContext(ctx, tc.Logger, tc.Tracer, obj, tc.scheme, tc.options, fmt.Sprintf("EndTrace %s %s", obj.GetObjectKind().GroupVersionKind().Kind, obj.GetName()), [10]tracingtypes.LinkedSpan{})
	defer span.End()

	annotations := obj.GetAnnotations()
	if annotations == nil {
		return nil
	}

	// get the current object and ensure that current object has the expected traceid and spanid annotations
	currentObjFromServer := obj.DeepCopyObject().(client.Object)
	err := tc.Reader.Get(ctx, client.ObjectKeyFromObject(obj), currentObjFromServer)

	if err != nil {
		span.RecordError(err)
	}

	// compare the stored trace context from current object to ensure that it has not changed
	currentStored, _ := extractTraceContextFromAnnotations(currentObjFromServer.GetAnnotations(), tc.options)
	desiredStored, _ := extractTraceContextFromAnnotations(obj.GetAnnotations(), tc.options)
	if currentStored.TraceParent != desiredStored.TraceParent {
		tc.Logger.Info("Trace context has changed, skipping patch", "object", obj.GetName())
		span.RecordError(fmt.Errorf("trace context has changed, skipping patch: object %s", obj.GetName()))
		return nil
	}

	// Remove the traceid and spanid annotations and create a patch
	original := obj.DeepCopyObject().(client.Object)
	patch := client.MergeFrom(original)

	persistTraceCarrier(annotations, tc.options, "", "")
	obj.SetAnnotations(annotations)

	tc.Logger.Info("Patching object", "object", obj.GetName())
	// Use the Patch function to apply the patch

	err = tc.Client.Patch(ctx, obj, patch, opts...)

	if err != nil {
		span.RecordError(err)
	}

	original = obj.DeepCopyObject().(client.Object)
	// remove the traceid and spanid conditions from the object and create a status().patch
	deleteConditionAsMap("TraceID", obj, tc.scheme)
	deleteConditionAsMap("SpanID", obj, tc.scheme)
	patch = client.MergeFrom(original)

	tc.Logger.Info("Patching object status", "object", obj.GetName())
	err = tc.Client.Status().Patch(ctx, obj, patch)

	if err != nil {
		span.RecordError(err)
	}

	return err
}

// Get adds tracing around the original client's Get method
func (tc *tracingClient) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	// Create or retrieve the span from the context
	gvk, err := apiutil.GVKForObject(obj, tc.scheme)
	if err != nil {
		return fmt.Errorf("problem getting the scheme: %w", err)
	}

	kind := gvk.GroupKind().Kind

	ctx, span := startSpanFromContext(ctx, tc.Logger, tc.Tracer, obj, tc.scheme, tc.options, fmt.Sprintf("Get %s %s", kind, key.Name), [10]tracingtypes.LinkedSpan{})
	defer span.End()

	tc.Logger.Info("Getting object", "object", key.Name)

	err = tc.Reader.Get(ctx, key, obj, opts...)

	if err != nil {
		span.RecordError(err)
	}

	return err
}

func (tc *tracingClient) List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
	gvk, _ := apiutil.GVKForObject(list, tc.scheme)
	kind := gvk.GroupKind().Kind
	ctx, span := startSpanFromContextGeneric(ctx, tc.Logger, tc.Tracer, kind)
	defer span.End()

	tc.Logger.Info("Getting List", "object", kind)
	err := tc.Client.List(ctx, list, opts...)
	if err != nil {
		span.RecordError(err)
	}
	return err
}

// Patch  adds tracing and traceID annotation around the original client's Patch method
func (tc *tracingClient) Patch(ctx context.Context, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
	gvk, err := apiutil.GVKForObject(obj, tc.scheme)
	if err != nil {
		return fmt.Errorf("problem getting the scheme: %w", err)
	}

	kind := gvk.GroupKind().Kind

	ctx, spanPrepare := startSpanFromContext(ctx, tc.Logger, tc.Tracer, obj, tc.scheme, tc.options, fmt.Sprintf("Prepare Patch %s %s", kind, obj.GetName()), [10]tracingtypes.LinkedSpan{})
	defer spanPrepare.End()

	existingObj := obj.DeepCopyObject().(client.Object)
	if err := tc.Client.Get(ctx, client.ObjectKeyFromObject(obj), existingObj); err != nil {
		return err
	}

	if !predicates.HasSignificantUpdate(existingObj, obj) {
		tc.Logger.Info("Skipping update as object content has not changed", "object", obj.GetName())
		return nil
	}

	// Actually doing the update will be another span that is a producer span
	// All Patch / Update call spans will be Producer spans
	spanOpts := []trace.SpanStartOption{
		trace.WithSpanKind(trace.SpanKindProducer),
	}

	ctx, spanPatch := startSpanFromContext(ctx, tc.Logger, tc.Tracer, obj, tc.scheme, tc.options, fmt.Sprintf("Patch %s %s", kind, obj.GetName()), [10]tracingtypes.LinkedSpan{}, spanOpts...)
	defer spanPatch.End()

	addTraceAnnotations(ctx, obj, tc.options)
	tc.Logger.Info("Patching object", "object", obj.GetName())
	err = tc.Client.Patch(ctx, obj, patch, opts...)
	if err != nil {
		spanPatch.RecordError(err)
	}

	return err
}

// Delete adds tracing around the original client's Delete method
func (tc *tracingClient) Delete(ctx context.Context, obj client.Object, opts ...client.DeleteOption) error {
	gvk, err := apiutil.GVKForObject(obj, tc.scheme)
	if err != nil {
		return fmt.Errorf("problem getting the scheme: %w", err)
	}

	kind := gvk.GroupKind().Kind

	deleteSpanOpts := []trace.SpanStartOption{trace.WithSpanKind(trace.SpanKindProducer)}
	ctx, spanDelete := startSpanFromContext(ctx, tc.Logger, tc.Tracer, obj, tc.scheme, tc.options, fmt.Sprintf("Delete %s %s", kind, obj.GetName()), [10]tracingtypes.LinkedSpan{}, deleteSpanOpts...)
	defer spanDelete.End()

	tc.Logger.Info("Deleting object", "object", obj.GetName())
	err = tc.Client.Delete(ctx, obj, opts...)
	if err != nil {
		spanDelete.RecordError(err)
	}
	return err
}

func (tc *tracingClient) DeleteAllOf(ctx context.Context, obj client.Object, opts ...client.DeleteAllOfOption) error {
	gvk, err := apiutil.GVKForObject(obj, tc.scheme)
	if err != nil {
		return fmt.Errorf("problem getting the scheme: %w", err)
	}

	kind := gvk.GroupKind().Kind

	deleteAllOfSpanOpts := []trace.SpanStartOption{trace.WithSpanKind(trace.SpanKindProducer)}
	ctx, spanDeleteAll := startSpanFromContext(ctx, tc.Logger, tc.Tracer, obj, tc.scheme, tc.options, fmt.Sprintf("DeleteAllOf %s %s", kind, obj.GetName()), [10]tracingtypes.LinkedSpan{}, deleteAllOfSpanOpts...)
	defer spanDeleteAll.End()

	tc.Logger.Info("Deleting all of object", "object", obj.GetName())
	err = tc.Client.DeleteAllOf(ctx, obj, opts...)
	if err != nil {
		spanDeleteAll.RecordError(err)
	}
	return err

}

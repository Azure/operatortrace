// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// pkg/client/tracing_client.go

package client

import (
	"context"
	"fmt"

	constants "github.com/Azure/operatortrace/operatortrace-go/pkg/constants"
	"github.com/Azure/operatortrace/operatortrace-go/pkg/predicates"
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

// Options holds the configuration for TracingClient
type Options struct {
	LinkedTraceIDLocation string
}

// Option is a function that configures Options
type Option func(*Options)

var _ TracingClient = (*tracingClient)(nil)

// NewTracingClient initializes and returns a new TracingClient
// optional scheme.  If not, it will use client-go scheme
func NewTracingClient(c client.Client, r client.Reader, t trace.Tracer, l logr.Logger, scheme ...*runtime.Scheme) TracingClient {
	tracingScheme := clientgoscheme.Scheme
	if len(scheme) > 0 {
		tracingScheme = scheme[0]
	}

	return &tracingClient{
		scheme: tracingScheme,
		Client: c,
		Reader: r,
		Tracer: t,
		Logger: l,
	}
}

// Create adds tracing and traceID annotation around the original client's Create method
func (tc *tracingClient) Create(ctx context.Context, obj client.Object, opts ...client.CreateOption) error {
	gvk, err := apiutil.GVKForObject(obj, tc.scheme)
	if err != nil {
		return fmt.Errorf("problem getting the scheme: %w", err)
	}

	kind := gvk.GroupKind().Kind
	ctx, span := startSpanFromContext(ctx, tc.Logger, tc.Tracer, obj, tc.scheme, fmt.Sprintf("Create %s %s", kind, obj.GetName()))
	defer span.End()

	addTraceIDAnnotation(ctx, obj)
	tc.Logger.Info("Creating object", "object", obj.GetName())
	err = tc.Client.Create(ctx, obj, opts...)
	if err != nil {
		span.RecordError(err)
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

	ctx, span := startSpanFromContext(ctx, tc.Logger, tc.Tracer, obj, tc.scheme, fmt.Sprintf("Update %s %s", kind, obj.GetName()))
	defer span.End()

	existingObj := obj.DeepCopyObject().(client.Object)
	if err := tc.Client.Get(ctx, client.ObjectKeyFromObject(obj), existingObj); err != nil {
		return err
	}

	if !predicates.HasSignificantUpdate(existingObj, obj) {
		tc.Logger.Info("Skipping update as object content has not changed", "object", obj.GetName())
		return nil
	}

	addTraceIDAnnotation(ctx, obj)
	tc.Logger.Info("Updating object", "object", obj.GetName())

	err = tc.Client.Update(ctx, obj, opts...)
	if err != nil {
		span.RecordError(err)
	}

	return err
}

func (tc *tracingClient) StartSpan(ctx context.Context, operationName string) (context.Context, trace.Span) {
	return startSpanFromContext(ctx, tc.Logger, tc.Tracer, nil, tc.scheme, operationName)
}

// EmbedTraceIDInNamespacedName embeds the traceID and spanID in the key.Name
func (tc *tracingClient) EmbedTraceIDInRequest(requestWithTraceID *tracingtypes.RequestWithTraceID, obj client.Object) error {
	traceID := obj.GetAnnotations()[constants.TraceIDAnnotation]
	spanID := obj.GetAnnotations()[constants.SpanIDAnnotation]
	if traceID == "" || spanID == "" {
		return nil
	}

	gvk, err := apiutil.GVKForObject(obj, tc.scheme)
	if err != nil {
		return fmt.Errorf("problem getting the scheme: %w", err)
	}
	objectKind := gvk.GroupKind().Kind
	objectName := obj.GetName()

	requestWithTraceID.TraceID = traceID
	requestWithTraceID.SpanID = spanID
	requestWithTraceID.SenderKind = objectKind
	requestWithTraceID.SenderName = objectName

	tc.Logger.Info("EmbedTraceIDInNamespacedName", "objectName", requestWithTraceID.Name)

	return nil
}

// Get adds tracing around the original client's Get method
// IMPORTANT: Caller MUST call `defer span.End()` to end the trace from the calling function
func (tc *tracingClient) StartTrace(ctx context.Context, requestWithTraceID *tracingtypes.RequestWithTraceID, obj client.Object, opts ...client.GetOption) (context.Context, trace.Span, error) {
	// Create or retrieve the span from the context
	getErr := tc.Reader.Get(ctx, requestWithTraceID.NamespacedName, obj, opts...)
	if getErr != nil {
		ctx, span := startSpanFromContext(ctx, tc.Logger, tc.Tracer, obj, tc.scheme, fmt.Sprintf("StartTrace Unknown Object %s", requestWithTraceID.NamespacedName))
		return trace.ContextWithSpan(ctx, span), span, getErr
	}
	overrideTraceIDFromNamespacedName(*requestWithTraceID, obj)

	gvk, err := apiutil.GVKForObject(obj, tc.scheme)
	objectKind := ""
	if err == nil {
		objectKind = gvk.GroupKind().Kind
	}
	name := requestWithTraceID.Name
	callerName := requestWithTraceID.SenderName
	callerKind := requestWithTraceID.SenderKind

	operationName := ""

	if callerKind != "" && callerName != "" {
		operationName = fmt.Sprintf("StartTrace %s/%s Triggered By Changed Object %s/%s", objectKind, name, callerKind, callerName)
	} else {
		operationName = fmt.Sprintf("StartTrace %s %s", objectKind, name)
	}

	ctx, span := startSpanFromContext(ctx, tc.Logger, tc.Tracer, obj, tc.scheme, operationName)

	if err != nil {
		span.RecordError(err)
	}

	tc.Logger.Info("Getting object", "object", name)
	return trace.ContextWithSpan(ctx, span), span, err
}

// Ends the trace by clearing the traceid from the object
func (tc *tracingClient) EndTrace(ctx context.Context, obj client.Object, opts ...client.PatchOption) (client.Object, error) {
	ctx, span := startSpanFromContext(ctx, tc.Logger, tc.Tracer, obj, tc.scheme, fmt.Sprintf("EndTrace %s %s", obj.GetObjectKind().GroupVersionKind().Kind, obj.GetName()))
	defer span.End()

	annotations := obj.GetAnnotations()
	if annotations == nil {
		return obj, nil
	}

	// get the current object and ensure that current object has the expected traceid and spanid annotations
	currentObjFromServer := obj.DeepCopyObject().(client.Object)
	err := tc.Reader.Get(ctx, client.ObjectKeyFromObject(obj), currentObjFromServer)

	if err != nil {
		span.RecordError(err)
	}

	// compare the traceid and spanid from currentobj to ensure that the traceid and spanid are not changed
	if currentObjFromServer.GetAnnotations()[constants.TraceIDAnnotation] != obj.GetAnnotations()[constants.TraceIDAnnotation] {
		tc.Logger.Info("TraceID has changed, skipping patch", "object", obj.GetName())
		span.RecordError(fmt.Errorf("TraceID has changed, skipping patch: object %s", obj.GetName()))
		return obj, nil
	}

	// Remove the traceid and spanid annotations and create a patch
	original := obj.DeepCopyObject().(client.Object)
	patch := client.MergeFrom(original)

	delete(annotations, constants.TraceIDAnnotation)
	delete(annotations, constants.SpanIDAnnotation)
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

	return obj, err
}

// Get adds tracing around the original client's Get method
func (tc *tracingClient) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	// Create or retrieve the span from the context
	gvk, err := apiutil.GVKForObject(obj, tc.scheme)
	if err != nil {
		return fmt.Errorf("problem getting the scheme: %w", err)
	}

	kind := gvk.GroupKind().Kind

	ctx, span := startSpanFromContext(ctx, tc.Logger, tc.Tracer, obj, tc.scheme, fmt.Sprintf("Get %s %s", kind, key.Name))
	defer span.End()

	tc.Logger.Info("Getting object", "object", key.Name)

	err = tc.Client.Get(ctx, key, obj, opts...)

	if err != nil {
		span.RecordError(err)
	}

	return err
}

func (tc *tracingClient) List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
	gvk, _ := apiutil.GVKForObject(list, tc.scheme)
	kind := gvk.GroupKind().Kind
	ctx, span := startSpanFromContextList(ctx, tc.Logger, tc.Tracer, list, kind)
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

	ctx, span := startSpanFromContext(ctx, tc.Logger, tc.Tracer, obj, tc.scheme, fmt.Sprintf("Patch %s %s", kind, obj.GetName()))
	defer span.End()

	existingObj := obj.DeepCopyObject().(client.Object)
	if err := tc.Client.Get(ctx, client.ObjectKeyFromObject(obj), existingObj); err != nil {
		return err
	}

	if !predicates.HasSignificantUpdate(existingObj, obj) {
		tc.Logger.Info("Skipping update as object content has not changed", "object", obj.GetName())
		return nil
	}

	addTraceIDAnnotation(ctx, obj)
	tc.Logger.Info("Patching object", "object", obj.GetName())
	err = tc.Client.Patch(ctx, obj, patch, opts...)
	if err != nil {
		span.RecordError(err)
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

	ctx, span := startSpanFromContext(ctx, tc.Logger, tc.Tracer, obj, tc.scheme, fmt.Sprintf("Delete %s %s", kind, obj.GetName()))
	defer span.End()

	tc.Logger.Info("Deleting object", "object", obj.GetName())
	err = tc.Client.Delete(ctx, obj, opts...)
	if err != nil {
		span.RecordError(err)
	}
	return err
}

func (tc *tracingClient) DeleteAllOf(ctx context.Context, obj client.Object, opts ...client.DeleteAllOfOption) error {
	gvk, err := apiutil.GVKForObject(obj, tc.scheme)
	if err != nil {
		return fmt.Errorf("problem getting the scheme: %w", err)
	}

	kind := gvk.GroupKind().Kind

	ctx, span := startSpanFromContext(ctx, tc.Logger, tc.Tracer, obj, tc.scheme, fmt.Sprintf("DeleteAllOf %s %s", kind, obj.GetName()))
	defer span.End()

	tc.Logger.Info("Deleting all of object", "object", obj.GetName())
	err = tc.Client.DeleteAllOf(ctx, obj, opts...)
	if err != nil {
		span.RecordError(err)
	}
	return err

}

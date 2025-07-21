// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// pkg/client/genericclient.go

package client

import (
	"context"
	"fmt"
	"time"

	constants "github.com/Azure/operatortrace/operatortrace-go/pkg/constants"
	tracingtypes "github.com/Azure/operatortrace/operatortrace-go/pkg/types"
	"github.com/go-logr/logr"
	"go.opentelemetry.io/otel/trace"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
)

type GenericClient interface {
	StartTrace(ctx context.Context, obj client.Object) (context.Context, trace.Span, error)
	EndTrace(ctx context.Context, obj client.Object) error
	StartSpan(ctx context.Context, operationName string) (context.Context, trace.Span)
	SetSpan(ctx context.Context, obj client.Object) (context.Context, trace.Span)
}

// genericClient wraps the trace.Tracer to provide helper methods for tracing kubernetes objects.
type genericClient struct {
	trace.Tracer
	logr.Logger
	scheme *runtime.Scheme
}

// NewTracingClient initializes and returns a new TracingClient
// optional scheme.  If not, it will use client-go scheme
func NewGenericClient(t trace.Tracer, l logr.Logger, scheme ...*runtime.Scheme) GenericClient {
	tracingScheme := clientgoscheme.Scheme
	if len(scheme) > 0 {
		tracingScheme = scheme[0]
	}

	return &genericClient{
		Tracer: t,
		Logger: l,
		scheme: tracingScheme,
	}
}

// StartTrace starts a new trace span from the given object.
func (gc *genericClient) StartTrace(ctx context.Context, obj client.Object) (context.Context, trace.Span, error) {
	linkedSpans := [10]tracingtypes.LinkedSpan{}

	gvk, err := apiutil.GVKForObject(obj, gc.scheme)
	objectName := obj.GetName()
	objectKind := ""
	if err == nil {
		objectKind = gvk.GroupKind().Kind
	}

	ctx, span := startSpanFromContext(ctx, gc.Logger, gc.Tracer, obj, gc.scheme, fmt.Sprintf("StartTrace %s %s", objectKind, objectName), linkedSpans)
	if err != nil {
		span.RecordError(err)
	}

	gc.Logger.Info("Getting object", "object", objectName)
	return trace.ContextWithSpan(ctx, span), span, err
}

// EndTrace ends the trace span for the given object.
func (gc *genericClient) EndTrace(ctx context.Context, obj client.Object) error {
	annotations := obj.GetAnnotations()
	if annotations == nil {
		return nil
	}

	delete(annotations, constants.TraceIDAnnotation)
	delete(annotations, constants.SpanIDAnnotation)
	delete(annotations, constants.TraceIDTimeAnnotation)
	obj.SetAnnotations(annotations)

	return nil
}

func (gc *genericClient) StartSpan(ctx context.Context, operationName string) (context.Context, trace.Span) {
	return startSpanFromContext(ctx, gc.Logger, gc.Tracer, nil, gc.scheme, operationName, [10]tracingtypes.LinkedSpan{})
}

func (gc *genericClient) SetSpan(ctx context.Context, obj client.Object) (context.Context, trace.Span) {
	ctx, span := startSpanFromContextGeneric(ctx, gc.Logger, gc.Tracer, obj.GetName())

	annotations := obj.GetAnnotations()
	if annotations == nil {
		annotations = make(map[string]string)
	}

	annotations[constants.TraceIDAnnotation] = span.SpanContext().TraceID().String()
	annotations[constants.SpanIDAnnotation] = span.SpanContext().SpanID().String()
	annotations[constants.TraceIDTimeAnnotation] = time.Now().Format(time.RFC3339)
	obj.SetAnnotations(annotations)

	return trace.ContextWithSpan(ctx, span), span
}

// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// pkg/client/genericclient.go

package client

import (
	"context"
	"fmt"

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
	scheme  *runtime.Scheme
	options Options
}

// NewTracingClient initializes and returns a new TracingClient
// optional scheme.  If not, it will use client-go scheme
func NewGenericClient(t trace.Tracer, l logr.Logger, scheme ...*runtime.Scheme) GenericClient {
	tracingScheme := clientgoscheme.Scheme
	if len(scheme) > 0 && scheme[0] != nil {
		tracingScheme = scheme[0]
	}

	return newGenericClientWithOptions(t, l, tracingScheme)
}

// NewGenericClientWithOptions allows callers to customize trace annotation behavior.
func NewGenericClientWithOptions(t trace.Tracer, l logr.Logger, scheme *runtime.Scheme, optFns ...Option) GenericClient {
	tracingScheme := scheme
	if tracingScheme == nil {
		tracingScheme = clientgoscheme.Scheme
	}
	return newGenericClientWithOptions(t, l, tracingScheme, optFns...)
}

func newGenericClientWithOptions(t trace.Tracer, l logr.Logger, scheme *runtime.Scheme, optFns ...Option) GenericClient {
	return &genericClient{
		Tracer:  t,
		Logger:  l,
		scheme:  scheme,
		options: newOptions(optFns...),
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

	ctx, span := startSpanFromContext(ctx, gc.Logger, gc.Tracer, obj, gc.scheme, gc.options, fmt.Sprintf("StartTrace %s %s", objectKind, objectName), linkedSpans)
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

	persistTraceCarrier(annotations, gc.options, "", "")
	obj.SetAnnotations(annotations)

	return nil
}

func (gc *genericClient) StartSpan(ctx context.Context, operationName string) (context.Context, trace.Span) {
	return startSpanFromContext(ctx, gc.Logger, gc.Tracer, nil, gc.scheme, gc.options, operationName, [10]tracingtypes.LinkedSpan{})
}

func (gc *genericClient) SetSpan(ctx context.Context, obj client.Object) (context.Context, trace.Span) {
	ctx, span := startSpanFromContextGeneric(ctx, gc.Logger, gc.Tracer, obj.GetName())
	ctxWithSpan := trace.ContextWithSpan(ctx, span)
	addTraceAnnotations(ctxWithSpan, obj, gc.options)
	return ctxWithSpan, span
}

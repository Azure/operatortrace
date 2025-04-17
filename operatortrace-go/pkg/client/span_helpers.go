// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// pkg/client/span_helpers.go

package client

import (
	"context"

	constants "github.com/Azure/operatortrace/operatortrace-go/pkg/constants"
	"github.com/go-logr/logr"
	"go.opentelemetry.io/otel/trace"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// startSpanFromContext starts a new span from the context and attaches trace information to the object
func startSpanFromContext(ctx context.Context, logger logr.Logger, tracer trace.Tracer, obj client.Object, scheme *runtime.Scheme, operationName string) (context.Context, trace.Span) {
	span := trace.SpanFromContext(ctx)
	if span.SpanContext().IsValid() {
		spanContext := trace.NewSpanContext(trace.SpanContextConfig{
			TraceID: span.SpanContext().TraceID(),
			SpanID:  span.SpanContext().SpanID(),
		})
		ctx = trace.ContextWithRemoteSpanContext(ctx, spanContext)
		ctx, span = tracer.Start(ctx, operationName)
		return ctx, span
	}

	if !span.SpanContext().IsValid() {
		if obj != nil {
			// no valid trace ID in context, check object conditions
			if traceID, err := getConditionMessage("TraceID", obj, scheme); err == nil {
				if traceIDValue, err := trace.TraceIDFromHex(traceID); err == nil {
					spanContext := trace.NewSpanContext(trace.SpanContextConfig{})
					if spanID, err := getConditionMessage("SpanID", obj, scheme); err == nil {
						if spanIDValue, err := trace.SpanIDFromHex(spanID); err == nil {
							spanContext = trace.NewSpanContext(trace.SpanContextConfig{
								TraceID: traceIDValue,
								SpanID:  spanIDValue,
							})
						} else {
							spanContext = trace.NewSpanContext(trace.SpanContextConfig{
								TraceID: traceIDValue,
							})
						}
					}
					ctx = trace.ContextWithRemoteSpanContext(ctx, spanContext)
				}
			} else {
				// No valid trace ID in context, check object annotations
				if traceID, ok := obj.GetAnnotations()[constants.TraceIDAnnotation]; ok {
					if traceIDValue, err := trace.TraceIDFromHex(traceID); err == nil {
						spanContext := trace.NewSpanContext(trace.SpanContextConfig{})
						if spanID, ok := obj.GetAnnotations()[constants.SpanIDAnnotation]; ok {
							if spanIDValue, err := trace.SpanIDFromHex(spanID); err == nil {
								spanContext = trace.NewSpanContext(trace.SpanContextConfig{
									TraceID: traceIDValue,
									SpanID:  spanIDValue,
								})
							} else {
								spanContext = trace.NewSpanContext(trace.SpanContextConfig{
									TraceID: traceIDValue,
								})
							}
						}
						ctx = trace.ContextWithRemoteSpanContext(ctx, spanContext)
					} else {
						logger.Error(err, "Invalid trace ID", "traceID", traceID)
					}
				}
			}
		}
	}

	// Create a new span
	ctx, span = tracer.Start(ctx, operationName)
	return ctx, span
}

func startSpanFromContextList(ctx context.Context, logger logr.Logger, tracer trace.Tracer, obj client.ObjectList, operationName string) (context.Context, trace.Span) {
	span := trace.SpanFromContext(ctx)
	if span.SpanContext().IsValid() {
		spanContext := trace.NewSpanContext(trace.SpanContextConfig{
			TraceID: span.SpanContext().TraceID(),
			SpanID:  span.SpanContext().SpanID(),
		})
		ctx = trace.ContextWithRemoteSpanContext(ctx, spanContext)
		ctx, span = tracer.Start(ctx, operationName)
		return trace.ContextWithSpan(ctx, span), span
	}

	// Create a new span
	ctx, span = tracer.Start(ctx, operationName)
	return ctx, span
}

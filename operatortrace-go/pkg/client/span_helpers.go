// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// pkg/client/span_helpers.go

package client

import (
	"context"
	"strconv"
	"time"

	constants "github.com/Azure/operatortrace/operatortrace-go/pkg/constants"
	"github.com/Azure/operatortrace/operatortrace-go/pkg/types"
	"github.com/go-logr/logr"
	"go.opentelemetry.io/otel/trace"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// sliceFromLinkedSpans converts a slice of LinkedSpan to a slice of trace.SpanContext
func sliceFromLinkedSpans(linkedSpans [10]types.LinkedSpan) []trace.Link {
	spanContexts := make([]trace.SpanContext, 0, len(linkedSpans))
	for _, linkedSpan := range linkedSpans {
		if linkedSpan.TraceID != "" && linkedSpan.SpanID != "" {
			traceID, err := trace.TraceIDFromHex(linkedSpan.TraceID)
			if err != nil {
				continue
			}
			spanID, err := trace.SpanIDFromHex(linkedSpan.SpanID)
			if err != nil {
				continue
			}
			spanContexts = append(spanContexts, trace.NewSpanContext(trace.SpanContextConfig{
				TraceID: traceID,
				SpanID:  spanID,
				Remote:  true,
			}))
		}
	}
	links := make([]trace.Link, 0, len(spanContexts))
	for _, spanContext := range spanContexts {
		links = append(links, trace.Link{
			SpanContext: spanContext,
			Attributes:  nil, // No attributes are set for linked spans
		})
	}
	return links
}

// startSpanFromContext starts a new span from the context and attaches trace information to the object
func startSpanFromContext(ctx context.Context, logger logr.Logger, tracer trace.Tracer, obj client.Object, scheme *runtime.Scheme, operationName string, linkedSpansArray [10]types.LinkedSpan) (context.Context, trace.Span) {
	span := trace.SpanFromContext(ctx)
	if span.SpanContext().IsValid() {
		ctx, span = tracer.Start(ctx, operationName)
		return ctx, span
	}

	if !span.SpanContext().IsValid() {
		if obj != nil {
			// no valid trace ID in context, check object conditions
			if traceID, err := getConditionMessage("TraceID", obj, scheme); err == nil {
				if traceID != "" {
					// Check if the traceID is more than 20 minutes old, if it is, we will not use it
					if traceIdTime, err := getConditionTime("TraceID", obj, scheme); err == nil {
						if traceIdTime.Time.Before(metav1.Now().Add(-constants.TraceExpirationTime * time.Minute)) {
							logger.Info("TraceID is more than " + strconv.Itoa(constants.TraceExpirationTime) + " minutes old, not using it")
						} else {
							if traceIDValue, err := trace.TraceIDFromHex(traceID); err == nil {
								spanContext := trace.NewSpanContext(trace.SpanContextConfig{})
								if spanID, err := getConditionMessage("SpanID", obj, scheme); err == nil {
									if spanIDValue, err := trace.SpanIDFromHex(spanID); err == nil {
										spanContext = trace.NewSpanContext(trace.SpanContextConfig{
											TraceID:    traceIDValue,
											SpanID:     spanIDValue,
											Remote:     true,
											TraceFlags: trace.FlagsSampled,
										})
									} else {
										spanContext = trace.NewSpanContext(trace.SpanContextConfig{
											TraceID:    traceIDValue,
											Remote:     true,
											TraceFlags: trace.FlagsSampled,
										})
									}
								}
								ctx = trace.ContextWithRemoteSpanContext(ctx, spanContext)
							}
						}
					}
				}
			} else {
				// No valid trace ID in context, check object annotations
				if traceID, ok := obj.GetAnnotations()[constants.TraceIDAnnotation]; ok {
					if traceID != "" {
						// Check if the traceID is more than 10 minutes (from constants) old, if it is, we will not use it
						if traceIDTime, ok := obj.GetAnnotations()[constants.TraceIDTimeAnnotation]; ok {
							if traceIdTimeValue, err := time.Parse(time.RFC3339, traceIDTime); err == nil {
								if traceIdTimeValue.Before(time.Now().Add(-constants.TraceExpirationTime * time.Minute)) {
									logger.Info("TraceID is more than " + strconv.Itoa(constants.TraceExpirationTime) + " minutes old, not using it")
								} else {
									if traceIDValue, err := trace.TraceIDFromHex(traceID); err == nil {
										spanContext := trace.NewSpanContext(trace.SpanContextConfig{})
										if spanID, ok := obj.GetAnnotations()[constants.SpanIDAnnotation]; ok {
											if spanIDValue, err := trace.SpanIDFromHex(spanID); err == nil {
												spanContext = trace.NewSpanContext(trace.SpanContextConfig{
													TraceID:    traceIDValue,
													SpanID:     spanIDValue,
													Remote:     true,
													TraceFlags: trace.FlagsSampled,
												})
											} else {
												spanContext = trace.NewSpanContext(trace.SpanContextConfig{
													TraceID:    traceIDValue,
													Remote:     true,
													TraceFlags: trace.FlagsSampled,
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
				}
			}
		}
	}
	// check for linked spans
	linkedSpans := sliceFromLinkedSpans(linkedSpansArray)

	// Create a new span
	ctx, span = tracer.Start(ctx, operationName, trace.WithLinks(linkedSpans...))
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

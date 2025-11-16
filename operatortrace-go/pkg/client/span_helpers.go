// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// pkg/client/span_helpers.go

package client

import (
	"context"
	"time"

	"github.com/Azure/operatortrace/operatortrace-go/pkg/tracecontext"
	"github.com/Azure/operatortrace/operatortrace-go/pkg/types"
	"github.com/go-logr/logr"
	"go.opentelemetry.io/otel/trace"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// sliceFromLinkedSpans converts a fixed array of LinkedSpan to OTEL links.
func sliceFromLinkedSpans(linkedSpans [10]types.LinkedSpan) []trace.Link {
	links := make([]trace.Link, 0, len(linkedSpans))
	for _, linkedSpan := range linkedSpans {
		if linkedSpan.TraceID == "" || linkedSpan.SpanID == "" {
			continue
		}
		traceID, err := trace.TraceIDFromHex(linkedSpan.TraceID)
		if err != nil {
			continue
		}
		spanID, err := trace.SpanIDFromHex(linkedSpan.SpanID)
		if err != nil {
			continue
		}
		links = append(links, trace.Link{SpanContext: trace.NewSpanContext(trace.SpanContextConfig{
			TraceID: traceID,
			SpanID:  spanID,
			Remote:  true,
		})})
	}
	return links
}

// startSpanFromContext starts a new span from the context and attaches trace information to the object.
func startSpanFromContext(ctx context.Context, logger logr.Logger, tracer trace.Tracer, obj client.Object, scheme *runtime.Scheme, opts Options, operationName string, linkedSpansArray [10]types.LinkedSpan, spanOpts ...trace.SpanStartOption) (context.Context, trace.Span) {
	span := trace.SpanFromContext(ctx)
	if span.SpanContext().IsValid() {
		return tracer.Start(ctx, operationName, spanOpts...)
	}

	var (
		incomingLink *trace.Link
		applied      bool
	)

	if obj != nil {
		if storedCtx, ok := extractTraceContextFromAnnotations(obj.GetAnnotations(), opts); ok && !traceContextExpired(storedCtx.Timestamp, opts) {
			ctx, incomingLink = applyStoredTraceContext(ctx, storedCtx, opts, incomingLink)
			applied = true
		}
		if !applied {
			if storedCtx, ok := extractTraceContextFromConditions(obj, scheme); ok && !traceContextExpired(storedCtx.Timestamp, opts) {
				ctx, incomingLink = applyStoredTraceContext(ctx, storedCtx, opts, incomingLink)
			}
		}
	}

	links := sliceFromLinkedSpans(linkedSpansArray)
	if incomingLink != nil {
		links = append(links, *incomingLink)
	}
	if len(links) > 0 {
		spanOpts = append(spanOpts, trace.WithLinks(links...))
	}

	return tracer.Start(ctx, operationName, spanOpts...)
}

func startSpanFromContextGeneric(ctx context.Context, logger logr.Logger, tracer trace.Tracer, operationName string) (context.Context, trace.Span) {
	span := trace.SpanFromContext(ctx)
	if span.SpanContext().IsValid() {
		spanContext := trace.NewSpanContext(trace.SpanContextConfig{
			TraceID:    span.SpanContext().TraceID(),
			SpanID:     span.SpanContext().SpanID(),
			TraceFlags: span.SpanContext().TraceFlags(),
			Remote:     false,
		})
		ctx = trace.ContextWithRemoteSpanContext(ctx, spanContext)
		ctx, span = tracer.Start(ctx, operationName)
		return trace.ContextWithSpan(ctx, span), span
	}

	ctx, span = tracer.Start(ctx, operationName)
	return ctx, span
}

func applyStoredTraceContext(ctx context.Context, stored storedTraceContext, opts Options, incomingLink *trace.Link) (context.Context, *trace.Link) {
	if stored.TraceParent == "" {
		return ctx, incomingLink
	}
	spanContext, err := tracecontext.SpanContextFromTraceData(stored.TraceParent, stored.TraceState)
	if err != nil {
		return ctx, incomingLink
	}

	relationship := stored.Relationship
	if relationship == "" {
		relationship = opts.IncomingTraceRelationship
	}

	if relationship == TraceParentRelationshipParent {
		ctx = trace.ContextWithRemoteSpanContext(ctx, spanContext)
		return ctx, incomingLink
	}
	incomingLink = &trace.Link{SpanContext: spanContext}
	return ctx, incomingLink
}

func extractTraceContextFromConditions(obj client.Object, scheme *runtime.Scheme) (storedTraceContext, bool) {
	traceID, err := getConditionMessage("TraceID", obj, scheme)
	if err != nil || traceID == "" {
		return storedTraceContext{}, false
	}
	spanID, err := getConditionMessage("SpanID", obj, scheme)
	if err != nil || spanID == "" {
		return storedTraceContext{}, false
	}
	traceParent, err := tracecontext.TraceParentFromIDs(traceID, spanID)
	if err != nil {
		return storedTraceContext{}, false
	}
	var timestamp time.Time
	if ts, err := getConditionTime("TraceID", obj, scheme); err == nil {
		timestamp = ts.Time
	}
	return storedTraceContext{
		TraceParent:  traceParent,
		Timestamp:    timestamp,
		Relationship: TraceParentRelationshipParent,
	}, true
}

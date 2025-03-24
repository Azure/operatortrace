// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package helpers

import (
	"context"

	"go.opentelemetry.io/otel/trace"
)

func StartSpan(ctx context.Context, tracer trace.Tracer, operationName string) (context.Context, trace.Span) {
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

	// If there is no span in the context, create a new one
	ctx, span = tracer.Start(ctx, operationName)
	return ctx, span
}

// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package helpers

import (
	"context"

	"go.opentelemetry.io/otel/trace"
)

func StartSpan(ctx context.Context, tracer trace.Tracer, operationName string, spanOpts ...trace.SpanStartOption) (context.Context, trace.Span) {
	span := trace.SpanFromContext(ctx)
	if span.SpanContext().IsValid() {
		ctx, span = tracer.Start(ctx, operationName, spanOpts...)
		return ctx, span
	}

	// If there is no span in the context, create a new one
	ctx, span = tracer.Start(ctx, operationName, spanOpts...)
	return ctx, span
}

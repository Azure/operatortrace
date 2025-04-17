// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package client

import (
	"context"

	"go.opentelemetry.io/otel/trace"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// TracingClient extends client.Client with tracing functionality
type TracingClient interface {
	client.Client
	trace.Tracer

	StartTrace(ctx context.Context, key *client.ObjectKey, obj client.Object, opts ...client.GetOption) (context.Context, trace.Span, error)
	EndTrace(ctx context.Context, obj client.Object, opts ...client.PatchOption) (client.Object, error)
	StartSpan(ctx context.Context, operationName string) (context.Context, trace.Span)
	EmbedTraceIDInNamespacedName(key *client.ObjectKey, obj client.Object) error
}

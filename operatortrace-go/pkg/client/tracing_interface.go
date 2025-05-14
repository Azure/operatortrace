// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// pkg/client/tracing_interface.go

package client

import (
	"context"

	tracingtypes "github.com/Azure/operatortrace/operatortrace-go/pkg/types"
	"go.opentelemetry.io/otel/trace"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// TracingClient extends client.Client with tracing functionality
type TracingClient interface {
	client.Client
	trace.Tracer

	StartTrace(ctx context.Context, requestWithTraceID *tracingtypes.RequestWithTraceID, obj client.Object, opts ...client.GetOption) (context.Context, trace.Span, error)
	EndTrace(ctx context.Context, obj client.Object, opts ...client.PatchOption) (client.Object, error)
	StartSpan(ctx context.Context, operationName string) (context.Context, trace.Span)
	EmbedTraceIDInRequest(requestWithTraceID *tracingtypes.RequestWithTraceID, obj client.Object) error
}

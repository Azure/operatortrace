// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// pkg/client/annotations.go

package client

import (
	"context"
	"time"

	constants "github.com/Azure/operatortrace/operatortrace-go/pkg/constants"
	tracingtypes "github.com/Azure/operatortrace/operatortrace-go/pkg/types"
	"github.com/samber/lo"
	"go.opentelemetry.io/otel/trace"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// addTraceIDAnnotation adds the traceID as an annotation to the object
func addTraceIDAnnotation(ctx context.Context, obj client.Object) {
	span := trace.SpanFromContext(ctx)
	traceID := span.SpanContext().TraceID().String()
	if traceID != "" {
		if obj.GetAnnotations() == nil {
			obj.SetAnnotations(map[string]string{})
		}
		annotations := obj.GetAnnotations()
		annotations[constants.TraceIDAnnotation] = traceID
		// This gets reset when the object is updated so if the object is constantly being updated, there could be a long running traceID
		annotations[constants.TraceIDTimeAnnotation] = time.Now().Format(time.RFC3339)
		obj.SetAnnotations(annotations)
	}
	spanID := span.SpanContext().SpanID().String()
	if spanID != "" {
		if obj.GetAnnotations() == nil {
			obj.SetAnnotations(map[string]string{})
		}
		annotations := obj.GetAnnotations()
		annotations[constants.SpanIDAnnotation] = spanID
		obj.SetAnnotations(annotations)
	}
}

// overrideTraceIDFromNamespacedName overrides the traceID and spanID annotations using the value from the requestWithTraceID this is used to track
// the traceID and spanID of the request that created or modified the object.
func overrideTraceIDFromNamespacedName(requestWithTraceID tracingtypes.RequestWithTraceID, obj client.Object) error {
	if lo.IsNil(requestWithTraceID.TraceID) || lo.IsNil(requestWithTraceID.SpanID) {
		return nil
	}

	if requestWithTraceID.TraceID == "" || requestWithTraceID.SpanID == "" {
		return nil
	}

	if obj.GetAnnotations() == nil {
		obj.SetAnnotations(map[string]string{})
	}
	annotations := obj.GetAnnotations()
	annotations[constants.TraceIDAnnotation] = requestWithTraceID.TraceID
	annotations[constants.SpanIDAnnotation] = requestWithTraceID.SpanID
	annotations[constants.TraceIDTimeAnnotation] = time.Now().Format(time.RFC3339)
	obj.SetAnnotations(annotations)
	return nil
}

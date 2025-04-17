// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// pkg/client/annotations.go

package client

import (
	"context"

	constants "github.com/Azure/operatortrace/operatortrace-go/pkg/constants"
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

// if the key.Name looks like this: f620f5cad0af940c294f980c5366a6a1;45f359cdc1c8ab06;Configmap;pod-configmap01;default-pod
// then we can extract the traceID and spanID from the key.Name
// and override the traceID and spanID in the object annotations
func overrideTraceIDFromNamespacedName(key client.ObjectKey, obj client.Object) error {
	embedTraceID := &EmbedTraceID{}
	if err := embedTraceID.FromString(key.Name); err != nil {
		return nil
	}

	if obj.GetAnnotations() == nil {
		obj.SetAnnotations(map[string]string{})
	}
	annotations := obj.GetAnnotations()
	annotations[constants.TraceIDAnnotation] = embedTraceID.TraceID
	annotations[constants.SpanIDAnnotation] = embedTraceID.SpanID
	obj.SetAnnotations(annotations)
	return nil
}

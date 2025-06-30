// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// pkg/client/generic_client_test.go

package client

import (
	"context"
	"testing"
	"time"

	"github.com/Azure/operatortrace/operatortrace-go/pkg/constants"
	"github.com/go-logr/logr"
	"github.com/go-logr/logr/testr"
	"github.com/stretchr/testify/assert"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

func initGenericTracer() trace.Tracer {
	exporter, err := stdouttrace.New(stdouttrace.WithPrettyPrint())
	if err != nil {
		panic(err)
	}
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
	)
	otel.SetTracerProvider(tp)
	return tp.Tracer("operatortrace-generic")
}

func TestNewGenericClient(t *testing.T) {
	tracer := initGenericTracer()
	logger := logr.Discard()
	client := NewGenericClient(tracer, logger)
	assert.NotNil(t, client)
}

func TestGenericClientStartTraceAndEndTrace(t *testing.T) {
	tracer := initGenericTracer()
	logger := testr.New(t)
	scheme := runtime.NewScheme()
	corev1.AddToScheme(scheme)
	client := NewGenericClient(tracer, logger, scheme)

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pod",
			Namespace: "default",
		},
	}
	ctx := context.Background()
	ctx, span, err := client.StartTrace(ctx, pod)
	defer span.End()
	assert.NoError(t, err)
	traceID := span.SpanContext().TraceID().String()
	spanID := span.SpanContext().SpanID().String()

	// Set annotations manually to simulate SetSpan
	annotations := pod.GetAnnotations()
	if annotations == nil {
		annotations = make(map[string]string)
	}
	annotations[constants.TraceIDAnnotation] = traceID
	annotations[constants.SpanIDAnnotation] = spanID
	annotations[constants.TraceIDTimeAnnotation] = time.Now().Format(time.RFC3339)
	pod.SetAnnotations(annotations)

	err = client.EndTrace(ctx, pod)
	assert.NoError(t, err)
	annotations = pod.GetAnnotations()
	assert.Empty(t, annotations[constants.TraceIDAnnotation])
	assert.Empty(t, annotations[constants.SpanIDAnnotation])
	assert.Empty(t, annotations[constants.TraceIDTimeAnnotation])
}

func TestGenericClientStartSpan(t *testing.T) {
	tracer := initGenericTracer()
	logger := logr.Discard()
	scheme := runtime.NewScheme()
	client := NewGenericClient(tracer, logger, scheme)
	ctx := context.Background()
	_, span := client.StartSpan(ctx, "TestOperation")
	defer span.End()
	assert.NotNil(t, span)
}

func TestGenericClientSetSpan(t *testing.T) {
	tracer := initGenericTracer()
	logger := logr.Discard()
	scheme := runtime.NewScheme()
	corev1.AddToScheme(scheme)
	client := NewGenericClient(tracer, logger, scheme)
	ctx := context.Background()
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pod",
			Namespace: "default",
		},
	}
	_, span := client.SetSpan(ctx, pod)
	defer span.End()
	annotations := pod.GetAnnotations()
	assert.NotEmpty(t, annotations[constants.TraceIDAnnotation])
	assert.NotEmpty(t, annotations[constants.SpanIDAnnotation])
	assert.NotEmpty(t, annotations[constants.TraceIDTimeAnnotation])
}

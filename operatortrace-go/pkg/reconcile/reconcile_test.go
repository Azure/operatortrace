// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// pkg/reconcile/reconcile.go

package reconcile

import (
	"context"
	"errors"
	"testing"

	tracingclient "github.com/Azure/operatortrace/operatortrace-go/pkg/client"
	"github.com/Azure/operatortrace/operatortrace-go/pkg/constants"
	tracingtypes "github.com/Azure/operatortrace/operatortrace-go/pkg/types"
	"github.com/go-logr/logr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	ctrlreconcile "sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// mockObjectReconciler is a test reconciler that tracks calls
type mockObjectReconciler struct {
	reconcileCalled bool
	reconcileError  error
	reconcileResult ctrlreconcile.Result
}

func (m *mockObjectReconciler) Reconcile(ctx context.Context, obj *corev1.Pod) (ctrlreconcile.Result, error) {
	m.reconcileCalled = true
	return m.reconcileResult, m.reconcileError
}

func initTestTracer() trace.Tracer {
	exporter, err := stdouttrace.New(stdouttrace.WithPrettyPrint())
	if err != nil {
		panic(err)
	}
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
	)
	otel.SetTracerProvider(tp)
	return tp.Tracer("operatortrace-test")
}

func setupTestClient(objects ...ctrlclient.Object) (tracingclient.TracingClient, *runtime.Scheme) {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)

	k8sClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(objects...).
		Build()

	tracer := initTestTracer()
	logger := logr.Discard()

	return tracingclient.NewTracingClient(k8sClient, k8sClient, tracer, logger, scheme), scheme
}

func TestTracingOptions(t *testing.T) {
	opts := TracingOptions()

	assert.NotNil(t, opts)
	assert.NotNil(t, opts.NewQueue)

	// Test that the queue factory works
	queue := opts.NewQueue("test-queue", nil)
	assert.NotNil(t, queue)
}

func TestNewReconcilerBuilder(t *testing.T) {
	client, _ := setupTestClient()
	mockRec := &mockObjectReconciler{}

	builder := NewReconcilerBuilder(client, mockRec)

	assert.NotNil(t, builder)
	assert.Equal(t, client, builder.client)
	assert.Equal(t, mockRec, builder.objReconciler)
	assert.False(t, builder.disableEndTrace, "disableEndTrace should default to false")
}

func TestReconcilerBuilder_WithDisableEndTrace(t *testing.T) {
	client, _ := setupTestClient()
	mockRec := &mockObjectReconciler{}

	builder := NewReconcilerBuilder(client, mockRec).
		WithDisableEndTrace()

	assert.True(t, builder.disableEndTrace)

	// Test chaining returns the same builder
	builder2 := builder.WithDisableEndTrace()
	assert.Equal(t, builder, builder2)
}

func TestReconcilerBuilder_Build(t *testing.T) {
	client, _ := setupTestClient()
	mockRec := &mockObjectReconciler{}

	reconciler := NewReconcilerBuilder(client, mockRec).Build()

	assert.NotNil(t, reconciler)

	// Verify it returns the correct type
	_, ok := reconciler.(*objectReconcilerAdapter[*corev1.Pod])
	assert.True(t, ok)
}

func TestReconcilerBuilder_BuildWithOptions(t *testing.T) {
	client, _ := setupTestClient()
	mockRec := &mockObjectReconciler{}

	reconciler := NewReconcilerBuilder(client, mockRec).
		WithDisableEndTrace().
		Build()

	assert.NotNil(t, reconciler)

	adapter, ok := reconciler.(*objectReconcilerAdapter[*corev1.Pod])
	assert.True(t, ok)
	assert.True(t, adapter.disableEndTrace)
}

func TestAsTracingReconciler(t *testing.T) {
	client, _ := setupTestClient()
	mockRec := &mockObjectReconciler{}

	reconciler := AsTracingReconciler(client, mockRec)

	assert.NotNil(t, reconciler)

	adapter, ok := reconciler.(*objectReconcilerAdapter[*corev1.Pod])
	assert.True(t, ok)
	assert.False(t, adapter.disableEndTrace, "should use default (false) when using AsTracingReconciler")
}

func TestObjectReconcilerAdapter_Reconcile_Success(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pod",
			Namespace: "default",
			Annotations: map[string]string{
				constants.TraceIDAnnotation: "test-trace-id",
				constants.SpanIDAnnotation:  "test-span-id",
			},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{Name: "test-container", Image: "test-image"},
			},
		},
	}

	client, _ := setupTestClient(pod)
	mockRec := &mockObjectReconciler{
		reconcileResult: ctrlreconcile.Result{Requeue: false},
		reconcileError:  nil,
	}

	reconciler := AsTracingReconciler(client, mockRec)

	req := tracingtypes.RequestWithTraceID{
		Request: ctrlreconcile.Request{
			NamespacedName: types.NamespacedName{
				Name:      "test-pod",
				Namespace: "default",
			},
		},
	}

	ctx := context.Background()
	result, err := reconciler.Reconcile(ctx, req)

	assert.NoError(t, err)
	assert.False(t, result.Requeue)
	assert.True(t, mockRec.reconcileCalled, "inner reconciler should have been called")
}

func TestObjectReconcilerAdapter_Reconcile_WithError(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pod",
			Namespace: "default",
			Annotations: map[string]string{
				constants.TraceIDAnnotation: "test-trace-id",
				constants.SpanIDAnnotation:  "test-span-id",
			},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{Name: "test-container", Image: "test-image"},
			},
		},
	}

	client, _ := setupTestClient(pod)
	expectedErr := errors.New("reconcile failed")
	mockRec := &mockObjectReconciler{
		reconcileResult: ctrlreconcile.Result{},
		reconcileError:  expectedErr,
	}

	reconciler := AsTracingReconciler(client, mockRec)

	req := tracingtypes.RequestWithTraceID{
		Request: ctrlreconcile.Request{
			NamespacedName: types.NamespacedName{
				Name:      "test-pod",
				Namespace: "default",
			},
		},
	}

	ctx := context.Background()
	result, err := reconciler.Reconcile(ctx, req)

	assert.Error(t, err)
	assert.Equal(t, expectedErr, err)
	assert.Equal(t, ctrlreconcile.Result{}, result)
	assert.True(t, mockRec.reconcileCalled)
}

func TestObjectReconcilerAdapter_Reconcile_ObjectNotFound(t *testing.T) {
	// Don't create the pod - it doesn't exist
	client, _ := setupTestClient()
	mockRec := &mockObjectReconciler{}

	reconciler := AsTracingReconciler(client, mockRec)

	req := tracingtypes.RequestWithTraceID{
		Request: ctrlreconcile.Request{
			NamespacedName: types.NamespacedName{
				Name:      "nonexistent-pod",
				Namespace: "default",
			},
		},
	}

	ctx := context.Background()
	result, err := reconciler.Reconcile(ctx, req)

	// Should ignore NotFound errors
	assert.NoError(t, err)
	assert.Equal(t, ctrlreconcile.Result{}, result)
	assert.False(t, mockRec.reconcileCalled, "inner reconciler should not be called if object not found")
}

func TestObjectReconcilerAdapter_Reconcile_WithRequeue(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pod",
			Namespace: "default",
			Annotations: map[string]string{
				constants.TraceIDAnnotation: "test-trace-id",
				constants.SpanIDAnnotation:  "test-span-id",
			},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{Name: "test-container", Image: "test-image"},
			},
		},
	}

	client, _ := setupTestClient(pod)
	mockRec := &mockObjectReconciler{
		reconcileResult: ctrlreconcile.Result{Requeue: true},
		reconcileError:  nil,
	}

	reconciler := AsTracingReconciler(client, mockRec)

	req := tracingtypes.RequestWithTraceID{
		Request: ctrlreconcile.Request{
			NamespacedName: types.NamespacedName{
				Name:      "test-pod",
				Namespace: "default",
			},
		},
	}

	ctx := context.Background()
	result, err := reconciler.Reconcile(ctx, req)

	assert.NoError(t, err)
	assert.True(t, result.Requeue)
	assert.True(t, mockRec.reconcileCalled)
}

func TestObjectReconcilerAdapter_Reconcile_DisableEndTrace(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pod",
			Namespace: "default",
			Annotations: map[string]string{
				constants.TraceIDAnnotation: "test-trace-id",
				constants.SpanIDAnnotation:  "test-span-id",
			},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{Name: "test-container", Image: "test-image"},
			},
		},
	}

	client, _ := setupTestClient(pod)
	mockRec := &mockObjectReconciler{
		reconcileResult: ctrlreconcile.Result{},
		reconcileError:  nil,
	}

	// Build with disableEndTrace
	reconciler := NewReconcilerBuilder(client, mockRec).
		WithDisableEndTrace().
		Build()

	req := tracingtypes.RequestWithTraceID{
		Request: ctrlreconcile.Request{
			NamespacedName: types.NamespacedName{
				Name:      "test-pod",
				Namespace: "default",
			},
		},
	}

	ctx := context.Background()
	result, err := reconciler.Reconcile(ctx, req)

	assert.NoError(t, err)
	assert.False(t, result.Requeue)
	assert.True(t, mockRec.reconcileCalled)

	// Verify the pod still has trace annotations (EndTrace wasn't called)
	var updatedPod corev1.Pod
	err = client.Get(ctx, types.NamespacedName{Name: "test-pod", Namespace: "default"}, &updatedPod)
	require.NoError(t, err)
	assert.Equal(t, "test-trace-id", updatedPod.Annotations[constants.TraceIDAnnotation])
	assert.Equal(t, "test-span-id", updatedPod.Annotations[constants.SpanIDAnnotation])
}

func TestObjectReconcilerAdapter_Reconcile_WithLinkedSpans(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pod",
			Namespace: "default",
			Annotations: map[string]string{
				constants.TraceIDAnnotation: "test-trace-id",
				constants.SpanIDAnnotation:  "test-span-id",
			},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{Name: "test-container", Image: "test-image"},
			},
		},
	}

	client, _ := setupTestClient(pod)
	mockRec := &mockObjectReconciler{
		reconcileResult: ctrlreconcile.Result{},
		reconcileError:  nil,
	}

	reconciler := AsTracingReconciler(client, mockRec)

	linkedSpans := [10]tracingtypes.LinkedSpan{
		{TraceID: "parent-trace-1", SpanID: "parent-span-1"},
		{TraceID: "parent-trace-2", SpanID: "parent-span-2"},
	}

	req := tracingtypes.RequestWithTraceID{
		Request: ctrlreconcile.Request{
			NamespacedName: types.NamespacedName{
				Name:      "test-pod",
				Namespace: "default",
			},
		},
		Parent: tracingtypes.RequestParent{
			TraceID: "parent-trace",
			SpanID:  "parent-span",
			Name:    "parent-object",
			Kind:    "Deployment",
		},
		LinkedSpans:     linkedSpans,
		LinkedSpanCount: 2,
	}

	ctx := context.Background()
	result, err := reconciler.Reconcile(ctx, req)

	assert.NoError(t, err)
	assert.False(t, result.Requeue)
	assert.True(t, mockRec.reconcileCalled)
}

func TestObjectReconcilerAdapter_Reconcile_WithParentInfo(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pod",
			Namespace: "default",
			Annotations: map[string]string{
				constants.TraceIDAnnotation: "test-trace-id",
				constants.SpanIDAnnotation:  "test-span-id",
			},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{Name: "test-container", Image: "test-image"},
			},
		},
	}

	client, _ := setupTestClient(pod)
	mockRec := &mockObjectReconciler{
		reconcileResult: ctrlreconcile.Result{},
		reconcileError:  nil,
	}

	reconciler := AsTracingReconciler(client, mockRec)

	req := tracingtypes.RequestWithTraceID{
		Request: ctrlreconcile.Request{
			NamespacedName: types.NamespacedName{
				Name:      "test-pod",
				Namespace: "default",
			},
		},
		Parent: tracingtypes.RequestParent{
			TraceID:   "parent-trace-id",
			SpanID:    "parent-span-id",
			Name:      "parent-deployment",
			Kind:      "Deployment",
			EventKind: "Update",
		},
	}

	ctx := context.Background()
	result, err := reconciler.Reconcile(ctx, req)

	assert.NoError(t, err)
	assert.False(t, result.Requeue)
	assert.True(t, mockRec.reconcileCalled)
}

func TestReconcilerBuilder_MultipleOptions(t *testing.T) {
	client, _ := setupTestClient()
	mockRec := &mockObjectReconciler{}

	// Test chaining multiple options
	builder := NewReconcilerBuilder(client, mockRec).
		WithDisableEndTrace()

	reconciler := builder.Build()

	adapter, ok := reconciler.(*objectReconcilerAdapter[*corev1.Pod])
	require.True(t, ok)
	assert.True(t, adapter.disableEndTrace)
	assert.Equal(t, client, adapter.client)
	assert.Equal(t, mockRec, adapter.objReconciler)
}

func TestReconcilerBuilder_FluentAPI(t *testing.T) {
	client, _ := setupTestClient()
	mockRec := &mockObjectReconciler{}

	// Test that the fluent API works correctly
	reconciler := NewReconcilerBuilder(client, mockRec).
		WithDisableEndTrace().
		WithDisableEndTrace(). // Should be idempotent
		Build()

	assert.NotNil(t, reconciler)

	adapter, ok := reconciler.(*objectReconcilerAdapter[*corev1.Pod])
	require.True(t, ok)
	assert.True(t, adapter.disableEndTrace)
}

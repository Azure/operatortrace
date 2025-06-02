// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// pkg/client/tracing_client_test.go

package client

import (
	"context"
	"testing"

	"github.com/Azure/operatortrace/operatortrace-go/pkg/constants"
	tracingtypes "github.com/Azure/operatortrace/operatortrace-go/pkg/types"
	"github.com/go-logr/logr"
	"github.com/go-logr/logr/testr"
	"github.com/stretchr/testify/assert"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	ctrlreconcile "sigs.k8s.io/controller-runtime/pkg/reconcile"
)

func initTracer() trace.Tracer {
	exporter, err := stdouttrace.New(stdouttrace.WithPrettyPrint())
	if err != nil {
		panic(err)
	}
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
	)
	otel.SetTracerProvider(tp)

	return tp.Tracer("operatortrace")
}

func TestNewTracingClient(t *testing.T) {
	// Create a fake Kubernetes client
	k8sClient := fake.NewClientBuilder().WithObjects(&corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pre-test-pod",
			Namespace: "default",
		},
	}).Build()

	// Create a real tracer
	tracer := initTracer()

	// Create a logger
	logger := logr.Discard()
	// Initialize the TracingClient
	tracingClient := NewTracingClient(k8sClient, k8sClient, tracer, logger)

	// Check if the client is not nil
	assert.NotNil(t, tracingClient)
}

func TestEmbedTraceIDInRequest(t *testing.T) {
	// Set up the tracingClient
	fakeClient := fake.NewClientBuilder().WithObjects().Build()
	scheme := runtime.NewScheme()
	corev1.AddToScheme(scheme)

	tracingClient := &tracingClient{
		Logger: logr.Discard(),
		scheme: scheme,
		Client: fakeClient,
	}

	// Mock object with traceID and spanID annotations
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pod",
			Namespace: "default",
			Annotations: map[string]string{
				constants.TraceIDAnnotation: "1234",
				constants.SpanIDAnnotation:  "5678",
			},
		},
	}

	// Set up a trace id request
	request := tracingtypes.RequestWithTraceID{
		Request: ctrlreconcile.Request{
			NamespacedName: types.NamespacedName{
				Name:      "test-deployment",
				Namespace: "default",
			},
		},
	}

	// Call the function
	err := tracingClient.EmbedTraceIDInRequest(&request, pod)

	// Assert no error
	assert.NoError(t, err)

	// Assert the object has been updated correctly
	assert.Equal(t, "1234", request.Parent.TraceID)
	assert.Equal(t, "5678", request.Parent.SpanID)
	assert.Equal(t, "test-pod", request.Parent.Name)
}

func TestAutomaticAnnotationManagement(t *testing.T) {
	// Create a fake Kubernetes client
	k8sClient := fake.NewClientBuilder().WithObjects(&corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pre-test-pod",
			Namespace: "default",
		},
	}).Build()

	// Create a real tracer
	tracer := initTracer()

	// Create a logger
	logger := logr.Discard()
	// Initialize the TracingClient
	tracingClient := NewTracingClient(k8sClient, k8sClient, tracer, logger)

	ctx := context.Background()

	// Create a spanId since no GET is being called to initialize the span
	request := ClientObjectToRequestWithTraceID(&client.ObjectKey{Name: "pre-test-pod", Namespace: "default"})
	ctx, span, err := tracingClient.StartTrace(ctx, &request, &corev1.Pod{})
	defer span.End()

	assert.NoError(t, err)
	traceID := span.SpanContext().TraceID().String()
	spanID := span.SpanContext().SpanID().String()

	// Create a Pod with an annotation
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pod",
			Namespace: "default",
		},
	}

	// Save the Pod
	err = tracingClient.Create(ctx, pod)
	assert.NoError(t, err)

	// Retrieve the Pod and check the annotation
	retrievedPod := &corev1.Pod{}
	err = tracingClient.Get(ctx, client.ObjectKey{Name: "test-pod", Namespace: "default"}, retrievedPod)
	assert.NoError(t, err)
	assert.Equal(t, traceID, retrievedPod.Annotations[constants.TraceIDAnnotation])
	assert.NotEqual(t, spanID, retrievedPod.Annotations[constants.SpanIDAnnotation])
	assert.Equal(t, len(spanID), len(retrievedPod.Annotations[constants.SpanIDAnnotation]))
}

func TestPassingTraceIdInNamespacedName(t *testing.T) {
	// Create a fake Kubernetes client
	k8sClient := fake.NewClientBuilder().WithObjects(&corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pre-test-pod",
			Namespace: "default",
		},
	}).Build()

	// Create a real tracer
	tracer := initTracer()

	// Create a logger
	logger := logr.Discard()
	// Initialize the TracingClient
	tracingClient := NewTracingClient(k8sClient, k8sClient, tracer, logger)

	ctx := context.Background()

	// key := client.ObjectKey{Name: "f620f5cad0af940c294f980c5366a6a1;45f359cdc1c8ab06;Configmap;configmap-10;pre-test-pod", Namespace: "default"}
	request := ClientObjectToRequestWithTraceID(&client.ObjectKey{Name: "pre-test-pod", Namespace: "default"})
	request.Parent.TraceID = "f620f5cad0af940c294f980c5366a6a1"
	request.Parent.SpanID = "45f359cdc1c8ab06"
	request.Parent.Kind = "Configmap"
	request.Parent.Name = "configmap-10"

	// Create a spanId since no GET is being called to initialize the span
	_, span, err := tracingClient.StartTrace(ctx, &request, &corev1.Pod{})
	defer span.End()

	assert.NoError(t, err)
	traceID := span.SpanContext().TraceID().String()

	assert.Equal(t, "f620f5cad0af940c294f980c5366a6a1", traceID)
	assert.Equal(t, "pre-test-pod", request.Name)
}

func TestChainReactionTracing(t *testing.T) {
	// Create a fake Kubernetes client
	k8sClient := fake.NewClientBuilder().WithObjects(&corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pre-test-pod",
			Namespace: "default",
		},
	}).Build()

	// Create a real tracer
	tracer := initTracer()

	// Create a logger
	logger := logr.Discard()

	// Initialize the TracingClient
	tracingClient := NewTracingClient(k8sClient, k8sClient, tracer, logger)

	// Create an initial Pod
	initialPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "initial-pod",
			Namespace: "default",
		},
	}

	ctx := context.Background()

	// Create a spanId since no GET is being called to initialize the span
	request := ClientObjectToRequestWithTraceID(&client.ObjectKey{Name: "pre-test-pod", Namespace: "default"})
	ctx, span, err := tracingClient.StartTrace(ctx, &request, &corev1.Pod{})
	defer span.End()

	assert.NoError(t, err)
	traceID := span.SpanContext().TraceID().String()
	spanID := span.SpanContext().SpanID().String()

	// Save the initial Pod
	err = tracingClient.Create(ctx, initialPod)
	assert.NoError(t, err)

	// Create a new TracingClient to simulate a fresh client
	newK8sClient := fake.NewClientBuilder().WithObjects(initialPod).Build()
	newTracingClient := NewTracingClient(newK8sClient, newK8sClient, tracer, logger)

	// Retrieve the initial Pod to get the trace ID
	retrievedInitialPod := &corev1.Pod{}
	err = newTracingClient.Get(ctx, client.ObjectKey{Name: "initial-pod", Namespace: "default"}, retrievedInitialPod)
	assert.NoError(t, err)

	// Extract the trace ID from the retrieved initial pod annotations
	savedtraceID := retrievedInitialPod.Annotations[constants.TraceIDAnnotation]
	savedSpanID := retrievedInitialPod.Annotations[constants.SpanIDAnnotation]
	assert.Equal(t, traceID, savedtraceID)
	assert.NotEqual(t, spanID, savedSpanID)

	t.Run("", func(t *testing.T) {
		patchPod := client.MergeFrom(retrievedInitialPod.DeepCopy())
		retrievedInitialPod.Status.Phase = corev1.PodRunning
		err := newTracingClient.Status().Patch(ctx, retrievedInitialPod, patchPod)
		assert.NoError(t, err)
		assert.Equal(t, retrievedInitialPod.Status.Phase, corev1.PodRunning)
		retrievedPatchedPod := &corev1.Pod{}
		err = newTracingClient.Get(ctx, client.ObjectKey{Name: "initial-pod", Namespace: "default"}, retrievedPatchedPod)
		assert.NoError(t, err)
		traceid, _ := getConditionMessage("TraceID", retrievedPatchedPod, k8sClient.Scheme())
		assert.Equal(t, savedtraceID, traceid)
		//Annotations will not be patched with Status.Patch
		assert.Equal(t, savedSpanID, retrievedPatchedPod.Annotations[constants.SpanIDAnnotation])
	})
}

func TestUpdateWithTracing(t *testing.T) {
	// Create a fake Kubernetes client
	k8sClient := fake.NewClientBuilder().WithObjects(&corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pre-test-pod",
			Namespace: "default",
		},
	}).Build()

	// Create a real tracer
	tracer := initTracer()

	// Create a logger
	logger := logr.Discard()

	// Initialize the TracingClient
	tracingClient := NewTracingClient(k8sClient, k8sClient, tracer, logger)

	ctx := context.Background()
	// Create a spanId since no GET is being called to initialize the span
	request := ClientObjectToRequestWithTraceID(&client.ObjectKey{Name: "pre-test-pod", Namespace: "default"})
	ctx, span, err := tracingClient.StartTrace(ctx, &request, &corev1.Pod{})
	defer span.End()

	assert.NoError(t, err)
	traceID := span.SpanContext().TraceID().String()
	spanID := span.SpanContext().SpanID().String()

	// Create a Pod with an annotation
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pod",
			Namespace: "default",
		},
	}

	// Save the Pod
	err = tracingClient.Create(ctx, pod)
	assert.NoError(t, err)

	// Update the Pod
	pod.Labels = map[string]string{"updated": "true"}
	err = tracingClient.Update(ctx, pod)
	assert.NoError(t, err)

	// Retrieve the Pod and check the annotation
	retrievedPod := &corev1.Pod{}
	err = tracingClient.Get(ctx, client.ObjectKey{Name: "test-pod", Namespace: "default"}, retrievedPod)
	assert.NoError(t, err)
	assert.Equal(t, traceID, retrievedPod.Annotations[constants.TraceIDAnnotation])
	assert.Equal(t, "true", retrievedPod.Labels["updated"])
	assert.NotEqual(t, spanID, retrievedPod.Annotations[constants.SpanIDAnnotation])
	assert.Equal(t, len(spanID), len(retrievedPod.Annotations[constants.SpanIDAnnotation]))

	// Test status update with tracing
	t.Run("update status with tracing", func(t *testing.T) {
		pod.Status.Phase = corev1.PodRunning
		err = tracingClient.Status().Update(ctx, retrievedPod)
		assert.NoError(t, err)
		assert.Equal(t, traceID, retrievedPod.Annotations[constants.TraceIDAnnotation])
	})

	// Update without any meaningful changes
	t.Run("update without changes (should skip update)", func(t *testing.T) {
		// Fetch the current resource again to get the latest state
		currentPod := &corev1.Pod{}
		err = tracingClient.Get(ctx, client.ObjectKey{Name: "test-pod", Namespace: "default"}, currentPod)
		assert.NoError(t, err)

		// Attempt update without any changes
		err = tracingClient.Update(ctx, currentPod)
		assert.NoError(t, err)

		// Fetch pod again to check if the resourceVersion has changed
		afterUpdatePod := &corev1.Pod{}
		err = tracingClient.Get(ctx, client.ObjectKey{Name: "test-pod", Namespace: "default"}, afterUpdatePod)
		assert.NoError(t, err)

		// resourceVersion should not have changed
		assert.Equal(t, currentPod.ResourceVersion, afterUpdatePod.ResourceVersion, "ResourceVersion should not change if update was skipped")
	})

	// Second status update with no real changes, should skip
	t.Run("second status update without changes (should skip)", func(t *testing.T) {
		// Fetch the current resource again
		latestPod := &corev1.Pod{}
		err = tracingClient.Get(ctx, client.ObjectKey{Name: "test-pod", Namespace: "default"}, latestPod)
		assert.NoError(t, err)

		// Attempt status update without any changes
		patch := client.MergeFrom(latestPod.DeepCopy())
		err = tracingClient.Status().Patch(ctx, latestPod, patch)
		assert.NoError(t, err)

		// Fetch the Pod again to verify that resourceVersion hasn't incremented
		afterStatusPatchPod := &corev1.Pod{}
		err = tracingClient.Get(ctx, client.ObjectKey{Name: "test-pod", Namespace: "default"}, afterStatusPatchPod)
		assert.NoError(t, err)

		assert.Equal(t, latestPod.ResourceVersion, afterStatusPatchPod.ResourceVersion, "ResourceVersion should not change if status patch was skipped")
	})
}

func TestStartSpan(t *testing.T) {
	// Create a fake Kubernetes client
	k8sClient := fake.NewClientBuilder().WithObjects(&corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pre-test-pod",
			Namespace: "default",
		},
	}).Build()

	// Create a real tracer
	tracer := initTracer()

	// Create a logger
	logger := logr.Discard()

	// Initialize the TracingClient
	tracingClient := NewTracingClient(k8sClient, k8sClient, tracer, logger)

	ctx := context.Background()
	// Create a spanId since no GET is being called to initialize the span
	_, span := tracingClient.StartSpan(ctx, "test-span")
	defer span.End()

	traceID := span.SpanContext().TraceID().String()
	spanID := span.SpanContext().SpanID().String()

	assert.NotEmpty(t, traceID)
	assert.NotEmpty(t, spanID)
}

func TestPatchWithTracing(t *testing.T) {
	// Create a fake Kubernetes client
	k8sClient := fake.NewClientBuilder().WithObjects(&corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pre-test-pod",
			Namespace: "default",
		},
	}).Build()

	// Create a real tracer
	tracer := initTracer()

	// Create a logger
	logger := logr.Discard()

	// Initialize the TracingClient
	tracingClient := NewTracingClient(k8sClient, k8sClient, tracer, logger)

	ctx := context.Background()
	// Create a spanId since no GET is being called to initialize the span
	request := ClientObjectToRequestWithTraceID(&client.ObjectKey{Name: "pre-test-pod", Namespace: "default"})
	ctx, span, err := tracingClient.StartTrace(ctx, &request, &corev1.Pod{})
	defer span.End()

	assert.NoError(t, err)
	traceID := span.SpanContext().TraceID().String()
	spanID := span.SpanContext().SpanID().String()

	// Create a Pod with an annotation
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pod",
			Namespace: "default",
		},
	}

	// Save the Pod
	err = tracingClient.Create(ctx, pod)
	assert.NoError(t, err)

	// Patch the Pod
	podPatch := client.MergeFrom(pod.DeepCopy())
	pod.Labels = map[string]string{"updated": "true"}
	err = tracingClient.Patch(ctx, pod, podPatch)
	assert.NoError(t, err)

	// Retrieve the Pod and check the annotation
	retrievedPod := &corev1.Pod{}
	err = tracingClient.Get(ctx, client.ObjectKey{Name: "test-pod", Namespace: "default"}, retrievedPod)
	assert.NoError(t, err)
	assert.Equal(t, traceID, retrievedPod.Annotations[constants.TraceIDAnnotation])
	assert.Equal(t, "true", retrievedPod.Labels["updated"])
	assert.NotEqual(t, spanID, retrievedPod.Annotations[constants.SpanIDAnnotation])
	assert.Equal(t, len(spanID), len(retrievedPod.Annotations[constants.SpanIDAnnotation]))

	t.Run("status create with tracing", func(t *testing.T) {
		err := tracingClient.Status().Create(ctx, retrievedPod, retrievedPod)
		// fakeClient does not support Create for subresoruce Client
		// https://github.com/kubernetes-sigs/controller-runtime/blob/v0.20.3/pkg/client/fake/client.go#L1227
		assert.Error(t, err)
	})
}

func TestPatchWithTracingClientMerge(t *testing.T) {
	// Create a fake Kubernetes client
	k8sClient := fake.NewClientBuilder().WithObjects(&corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pre-test-pod",
			Namespace: "default",
		},
	}).Build()

	// Create a real tracer
	tracer := initTracer()

	// Create a logger
	logger := logr.Discard()

	// Initialize the TracingClient
	tracingClient := NewTracingClient(k8sClient, k8sClient, tracer, logger)

	ctx := context.Background()
	// Create a spanId since no GET is being called to initialize the span
	request := ClientObjectToRequestWithTraceID(&client.ObjectKey{Name: "pre-test-pod", Namespace: "default"})
	ctx, span, err := tracingClient.StartTrace(ctx, &request, &corev1.Pod{})
	defer span.End()

	assert.NoError(t, err)
	traceID := span.SpanContext().TraceID().String()
	spanID := span.SpanContext().SpanID().String()

	// Create a Pod with an annotation
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pod",
			Namespace: "default",
		},
	}

	// Save the Pod
	err = tracingClient.Create(ctx, pod)
	assert.NoError(t, err)

	// Patch the Pod
	pod.Labels = map[string]string{"updated": "true"}
	pod.SetOwnerReferences([]metav1.OwnerReference{
		{
			APIVersion: "v1",
			Kind:       "Pod",
			Name:       "test-pod",
			UID:        "1234",
		},
	})
	err = tracingClient.Patch(ctx, pod, client.Merge)
	assert.NoError(t, err)

	// Retrieve the Pod and check the annotation
	retrievedPod := &corev1.Pod{}
	err = tracingClient.Get(ctx, client.ObjectKey{Name: "test-pod", Namespace: "default"}, retrievedPod)
	assert.NoError(t, err)
	assert.Equal(t, traceID, retrievedPod.Annotations[constants.TraceIDAnnotation])
	assert.Equal(t, "true", retrievedPod.Labels["updated"])
	assert.Equal(t, "test-pod", retrievedPod.OwnerReferences[0].Name)
	assert.NotEqual(t, spanID, retrievedPod.Annotations[constants.SpanIDAnnotation])
	assert.Equal(t, len(spanID), len(retrievedPod.Annotations[constants.SpanIDAnnotation]))

	t.Run("status create with tracing", func(t *testing.T) {
		err := tracingClient.Status().Create(ctx, retrievedPod, retrievedPod)
		// fakeClient does not support Create for subresoruce Client
		// https://github.com/kubernetes-sigs/controller-runtime/blob/v0.20.3/pkg/client/fake/client.go#L1227
		assert.Error(t, err)
	})
}

func TestEndTrace(t *testing.T) {
	// Create a fake Kubernetes client
	k8sClient := fake.NewClientBuilder().WithObjects(&corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pre-test-pod",
			Namespace: "default",
		},
	}).Build()

	// Create a real tracer
	tracer := initTracer()

	// Create a logger
	logger := logr.Discard()

	// Initialize the TracingClient
	tracingClient := NewTracingClient(k8sClient, k8sClient, tracer, logger)

	ctx := context.Background()
	// Create a spanId since no GET is being called to initialize the span
	request := ClientObjectToRequestWithTraceID(&client.ObjectKey{Name: "pre-test-pod", Namespace: "default"})
	ctx, span, err := tracingClient.StartTrace(ctx, &request, &corev1.Pod{})
	defer span.End()

	assert.NoError(t, err)
	traceID := span.SpanContext().TraceID().String()
	spanID := span.SpanContext().SpanID().String()

	// Create a Pod with an annotation
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pod",
			Namespace: "default",
		},
		Status: corev1.PodStatus{
			Conditions: []corev1.PodCondition{
				{
					Type:   corev1.PodScheduled,
					Status: corev1.ConditionTrue,
				},
			},
		},
	}

	// Save the Pod
	err = tracingClient.Create(ctx, pod)
	assert.NoError(t, err)

	// Patch the Pod
	podPatch := client.MergeFrom(pod.DeepCopy())
	pod.Labels = map[string]string{"updated": "true"}
	err = tracingClient.Patch(ctx, pod, podPatch)
	assert.NoError(t, err)
	err = tracingClient.Status().Update(ctx, pod)
	assert.NoError(t, err)

	// Retrieve the Pod and check the annotation
	retrievedPod := &corev1.Pod{}
	err = tracingClient.Get(ctx, client.ObjectKey{Name: "test-pod", Namespace: "default"}, retrievedPod)
	assert.NoError(t, err)
	assert.Equal(t, traceID, retrievedPod.Annotations[constants.TraceIDAnnotation])
	assert.Equal(t, "true", retrievedPod.Labels["updated"])
	assert.NotEqual(t, spanID, retrievedPod.Annotations[constants.SpanIDAnnotation])
	assert.Equal(t, len(spanID), len(retrievedPod.Annotations[constants.SpanIDAnnotation]))

	// Test EndTrace
	_, err = tracingClient.EndTrace(ctx, retrievedPod)
	assert.NoError(t, err)
	finalPod := &corev1.Pod{}
	// Get the pod with default kubernetes client to ensure that there is no traceID and spanID
	err = k8sClient.Get(ctx, client.ObjectKey{Name: "test-pod", Namespace: "default"}, finalPod)
	assert.NoError(t, err)
	assert.Empty(t, finalPod.Annotations[constants.TraceIDAnnotation])
	assert.Empty(t, finalPod.Annotations[constants.SpanIDAnnotation])
	assert.Equal(t, 1, len(finalPod.Status.Conditions))
}

func TestEndTraceChangedAnnotation(t *testing.T) {
	// Create a fake Kubernetes client
	k8sClient := fake.NewClientBuilder().WithObjects(&corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pre-test-pod",
			Namespace: "default",
		},
	}).Build()

	// Create a real tracer
	tracer := initTracer()

	// Create a logger
	logger := logr.Discard()

	// Initialize the TracingClient
	tracingClient := NewTracingClient(k8sClient, k8sClient, tracer, logger)

	ctx := context.Background()
	// Create a spanId since no GET is being called to initialize the span
	request := ClientObjectToRequestWithTraceID(&client.ObjectKey{Name: "pre-test-pod", Namespace: "default"})
	ctx, span, err := tracingClient.StartTrace(ctx, &request, &corev1.Pod{})
	defer span.End()

	assert.NoError(t, err)
	traceID := span.SpanContext().TraceID().String()
	spanID := span.SpanContext().SpanID().String()

	// Create a Pod with an annotation
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pod",
			Namespace: "default",
		},
	}

	// Save the Pod
	err = tracingClient.Create(ctx, pod)
	assert.NoError(t, err)

	// Patch the Pod
	podPatch := client.MergeFrom(pod.DeepCopy())
	pod.Labels = map[string]string{"updated": "true"}
	err = tracingClient.Patch(ctx, pod, podPatch)
	assert.NoError(t, err)

	// Retrieve the Pod and check the annotation
	retrievedPod := &corev1.Pod{}
	err = tracingClient.Get(ctx, client.ObjectKey{Name: "test-pod", Namespace: "default"}, retrievedPod)
	assert.NoError(t, err)
	assert.Equal(t, traceID, retrievedPod.Annotations[constants.TraceIDAnnotation])
	assert.Equal(t, "true", retrievedPod.Labels["updated"])
	assert.NotEqual(t, spanID, retrievedPod.Annotations[constants.SpanIDAnnotation])
	assert.Equal(t, len(spanID), len(retrievedPod.Annotations[constants.SpanIDAnnotation]))

	// Initialize the TracingClient
	tracingClientNew := NewTracingClient(k8sClient, k8sClient, tracer, logger)
	ctxNew := context.Background()
	request = ClientObjectToRequestWithTraceID(&client.ObjectKey{Name: "pre-test-pod", Namespace: "default"})
	ctxNew, spanNew, errNew := tracingClientNew.StartTrace(ctxNew, &request, &corev1.Pod{})
	defer spanNew.End()
	assert.NoError(t, errNew)
	traceIDNew := spanNew.SpanContext().TraceID().String()
	retrievedPodClone := retrievedPod.DeepCopy()
	retrievedPodClone.Status.HostIP = "11.12.13.14"
	tracingClientNew.Update(ctxNew, retrievedPodClone)

	// Test EndTrace and ensure that it did not remove the traceID since it was updated by a different client
	_, err = tracingClient.EndTrace(ctx, retrievedPod)
	assert.NoError(t, err)
	finalPod := &corev1.Pod{}
	// Get the pod with default kubernetes client to ensure that there is no traceID and spanID
	err = k8sClient.Get(ctx, client.ObjectKey{Name: "test-pod", Namespace: "default"}, finalPod)
	assert.NoError(t, err)
	assert.Equal(t, traceIDNew, finalPod.Annotations[constants.TraceIDAnnotation])
	assert.NotEmpty(t, finalPod.Annotations[constants.SpanIDAnnotation])
}

func TestListWithTracing(t *testing.T) {
	// Create a fake Kubernetes client
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pre-test-pod",
			Namespace: "default",
		},
	}
	k8sClient := fake.NewClientBuilder().WithObjects(pod).Build()

	// Create a real tracer
	tracer := initTracer()

	// Create a logger
	logger := testr.New(t)

	// Initialize the TracingClient
	tracingClient := NewTracingClient(k8sClient, k8sClient, tracer, logger)

	ctx := context.Background()
	// Create a spanId since no GET is being called to initialize the span
	request := ClientObjectToRequestWithTraceID(&client.ObjectKey{Name: "pre-test-pod", Namespace: "default"})
	ctx, span, err := tracingClient.StartTrace(ctx, &request, &corev1.Pod{})
	defer span.End()
	assert.NoError(t, err)

	// Retrieve the Pod and check the annotation
	retrievedPod := &corev1.PodList{}
	err = tracingClient.List(ctx, retrievedPod)
	assert.NoError(t, err)
	span = trace.SpanFromContext(ctx)
	traceID := span.SpanContext().TraceID().String()
	assert.NotEmpty(t, traceID)

}

func TestDeleteWithTracing(t *testing.T) {
	// Create a fake Kubernetes client
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pre-test-pod",
			Namespace: "default",
		},
	}
	k8sClient := fake.NewClientBuilder().WithObjects(pod).Build()

	// Create a real tracer
	tracer := initTracer()

	// Create a logger
	logger := logr.Discard()

	// Initialize the TracingClient
	tracingClient := NewTracingClient(k8sClient, k8sClient, tracer, logger)

	ctx := context.Background()
	// Create a spanId since no GET is being called to initialize the span
	request := ClientObjectToRequestWithTraceID(&client.ObjectKey{Name: "pre-test-pod", Namespace: "default"})
	ctx, span, err := tracingClient.StartTrace(ctx, &request, &corev1.Pod{})
	defer span.End()
	assert.NoError(t, err)
	traceID := span.SpanContext().TraceID().String()

	// Retrieve the Pod and check the annotation
	err = tracingClient.Delete(ctx, pod)
	assert.NoError(t, err)
	span = trace.SpanFromContext(ctx)
	traceID = span.SpanContext().TraceID().String()
	assert.NotEmpty(t, traceID)
}

func TestDeleteAllOfWithTracing(t *testing.T) {
	// Create a fake Kubernetes client
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pre-test-pod",
			Namespace: "default",
		},
	}
	k8sClient := fake.NewClientBuilder().WithObjects(pod).Build()

	// Create a real tracer
	tracer := initTracer()

	// Create a logger
	logger := logr.Discard()

	// Initialize the TracingClient
	tracingClient := NewTracingClient(k8sClient, k8sClient, tracer, logger)

	ctx := context.Background()
	// Create a spanId since no GET is being called to initialize the span
	request := ClientObjectToRequestWithTraceID(&client.ObjectKey{Name: "pre-test-pod", Namespace: "default"})
	ctx, span, err := tracingClient.StartTrace(ctx, &request, &corev1.Pod{})
	defer span.End()
	assert.NoError(t, err)
	traceID := span.SpanContext().TraceID().String()

	// Retrieve the Pod and check the annotation
	retrievedPod := &corev1.Pod{}
	err = tracingClient.DeleteAllOf(ctx, retrievedPod)
	assert.NoError(t, err)
	span = trace.SpanFromContext(ctx)
	traceID = span.SpanContext().TraceID().String()
	assert.NotEmpty(t, traceID)

}

func TestGetConditions(t *testing.T) {
	// Create a scheme
	scheme := runtime.NewScheme()
	corev1.AddToScheme(scheme)

	// Create a Pod object with conditions
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pod",
			Namespace: "default",
		},
		Status: corev1.PodStatus{
			Conditions: []corev1.PodCondition{
				{
					Type:   corev1.PodScheduled,
					Status: corev1.ConditionTrue,
				},
			},
		},
	}

	// Retrieve the conditions using the getConditions function
	conditions, err := getConditionsAsMap(pod, scheme)

	// Assert no error occurred
	assert.NoError(t, err)

	assert.Equal(t, conditions[0]["Type"], v1.PodConditionType("PodScheduled"))
}

func TestGetConditionMessage(t *testing.T) {
	// Create a scheme
	scheme := runtime.NewScheme()
	corev1.AddToScheme(scheme)

	// Create a Pod object with conditions
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pod",
			Namespace: "default",
		},
		Status: corev1.PodStatus{
			Conditions: []corev1.PodCondition{
				{
					Type:               corev1.PodScheduled,
					Status:             corev1.ConditionTrue,
					Reason:             "PodScheduled",
					Message:            "Pod has been scheduled",
					LastTransitionTime: metav1.Now(),
				},
			},
		},
	}

	// Retrieve the condition message using the getConditionMessage function
	message, err := getConditionMessage("PodScheduled", pod, scheme)

	// Assert no error occurred
	assert.NoError(t, err)

	// Assert the message is as expected
	expectedMessage := "Pod has been scheduled"
	assert.Equal(t, expectedMessage, message)
}

func TestSetConditionMessage(t *testing.T) {
	// Create a scheme
	scheme := runtime.NewScheme()
	corev1.AddToScheme(scheme)

	// Create a Pod object with conditions
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pod",
			Namespace: "default",
		},
		Status: corev1.PodStatus{
			Conditions: []corev1.PodCondition{
				{
					Type:               corev1.PodScheduled,
					Status:             corev1.ConditionTrue,
					Reason:             "PodScheduled",
					Message:            "Pod has been scheduled",
					LastTransitionTime: metav1.Now(),
				},
			},
		},
	}

	// Set the condition message using the setConditionMessage function
	err := setConditionMessage("PodScheduled", "New message", pod, scheme)

	// Assert no error occurred
	assert.NoError(t, err)

	// Retrieve the updated condition message using the getConditionMessage function
	message, err := getConditionMessage("PodScheduled", pod, scheme)

	// Assert no error occurred
	assert.NoError(t, err)

	// Assert the message is as expected
	expectedMessage := "New message"
	assert.Equal(t, expectedMessage, message)

	// Test setting a new condition
	err = setConditionMessage("NewCondition", "Initial message", pod, scheme)

	// Assert no error occurred
	assert.NoError(t, err)

	// Retrieve the new condition message using the getConditionMessage function
	message, err = getConditionMessage("NewCondition", pod, scheme)

	// Assert no error occurred
	assert.NoError(t, err)

	// Assert the message is as expected
	expectedMessage = "Initial message"
	assert.Equal(t, expectedMessage, message)
}

func TestDeleteCondition(t *testing.T) {
	// Create a scheme
	scheme := runtime.NewScheme()
	corev1.AddToScheme(scheme)

	// Create a Pod object with conditions
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pod",
			Namespace: "default",
		},
		Status: corev1.PodStatus{
			Conditions: []corev1.PodCondition{
				{
					Type:               corev1.PodScheduled,
					Status:             corev1.ConditionTrue,
					Reason:             "PodScheduled",
					Message:            "Pod has been scheduled",
					LastTransitionTime: metav1.Now(),
				},
			},
		},
	}

	// Delete the condition using the deleteCondition function
	err := deleteConditionAsMap("PodScheduled", pod, scheme)

	// Assert no error occurred
	assert.NoError(t, err)

	// Retrieve the conditions using the getConditions function
	conditions, err := getConditionsAsMap(pod, scheme)

	// Assert no error occurred
	assert.NoError(t, err)

	// Assert the conditions are as expected
	expectedConditions := []map[string]interface{}(nil)
	assert.Equal(t, expectedConditions, conditions)
}

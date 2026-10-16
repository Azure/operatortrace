// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// pkg/client/tracing_client_test.go

package client

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/Azure/operatortrace/operatortrace-go/pkg/tracecontext"
	tracingtypes "github.com/Azure/operatortrace/operatortrace-go/pkg/types"
	"github.com/go-logr/logr"
	"github.com/go-logr/logr/testr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/conversion"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
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

func init() {
	// Initialize OTEL text map propagator for tests
	otel.SetTextMapPropagator(propagation.TraceContext{})
}

const (
	testTraceIDHex = "1234567890abcdef1234567890abcdef"
	testSpanIDHex  = "abcdef1234567890"
)

func tracingClientOptionsForTest(t *testing.T, tc TracingClient) Options {
	t.Helper()
	impl, ok := tc.(*tracingClient)
	require.True(t, ok, "expected *tracingClient")
	return impl.options
}

func annotateObjectWithTraceIDs(t *testing.T, obj client.Object, opts Options, traceID, spanID string) {
	t.Helper()
	traceParent, err := tracecontext.TraceParentFromIDs(traceID, spanID)
	require.NoError(t, err)
	annotations := obj.GetAnnotations()
	if annotations == nil {
		annotations = map[string]string{}
	}
	persistTraceCarrier(annotations, opts, traceParent, "")
	obj.SetAnnotations(annotations)
}

func traceIDsFromObject(t *testing.T, obj client.Object, opts Options) (string, string) {
	t.Helper()
	stored, ok := extractTraceContextFromAnnotations(obj.GetAnnotations(), opts)
	if !ok || stored.TraceParent == "" {
		return "", ""
	}
	spanContext, err := tracecontext.SpanContextFromTraceData(stored.TraceParent, stored.TraceState)
	require.NoError(t, err)
	return spanContext.TraceID().String(), spanContext.SpanID().String()
}

type unconvertibleClientObject struct {
	corev1.Pod  `json:",inline"`
	Unsupported chan string `json:"unsupported"`
}

func (obj *unconvertibleClientObject) DeepCopyObject() runtime.Object {
	copy := *obj
	copy.Pod = *obj.Pod.DeepCopy()
	return &copy
}

func TestWritesProceedWhenComparisonFails(t *testing.T) {
	for _, operation := range []string{"update", "patch", "status update", "status patch"} {
		t.Run(operation, func(t *testing.T) {
			ctx := context.Background()
			scheme := runtime.NewScheme()
			scheme.AddKnownTypes(schema.GroupVersion{Group: "test.example.com", Version: "v1"}, &unconvertibleClientObject{})
			existing := &unconvertibleClientObject{
				Pod:         corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "test-object", Namespace: "default", ResourceVersion: "1"}},
				Unsupported: make(chan string),
			}
			writes := 0
			k8sClient := fake.NewClientBuilder().WithScheme(scheme).WithInterceptorFuncs(interceptor.Funcs{
				Get: func(ctx context.Context, underlying client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
					*obj.(*unconvertibleClientObject) = *existing.DeepCopyObject().(*unconvertibleClientObject)
					return nil
				},
				Update: func(context.Context, client.WithWatch, client.Object, ...client.UpdateOption) error {
					writes++
					return nil
				},
				Patch: func(context.Context, client.WithWatch, client.Object, client.Patch, ...client.PatchOption) error {
					writes++
					return nil
				},
				SubResourceUpdate: func(context.Context, client.Client, string, client.Object, ...client.SubResourceUpdateOption) error {
					writes++
					return nil
				},
				SubResourcePatch: func(context.Context, client.Client, string, client.Object, client.Patch, ...client.SubResourcePatchOption) error {
					writes++
					return nil
				},
			}).Build()
			recorder := tracetest.NewSpanRecorder()
			provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
			t.Cleanup(func() { require.NoError(t, provider.Shutdown(ctx)) })
			tracingClient := NewTracingClient(k8sClient, k8sClient, provider.Tracer("test"), logr.Discard(), scheme)
			desired := existing.DeepCopyObject().(client.Object)
			desired.(*unconvertibleClientObject).Status.Phase = corev1.PodRunning
			patch := client.MergeFrom(existing.DeepCopyObject().(client.Object))
			var err error
			switch operation {
			case "update":
				err = tracingClient.Update(ctx, desired)
			case "patch":
				err = tracingClient.Patch(ctx, desired, patch)
			case "status update":
				err = tracingClient.Status().Update(ctx, desired)
			case "status patch":
				err = tracingClient.Status().Patch(ctx, desired, patch)
			}
			require.NoError(t, err)
			assert.Equal(t, 1, writes)
			recordedError := false
			for _, span := range recorder.Ended() {
				for _, event := range span.Events() {
					recordedError = recordedError || event.Name == "exception"
				}
			}
			assert.True(t, recordedError, "comparison error must be recorded on the span")
		})
	}
}

func TestSetConditionMessagePreservesDeletionError(t *testing.T) {
	failure := errors.New("condition conversion failed")
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	conversions := 0
	require.NoError(t, scheme.AddConversionFunc(&corev1.Pod{}, &corev1.Pod{}, func(from, to interface{}, scope conversion.Scope) error {
		conversions++
		if conversions == 1 {
			return failure
		}
		*to.(*corev1.Pod) = *from.(*corev1.Pod).DeepCopy()
		return nil
	}))
	pod := &corev1.Pod{}
	err := setConditionMessage("TraceID", testTraceIDHex, pod, scheme)
	require.ErrorIs(t, err, failure)
	assert.Equal(t, 1, conversions)
	assert.Empty(t, pod.Status.Conditions)
}

func TestStatusWritesProceedWhenTraceConditionsFail(t *testing.T) {
	for _, operation := range []string{"update", "patch", "create"} {
		t.Run(operation, func(t *testing.T) {
			ctx := context.Background()
			failure := errors.New("condition conversion failed")
			scheme := runtime.NewScheme()
			require.NoError(t, corev1.AddToScheme(scheme))
			require.NoError(t, scheme.AddConversionFunc(&corev1.Pod{}, &corev1.Pod{}, func(interface{}, interface{}, conversion.Scope) error { return failure }))
			existing := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "test-pod", Namespace: "default"}}
			writes := 0
			k8sClient := fake.NewClientBuilder().WithScheme(scheme).WithInterceptorFuncs(interceptor.Funcs{
				Get: func(ctx context.Context, underlying client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
					*obj.(*corev1.Pod) = *existing.DeepCopy()
					return nil
				},
				SubResourceUpdate: func(context.Context, client.Client, string, client.Object, ...client.SubResourceUpdateOption) error {
					writes++
					return nil
				},
				SubResourcePatch: func(context.Context, client.Client, string, client.Object, client.Patch, ...client.SubResourcePatchOption) error {
					writes++
					return nil
				},
				SubResourceCreate: func(context.Context, client.Client, string, client.Object, client.Object, ...client.SubResourceCreateOption) error {
					writes++
					return nil
				},
			}).Build()
			recorder := tracetest.NewSpanRecorder()
			provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
			t.Cleanup(func() { require.NoError(t, provider.Shutdown(ctx)) })
			tracingClient := NewTracingClient(k8sClient, k8sClient, provider.Tracer("test"), logr.Discard(), scheme)
			desired := existing.DeepCopy()
			desired.Status.Phase = corev1.PodRunning
			var err error
			switch operation {
			case "update":
				err = tracingClient.Status().Update(ctx, desired)
			case "patch":
				err = tracingClient.Status().Patch(ctx, desired, client.MergeFrom(existing.DeepCopy()))
			case "create":
				err = tracingClient.Status().Create(ctx, desired, &corev1.Pod{})
			}
			require.NoError(t, err)
			assert.Equal(t, 1, writes)
			recordedError := false
			for _, span := range recorder.Ended() {
				for _, event := range span.Events() {
					recordedError = recordedError || event.Name == "exception"
				}
			}
			assert.True(t, recordedError, "trace-condition failure must be recorded on the span")
		})
	}
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
		Logger:  logr.Discard(),
		scheme:  scheme,
		Client:  fakeClient,
		options: newOptions(),
	}

	// Mock object with traceID and spanID annotations
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pod",
			Namespace: "default",
		},
	}
	annotateObjectWithTraceIDs(t, pod, tracingClient.options, testTraceIDHex, testSpanIDHex)

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
	assert.Equal(t, testTraceIDHex, request.Parent.TraceID)
	assert.Equal(t, testSpanIDHex, request.Parent.SpanID)
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
	opts := tracingClientOptionsForTest(t, tracingClient)

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
	savedTraceID, savedSpanID := traceIDsFromObject(t, retrievedPod, opts)
	assert.Equal(t, traceID, savedTraceID)
	assert.NotEqual(t, spanID, savedSpanID)
	assert.Equal(t, len(spanID), len(savedSpanID))
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
	// Initialize the TracingClient with parent relationship so trace ID is inherited
	tracingClient := NewTracingClientWithOptions(k8sClient, k8sClient, tracer, logger, nil, WithIncomingTraceRelationship(TraceParentRelationshipParent))

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
	newOpts := tracingClientOptionsForTest(t, newTracingClient)

	// Retrieve the initial Pod to get the trace ID
	retrievedInitialPod := &corev1.Pod{}
	err = newTracingClient.Get(ctx, client.ObjectKey{Name: "initial-pod", Namespace: "default"}, retrievedInitialPod)
	assert.NoError(t, err)

	// Extract the trace ID from the retrieved initial pod annotations
	savedtraceID, savedSpanID := traceIDsFromObject(t, retrievedInitialPod, newOpts)
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
		traceid, _ := GetConditionMessage("TraceID", retrievedPatchedPod, k8sClient.Scheme())
		assert.Equal(t, savedtraceID, traceid)
		//Annotations will not be patched with Status.Patch
		_, patchedSpanID := traceIDsFromObject(t, retrievedPatchedPod, newOpts)
		assert.Equal(t, savedSpanID, patchedSpanID)
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
	opts := tracingClientOptionsForTest(t, tracingClient)

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
	savedTraceID, savedSpanID := traceIDsFromObject(t, retrievedPod, opts)
	assert.Equal(t, traceID, savedTraceID)
	assert.Equal(t, "true", retrievedPod.Labels["updated"])
	assert.NotEqual(t, spanID, savedSpanID)
	assert.Equal(t, len(spanID), len(savedSpanID))

	// Test status update with tracing
	t.Run("update status with tracing", func(t *testing.T) {
		pod.Status.Phase = corev1.PodRunning
		err = tracingClient.Status().Update(ctx, retrievedPod)
		assert.NoError(t, err)
		currentTraceID, _ := traceIDsFromObject(t, retrievedPod, opts)
		assert.Equal(t, traceID, currentTraceID)
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

func TestUpdateFallbackPreservesTypeMeta(t *testing.T) {
	tests := []struct {
		name                  string
		populatedTypeMeta     bool
		callerResourceVersion string
		wantConflict          bool
	}{
		{name: "empty caller GVK", callerResourceVersion: "1000"},
		{name: "populated caller GVK", populatedTypeMeta: true, callerResourceVersion: "1000"},
		{name: "stale caller conflicts", callerResourceVersion: "999", wantConflict: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			stored := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{
				Name: "test-configmap", Namespace: "default", ResourceVersion: "1000",
			}}
			var patchBody map[string]interface{}
			patchCalls := 0
			k8sClient := fake.NewClientBuilder().WithObjects(stored).WithInterceptorFuncs(interceptor.Funcs{
				Get: func(ctx context.Context, underlying client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
					if err := underlying.Get(ctx, key, obj, opts...); err != nil {
						return err
					}
					obj.SetResourceVersion("998")
					obj.GetObjectKind().SetGroupVersionKind(corev1.SchemeGroupVersion.WithKind("ConfigMap"))
					return nil
				},
				Patch: func(ctx context.Context, underlying client.WithWatch, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
					patchCalls++
					data, err := patch.Data(obj)
					require.NoError(t, err)
					require.NoError(t, json.Unmarshal(data, &patchBody))
					return underlying.Patch(ctx, obj, patch, opts...)
				},
			}).Build()
			desired := stored.DeepCopy()
			desired.ResourceVersion = test.callerResourceVersion
			desired.Labels = map[string]string{"changed": "true"}
			if test.populatedTypeMeta {
				desired.SetGroupVersionKind(corev1.SchemeGroupVersion.WithKind("ConfigMap"))
			}
			tracingClient := NewTracingClient(k8sClient, k8sClient, initTracer(), logr.Discard())

			err := tracingClient.Update(ctx, desired)
			if test.wantConflict {
				require.True(t, apierrors.IsConflict(err), "expected a resourceVersion conflict, got %v", err)
			} else {
				require.NoError(t, err)
			}

			require.Equal(t, 1, patchCalls)
			assert.NotContains(t, patchBody, "apiVersion")
			assert.NotContains(t, patchBody, "kind")
			metadata := patchBody["metadata"].(map[string]interface{})
			assert.Equal(t, test.callerResourceVersion, metadata["resourceVersion"])
			assert.Equal(t, map[string]interface{}{"changed": "true"}, metadata["labels"])
			assert.NotEmpty(t, metadata["annotations"])
			final := &corev1.ConfigMap{}
			require.NoError(t, k8sClient.Get(ctx, client.ObjectKeyFromObject(stored), final))
			if test.wantConflict {
				assert.Empty(t, final.Labels)
			} else {
				assert.Equal(t, desired.Labels, final.Labels)
			}
		})
	}
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
	opts := tracingClientOptionsForTest(t, tracingClient)

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
	savedTraceID, savedSpanID := traceIDsFromObject(t, retrievedPod, opts)
	assert.Equal(t, traceID, savedTraceID)
	assert.Equal(t, "true", retrievedPod.Labels["updated"])
	assert.NotEqual(t, spanID, savedSpanID)
	assert.Equal(t, len(spanID), len(savedSpanID))

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
	opts := tracingClientOptionsForTest(t, tracingClient)

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
	savedTraceID, savedSpanID := traceIDsFromObject(t, retrievedPod, opts)
	assert.Equal(t, traceID, savedTraceID)
	assert.Equal(t, "true", retrievedPod.Labels["updated"])
	assert.Equal(t, "test-pod", retrievedPod.OwnerReferences[0].Name)
	assert.NotEqual(t, spanID, savedSpanID)
	assert.Equal(t, len(spanID), len(savedSpanID))

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
	opts := tracingClientOptionsForTest(t, tracingClient)

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
	savedTraceID, savedSpanID := traceIDsFromObject(t, retrievedPod, opts)
	assert.Equal(t, traceID, savedTraceID)
	assert.Equal(t, "true", retrievedPod.Labels["updated"])
	assert.NotEqual(t, spanID, savedSpanID)
	assert.Equal(t, len(spanID), len(savedSpanID))

	// Test EndTrace
	err = tracingClient.EndTrace(ctx, retrievedPod)
	assert.NoError(t, err)
	finalPod := &corev1.Pod{}
	// Get the pod with default kubernetes client to ensure that there is no traceID and spanID
	err = k8sClient.Get(ctx, client.ObjectKey{Name: "test-pod", Namespace: "default"}, finalPod)
	assert.NoError(t, err)
	finalTraceID, finalSpanID := traceIDsFromObject(t, finalPod, opts)
	assert.Empty(t, finalTraceID)
	assert.Empty(t, finalSpanID)
	assert.Equal(t, 1, len(finalPod.Status.Conditions))
}

func TestEndTraceRemovesOnlyOwnedAnnotations(t *testing.T) {
	traceParent, err := tracecontext.TraceParentFromIDs(testTraceIDHex, testSpanIDHex)
	require.NoError(t, err)
	defaultOpts := newOptions()
	customOpts := newOptions(WithAnnotationPrefix("example.com"), WithEmittedAnnotationSuffixes("parent", "state"), WithIncomingTraceParentAnnotation("upstream.example.com/parent"))
	tests := []struct {
		name        string
		options     Options
		annotations map[string]string
		removedKeys []string
	}{
		{
			name:        "only traceparent",
			options:     defaultOpts,
			annotations: map[string]string{defaultOpts.emittedTraceParentAnnotationKey(): traceParent},
			removedKeys: []string{defaultOpts.emittedTraceParentAnnotationKey()},
		},
		{
			name:    "custom and legacy keys with unrelated annotations",
			options: customOpts,
			annotations: map[string]string{
				customOpts.emittedTraceParentAnnotationKey(): traceParent,
				customOpts.emittedTraceStateAnnotationKey():  "vendor=value",
				customOpts.legacyTraceIDAnnotationKey():      testTraceIDHex,
				customOpts.legacySpanIDAnnotationKey():       testSpanIDHex,
				customOpts.legacyTraceTimeAnnotationKey():    "2026-10-16T00:00:00Z",
				"upstream.example.com/parent":                traceParent,
				"example.com/keep":                           "value",
			},
			removedKeys: []string{
				customOpts.emittedTraceParentAnnotationKey(), customOpts.emittedTraceStateAnnotationKey(),
				customOpts.legacyTraceIDAnnotationKey(), customOpts.legacySpanIDAnnotationKey(), customOpts.legacyTraceTimeAnnotationKey(),
			},
		},
		{
			name:    "legacy keys only",
			options: defaultOpts,
			annotations: map[string]string{
				defaultOpts.legacyTraceIDAnnotationKey():   testTraceIDHex,
				defaultOpts.legacySpanIDAnnotationKey():    testSpanIDHex,
				defaultOpts.legacyTraceTimeAnnotationKey(): "2026-10-16T00:00:00Z",
			},
			removedKeys: []string{defaultOpts.legacyTraceIDAnnotationKey(), defaultOpts.legacySpanIDAnnotationKey(), defaultOpts.legacyTraceTimeAnnotationKey()},
		},
		{name: "no annotations", options: defaultOpts},
		{name: "empty annotations", options: defaultOpts, annotations: map[string]string{}},
		{name: "unrelated annotations only", options: defaultOpts, annotations: map[string]string{"example.com/keep": "value"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{
				Name: "test-pod", Namespace: "default", Annotations: test.annotations,
			}}
			readCalls := 0
			patchCalls := 0
			var patchBody map[string]interface{}
			k8sClient := fake.NewClientBuilder().WithObjects(pod).WithInterceptorFuncs(interceptor.Funcs{
				Get: func(ctx context.Context, underlying client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
					readCalls++
					return underlying.Get(ctx, key, obj, opts...)
				},
				Patch: func(ctx context.Context, underlying client.WithWatch, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
					patchCalls++
					assert.Equal(t, types.MergePatchType, patch.Type())
					data, err := patch.Data(obj)
					require.NoError(t, err)
					require.NoError(t, json.Unmarshal(data, &patchBody))
					fresh := &corev1.Pod{}
					require.NoError(t, underlying.Get(ctx, client.ObjectKeyFromObject(obj), fresh))
					if fresh.Annotations == nil {
						fresh.Annotations = map[string]string{}
					}
					fresh.Annotations["example.com/concurrent"] = "preserved"
					require.NoError(t, underlying.Update(ctx, fresh))
					return underlying.Patch(ctx, obj, patch, opts...)
				},
			}).Build()
			tracingClient := newTracingClientWithOptions(k8sClient, k8sClient, initTracer(), logr.Discard(), k8sClient.Scheme(), func(opts *Options) { *opts = test.options })
			obj := pod.DeepCopy()

			require.NoError(t, tracingClient.EndTrace(ctx, obj))
			if len(test.removedKeys) == 0 {
				assert.Zero(t, readCalls)
			} else {
				assert.Equal(t, 1, readCalls)
			}

			final := &corev1.Pod{}
			require.NoError(t, k8sClient.Get(ctx, client.ObjectKeyFromObject(pod), final))
			if len(test.removedKeys) == 0 {
				assert.Zero(t, patchCalls)
				assert.Empty(t, patchBody)
				assert.Len(t, final.Annotations, len(pod.Annotations))
				for key, value := range pod.Annotations {
					assert.Equal(t, value, final.Annotations[key])
				}
				return
			}
			assert.Equal(t, 1, patchCalls)
			expectedDeleted := map[string]interface{}{}
			for _, key := range test.removedKeys {
				expectedDeleted[key] = nil
				assert.NotContains(t, final.Annotations, key)
			}
			assert.Equal(t, map[string]interface{}{"metadata": map[string]interface{}{"annotations": expectedDeleted}}, patchBody)
			assert.Equal(t, "preserved", final.Annotations["example.com/concurrent"])
			for key, value := range pod.Annotations {
				if _, removed := expectedDeleted[key]; !removed {
					assert.Equal(t, value, final.Annotations[key])
				}
			}
		})
	}
}

func TestEndTraceStatusCleanup(t *testing.T) {
	ready := corev1.PodCondition{Type: corev1.PodReady, Status: corev1.ConditionTrue}
	traceID := corev1.PodCondition{Type: "TraceID", Status: corev1.ConditionUnknown, Message: testTraceIDHex}
	spanID := corev1.PodCondition{Type: "SpanID", Status: corev1.ConditionUnknown, Message: testSpanIDHex}
	tests := []struct {
		name             string
		configMap        bool
		annotations      bool
		conditions       []corev1.PodCondition
		wantConditions   []corev1.PodCondition
		wantStatusWrites int
	}{
		{name: "no status subresource", configMap: true, annotations: true},
		{name: "no status or trace annotations", configMap: true},
		{name: "empty conditions", annotations: true},
		{name: "unrelated conditions", annotations: true, conditions: []corev1.PodCondition{ready}, wantConditions: []corev1.PodCondition{ready}},
		{name: "legacy trace ID without annotations", conditions: []corev1.PodCondition{traceID}, wantStatusWrites: 1},
		{name: "legacy span ID without annotations", conditions: []corev1.PodCondition{spanID}, wantStatusWrites: 1},
		{name: "both legacy conditions", annotations: true, conditions: []corev1.PodCondition{traceID, ready, spanID}, wantConditions: []corev1.PodCondition{ready}, wantStatusWrites: 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			var obj client.Object = &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{Name: "test-object", Namespace: "default"},
				Status:     corev1.PodStatus{Conditions: test.conditions},
			}
			if test.configMap {
				obj = &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "test-object", Namespace: "default"}}
			}
			if test.annotations {
				annotateObjectWithTraceIDs(t, obj, newOptions(), testTraceIDHex, testSpanIDHex)
			}
			statusWrites := 0
			k8sClient := fake.NewClientBuilder().WithObjects(obj).WithInterceptorFuncs(interceptor.Funcs{
				SubResourcePatch: func(ctx context.Context, underlying client.Client, subResource string, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
					statusWrites++
					assert.Equal(t, "status", subResource)
					if test.wantStatusWrites == 0 {
						return errors.New("status write is not allowed")
					}
					return underlying.SubResource(subResource).Patch(ctx, obj, patch, opts...)
				},
			}).Build()
			tracingClient := NewTracingClient(k8sClient, k8sClient, initTracer(), logr.Discard())

			require.NoError(t, tracingClient.EndTrace(ctx, obj))

			assert.Equal(t, test.wantStatusWrites, statusWrites)
			final := obj.DeepCopyObject().(client.Object)
			require.NoError(t, k8sClient.Get(ctx, client.ObjectKeyFromObject(obj), final))
			assert.Empty(t, final.GetAnnotations())
			if pod, ok := final.(*corev1.Pod); ok {
				if len(test.wantConditions) == 0 {
					assert.Empty(t, pod.Status.Conditions)
				} else {
					assert.Equal(t, test.wantConditions, pod.Status.Conditions)
				}
			}
		})
	}
}

func TestEndTracePreservesConcurrentConditions(t *testing.T) {
	traceID := corev1.PodCondition{Type: "TraceID", Status: corev1.ConditionUnknown, Message: testTraceIDHex}
	ready := corev1.PodCondition{Type: corev1.PodReady, Status: corev1.ConditionTrue}
	tests := []struct {
		name                 string
		traceAnnotations     bool
		concurrentBeforeRead bool
	}{
		{name: "fresh conditions without metadata patch", concurrentBeforeRead: true},
		{name: "fresh conditions with metadata patch", traceAnnotations: true, concurrentBeforeRead: true},
		{name: "conflict without metadata patch"},
		{name: "conflict with metadata patch", traceAnnotations: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			pod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-pod", Namespace: "default", Annotations: map[string]string{"example.com/keep": "value"},
				},
				Status: corev1.PodStatus{Conditions: []corev1.PodCondition{traceID}},
			}
			if test.traceAnnotations {
				annotateObjectWithTraceIDs(t, pod, newOptions(), testTraceIDHex, testSpanIDHex)
			}
			caller := pod.DeepCopy()
			addReady := func(underlying client.Client) {
				fresh := &corev1.Pod{}
				require.NoError(t, underlying.Get(ctx, client.ObjectKeyFromObject(pod), fresh))
				fresh.Status.Conditions = append(fresh.Status.Conditions, ready)
				require.NoError(t, underlying.Status().Update(ctx, fresh))
			}
			statusWrites := 0
			k8sClient := fake.NewClientBuilder().WithObjects(pod).WithInterceptorFuncs(interceptor.Funcs{
				SubResourcePatch: func(ctx context.Context, underlying client.Client, subResource string, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
					statusWrites++
					data, err := patch.Data(obj)
					require.NoError(t, err)
					var body map[string]interface{}
					require.NoError(t, json.Unmarshal(data, &body))
					metadata, _ := body["metadata"].(map[string]interface{})
					assert.NotEmpty(t, metadata["resourceVersion"])
					if !test.concurrentBeforeRead {
						addReady(underlying)
					}
					return underlying.SubResource(subResource).Patch(ctx, obj, patch, opts...)
				},
			}).Build()
			if test.concurrentBeforeRead {
				addReady(k8sClient)
			}
			tracingClient := NewTracingClient(k8sClient, k8sClient, initTracer(), logr.Discard())

			err := tracingClient.EndTrace(ctx, caller)

			if test.concurrentBeforeRead {
				require.NoError(t, err)
			} else {
				require.True(t, apierrors.IsConflict(err), "expected a resourceVersion conflict, got %v", err)
			}
			assert.Equal(t, 1, statusWrites)
			final := &corev1.Pod{}
			require.NoError(t, k8sClient.Get(ctx, client.ObjectKeyFromObject(pod), final))
			if test.concurrentBeforeRead {
				assert.Equal(t, []corev1.PodCondition{ready}, final.Status.Conditions)
			} else {
				assert.Equal(t, []corev1.PodCondition{traceID, ready}, final.Status.Conditions)
			}
			assert.Equal(t, "value", final.Annotations["example.com/keep"])
		})
	}
}

func TestEndTraceErrors(t *testing.T) {
	failure := errors.New("cleanup failed")
	for _, stage := range []string{"read", "metadata patch", "status patch"} {
		t.Run(stage, func(t *testing.T) {
			ctx := context.Background()
			pod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{Name: "test-pod", Namespace: "default"},
				Status:     corev1.PodStatus{Conditions: []corev1.PodCondition{{Type: "TraceID", Status: corev1.ConditionUnknown, Message: testTraceIDHex}}},
			}
			annotateObjectWithTraceIDs(t, pod, newOptions(), testTraceIDHex, testSpanIDHex)
			metadataWrites, statusWrites := 0, 0
			k8sClient := fake.NewClientBuilder().WithObjects(pod).WithInterceptorFuncs(interceptor.Funcs{
				Get: func(ctx context.Context, underlying client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
					if stage == "read" {
						return failure
					}
					return underlying.Get(ctx, key, obj, opts...)
				},
				Patch: func(ctx context.Context, underlying client.WithWatch, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
					metadataWrites++
					if stage == "metadata patch" {
						return failure
					}
					return underlying.Patch(ctx, obj, patch, opts...)
				},
				SubResourcePatch: func(ctx context.Context, underlying client.Client, subResource string, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
					statusWrites++
					if stage == "status patch" {
						return failure
					}
					return underlying.SubResource(subResource).Patch(ctx, obj, patch, opts...)
				},
			}).Build()
			tracingClient := NewTracingClient(k8sClient, k8sClient, initTracer(), logr.Discard())

			err := tracingClient.EndTrace(ctx, pod)

			require.ErrorIs(t, err, failure)
			if stage == "read" {
				assert.Zero(t, metadataWrites)
			} else {
				assert.Equal(t, 1, metadataWrites)
			}
			if stage == "status patch" {
				assert.Equal(t, 1, statusWrites)
			} else {
				assert.Zero(t, statusWrites)
			}
		})
	}
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
	opts := tracingClientOptionsForTest(t, tracingClient)

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
	savedTraceID, savedSpanID := traceIDsFromObject(t, retrievedPod, opts)
	assert.Equal(t, traceID, savedTraceID)
	assert.Equal(t, "true", retrievedPod.Labels["updated"])
	assert.NotEqual(t, spanID, savedSpanID)
	assert.Equal(t, len(spanID), len(savedSpanID))

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
	err = tracingClient.EndTrace(ctx, retrievedPod)
	assert.NoError(t, err)
	finalPod := &corev1.Pod{}
	// Get the pod with default kubernetes client to ensure that there is no traceID and spanID
	err = k8sClient.Get(ctx, client.ObjectKey{Name: "test-pod", Namespace: "default"}, finalPod)
	assert.NoError(t, err)
	finalTraceID, finalSpanID := traceIDsFromObject(t, finalPod, opts)
	assert.Equal(t, traceIDNew, finalTraceID)
	assert.NotEmpty(t, finalSpanID)
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
	assert.NotEmpty(t, traceID)

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
	assert.NotEmpty(t, traceID)

	// Retrieve the Pod and check the annotation
	retrievedPod := &corev1.Pod{}
	err = tracingClient.DeleteAllOf(ctx, retrievedPod)
	assert.NoError(t, err)
	span = trace.SpanFromContext(ctx)
	traceID = span.SpanContext().TraceID().String()
	assert.NotEmpty(t, traceID)

}

func TestEndTraceInspectionFailuresAreObservable(t *testing.T) {
	for _, withAnnotations := range []bool{false, true} {
		t.Run(map[bool]string{false: "before read", true: "after metadata cleanup"}[withAnnotations], func(t *testing.T) {
			ctx := context.Background()
			failure := errors.New("condition conversion failed")
			scheme := runtime.NewScheme()
			require.NoError(t, corev1.AddToScheme(scheme))
			require.NoError(t, scheme.AddConversionFunc(&corev1.Pod{}, &corev1.Pod{}, func(interface{}, interface{}, conversion.Scope) error { return failure }))
			pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "test-pod", Namespace: "default"}}
			if withAnnotations {
				annotateObjectWithTraceIDs(t, pod, newOptions(), testTraceIDHex, testSpanIDHex)
			}
			reads, metadataWrites, statusWrites := 0, 0, 0
			k8sClient := fake.NewClientBuilder().WithScheme(scheme).WithInterceptorFuncs(interceptor.Funcs{
				Get: func(ctx context.Context, underlying client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
					reads++
					*obj.(*corev1.Pod) = *pod.DeepCopy()
					return nil
				},
				Patch: func(context.Context, client.WithWatch, client.Object, client.Patch, ...client.PatchOption) error {
					metadataWrites++
					return nil
				},
				SubResourcePatch: func(context.Context, client.Client, string, client.Object, client.Patch, ...client.SubResourcePatchOption) error {
					statusWrites++
					return nil
				},
			}).Build()
			recorder := tracetest.NewSpanRecorder()
			provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
			t.Cleanup(func() { require.NoError(t, provider.Shutdown(ctx)) })
			tracingClient := NewTracingClient(k8sClient, k8sClient, provider.Tracer("test"), logr.Discard(), scheme)
			require.NoError(t, tracingClient.EndTrace(ctx, pod.DeepCopy()))
			assert.Zero(t, statusWrites)
			if withAnnotations {
				assert.Equal(t, 1, reads)
				assert.Equal(t, 1, metadataWrites)
			} else {
				assert.Zero(t, reads)
				assert.Zero(t, metadataWrites)
			}
			recordedFailure := false
			for _, span := range recorder.Ended() {
				for _, event := range span.Events() {
					for _, attribute := range event.Attributes {
						recordedFailure = recordedFailure || attribute.Key == "exception.message" && strings.Contains(attribute.Value.AsString(), failure.Error())
					}
				}
			}
			assert.True(t, recordedFailure)
		})
	}
}

func TestLegacyTraceConditionInspection(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	failingScheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(failingScheme))
	require.NoError(t, failingScheme.AddConversionFunc(&corev1.Pod{}, &corev1.Pod{}, func(interface{}, interface{}, conversion.Scope) error {
		return errors.New("condition conversion failed")
	}))
	tests := []struct {
		name    string
		obj     client.Object
		scheme  *runtime.Scheme
		want    bool
		wantErr bool
	}{
		{name: "no status", obj: &corev1.ConfigMap{}},
		{name: "no conditions", obj: &corev1.Service{}},
		{name: "empty conditions", obj: &corev1.Pod{}},
		{name: "legacy condition", obj: &corev1.Pod{Status: corev1.PodStatus{Conditions: []corev1.PodCondition{{Type: "TraceID"}}}}, want: true},
		{name: "unregistered type", obj: &corev1.Pod{}, scheme: runtime.NewScheme(), wantErr: true},
		{name: "conversion failure", obj: &corev1.Pod{}, scheme: failingScheme, wantErr: true},
		{name: "unregistered unstructured", obj: &unstructured.Unstructured{Object: map[string]interface{}{
			"apiVersion": "example.com/v1", "kind": "Example", "status": map[string]interface{}{
				"conditions": []interface{}{map[string]interface{}{"type": "TraceID"}},
			},
		}}, wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			inspectionScheme := test.scheme
			if inspectionScheme == nil {
				inspectionScheme = scheme
			}
			found, err := hasLegacyTraceConditions(test.obj, inspectionScheme)
			assert.Equal(t, test.want, found)
			if test.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
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

	assert.Equal(t, conditions[0]["Type"], corev1.PodConditionType("PodScheduled"))
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
	message, err := GetConditionMessage("PodScheduled", pod, scheme)

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
	message, err := GetConditionMessage("PodScheduled", pod, scheme)

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
	message, err = GetConditionMessage("NewCondition", pod, scheme)

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

// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// pkg/handler/enqueue_owner_test.go

package handler

import (
	"context"
	"testing"

	tracingconstants "github.com/Azure/operatortrace/operatortrace-go/pkg/constants"
	"github.com/Azure/operatortrace/operatortrace-go/pkg/tracecontext"

	tracingqueue "github.com/Azure/operatortrace/operatortrace-go/pkg/tracingqueue"
	tracingtypes "github.com/Azure/operatortrace/operatortrace-go/pkg/types"
	"github.com/stretchr/testify/assert"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/event"
	ctrlreconcile "sigs.k8s.io/controller-runtime/pkg/reconcile"
)

func init() {
	// Initialize OTEL text map propagator for tests
	otel.SetTextMapPropagator(propagation.TraceContext{})
}

// Test enqueing objects based on the owner reference for create.
const (
	baseTraceID           = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	baseSpanID            = "bbbbbbbbbbbbbbbb"
	differentNameTraceID  = "cccccccccccccccccccccccccccccccc"
	differentNameSpanID   = "dddddddddddddddd"
	differentOwnerTraceID = "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"
	differentOwnerSpanID  = "ffffffffffffffff"
	mixedOwnerTraceID     = "99999999999999999999999999999999"
	mixedOwnerSpanID      = "8888888888888888"
)

func TestEnqueueOwnerCreate(t *testing.T) {
	t.Parallel()

	// Base node object
	nodeObjectBase := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Generation:      1,
			ResourceVersion: "1",
			Name:            "node1",
			Annotations:     traceAnnotations(baseTraceID, baseSpanID),
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion: "1",
					Kind:       "Node",
					Name:       "ParentNode",
					UID:        "abcdef1",
				},
			},
		},
	}

	// Change the node name and use a different trace / span information.
	nodeObjectWithDifferentNameAndTraceInfo := nodeObjectBase.DeepCopy()
	nodeObjectWithDifferentNameAndTraceInfo.SetName("node2")
	nodeObjectWithDifferentNameAndTraceInfo.Annotations = traceAnnotations(differentNameTraceID, differentNameSpanID)

	// Change the node name and use a different Owner.
	nodeObjectWithDifferentOwnerReference := nodeObjectBase.DeepCopy()
	nodeObjectWithDifferentOwnerReference.SetName("node3")
	nodeObjectWithDifferentOwnerReference.Annotations = traceAnnotations(differentOwnerTraceID, differentOwnerSpanID)
	nodeObjectWithDifferentOwnerReference.OwnerReferences = []metav1.OwnerReference{
		{
			APIVersion: "1",
			Kind:       "Node",
			Name:       "ParentNode2",
			UID:        "abcdef2",
		},
	}

	// Change the node name and use a the original owner but add a second new owner.
	nodeObjectWithDifferentOwnerReferenceAndOriginal := nodeObjectBase.DeepCopy()
	nodeObjectWithDifferentOwnerReferenceAndOriginal.SetName("node4")
	nodeObjectWithDifferentOwnerReferenceAndOriginal.Annotations = traceAnnotations(mixedOwnerTraceID, mixedOwnerSpanID)
	nodeObjectWithDifferentOwnerReferenceAndOriginal.OwnerReferences = []metav1.OwnerReference{
		{
			APIVersion: nodeObjectBase.OwnerReferences[0].APIVersion,
			Kind:       nodeObjectBase.OwnerReferences[0].Kind,
			Name:       nodeObjectBase.OwnerReferences[0].Name,
			UID:        nodeObjectBase.OwnerReferences[0].UID,
		},
		{
			APIVersion: "1",
			Kind:       "Node",
			Name:       "ParentNode2",
			UID:        "abcdef2",
		},
	}

	// Change the node name and remove an span / trace information
	nodeObjectWithoutTraceInformation := nodeObjectBase.DeepCopy()
	nodeObjectWithoutTraceInformation.SetName("node5")
	nodeObjectWithoutTraceInformation.Annotations = map[string]string{}

	// Setup a fake client that has our registered type in the RESTMapper
	groupVersions := []schema.GroupVersion{{Group: "Node", Version: "1"}}
	restmap := meta.NewDefaultRESTMapper(groupVersions)
	customGroupVersion := schema.GroupVersionKind{Kind: "Node", Version: "1"}
	restmap.Add(customGroupVersion, meta.RESTScopeRoot)
	k8sClient := fake.NewClientBuilder().
		WithObjects(nodeObjectBase, nodeObjectWithDifferentNameAndTraceInfo, nodeObjectWithDifferentOwnerReference, nodeObjectWithDifferentOwnerReferenceAndOriginal, nodeObjectWithoutTraceInformation).
		WithRESTMapper(restmap).
		Build()

	tests := []struct {
		name              string
		inputs            []corev1.Node
		expected_requests []tracingtypes.RequestWithTraceID
	}{
		{
			name:   "Basic Test Case",
			inputs: []corev1.Node{*nodeObjectBase},
			expected_requests: []tracingtypes.RequestWithTraceID{
				{
					Request: ctrlreconcile.Request{
						NamespacedName: types.NamespacedName{
							Name:      nodeObjectBase.OwnerReferences[0].Name,
							Namespace: nodeObjectBase.Namespace,
						},
					},
					Parent: tracingtypes.RequestParent{
						Name:    nodeObjectBase.Name,
						Kind:    "Node",
						TraceID: baseTraceID,
						SpanID:  baseSpanID,
					},
					LinkedSpanCount: 0,
				},
			},
		},
		{
			name:   "A different parent should create a second reconcile request",
			inputs: []corev1.Node{*nodeObjectBase, *nodeObjectWithDifferentOwnerReference},
			expected_requests: []tracingtypes.RequestWithTraceID{
				{
					Request: ctrlreconcile.Request{
						NamespacedName: types.NamespacedName{
							Name:      nodeObjectBase.OwnerReferences[0].Name,
							Namespace: nodeObjectBase.Namespace,
						},
					},
					Parent: tracingtypes.RequestParent{
						Name:    nodeObjectBase.Name,
						Kind:    "Node",
						TraceID: baseTraceID,
						SpanID:  baseSpanID,
					},
					LinkedSpanCount: 0,
				},
				{
					Request: ctrlreconcile.Request{
						NamespacedName: types.NamespacedName{
							Name:      nodeObjectWithDifferentOwnerReference.OwnerReferences[0].Name,
							Namespace: nodeObjectWithDifferentOwnerReference.Namespace,
						},
					},
					Parent: tracingtypes.RequestParent{
						Name:    nodeObjectWithDifferentOwnerReference.Name,
						Kind:    "Node",
						TraceID: differentOwnerTraceID,
						SpanID:  differentOwnerSpanID,
					},
					LinkedSpanCount: 0,
				},
			},
		},
		{
			name:   "The same parent shouldn't be added to the workqueue twice and should create a LinkedSpan",
			inputs: []corev1.Node{*nodeObjectBase, *nodeObjectWithDifferentNameAndTraceInfo},
			expected_requests: []tracingtypes.RequestWithTraceID{
				{
					Request: ctrlreconcile.Request{
						NamespacedName: types.NamespacedName{
							Name:      nodeObjectBase.OwnerReferences[0].Name,
							Namespace: nodeObjectBase.Namespace,
						},
					},
					Parent: tracingtypes.RequestParent{
						Name:    nodeObjectBase.Name,
						Kind:    "Node",
						TraceID: baseTraceID,
						SpanID:  baseSpanID,
					},
					LinkedSpans: [10]tracingtypes.LinkedSpan{
						{
							TraceID: differentNameTraceID,
							SpanID:  differentNameSpanID,
						},
					},
					LinkedSpanCount: 1,
				},
			},
		},
		{
			name:   "Validate using an object without Tracing information followed by one that has trace information",
			inputs: []corev1.Node{*nodeObjectWithoutTraceInformation, *nodeObjectBase},
			expected_requests: []tracingtypes.RequestWithTraceID{
				{
					Request: ctrlreconcile.Request{
						NamespacedName: types.NamespacedName{
							Name:      nodeObjectBase.OwnerReferences[0].Name,
							Namespace: nodeObjectBase.Namespace,
						},
					},
					Parent: tracingtypes.RequestParent{
						Name:    nodeObjectWithoutTraceInformation.Name,
						Kind:    "Node",
						TraceID: "",
						SpanID:  "",
					},
					LinkedSpans: [10]tracingtypes.LinkedSpan{
						{
							TraceID: baseTraceID,
							SpanID:  baseSpanID,
						},
					},
					LinkedSpanCount: 1,
				},
			},
		},
		{
			name:   "Validate using an object with trace information followed by one without trace information",
			inputs: []corev1.Node{*nodeObjectBase, *nodeObjectWithoutTraceInformation},
			expected_requests: []tracingtypes.RequestWithTraceID{
				{
					Request: ctrlreconcile.Request{
						NamespacedName: types.NamespacedName{
							Name:      nodeObjectBase.OwnerReferences[0].Name,
							Namespace: nodeObjectBase.Namespace,
						},
					},
					Parent: tracingtypes.RequestParent{
						Name:    nodeObjectBase.Name,
						Kind:    "Node",
						TraceID: baseTraceID,
						SpanID:  baseSpanID,
					},
					LinkedSpanCount: 0,
				},
			},
		},
		{
			name:   "Validate a case of a second object with the same parent + also a different parent",
			inputs: []corev1.Node{*nodeObjectBase, *nodeObjectWithDifferentOwnerReferenceAndOriginal},
			expected_requests: []tracingtypes.RequestWithTraceID{
				{
					Request: ctrlreconcile.Request{
						NamespacedName: types.NamespacedName{
							Name:      nodeObjectBase.OwnerReferences[0].Name,
							Namespace: nodeObjectBase.Namespace,
						},
					},
					Parent: tracingtypes.RequestParent{
						Name:    nodeObjectBase.Name,
						Kind:    "Node",
						TraceID: baseTraceID,
						SpanID:  baseSpanID,
					},
					LinkedSpans: [10]tracingtypes.LinkedSpan{
						{
							TraceID: mixedOwnerTraceID,
							SpanID:  mixedOwnerSpanID,
						},
					},
					LinkedSpanCount: 1,
				},
				{
					Request: ctrlreconcile.Request{
						NamespacedName: types.NamespacedName{
							Name:      nodeObjectWithDifferentOwnerReferenceAndOriginal.OwnerReferences[1].Name, // The [1] index is the second different parent
							Namespace: nodeObjectWithDifferentOwnerReferenceAndOriginal.Namespace,
						},
					},
					Parent: tracingtypes.RequestParent{
						Name:    nodeObjectWithDifferentOwnerReferenceAndOriginal.Name,
						Kind:    "Node",
						TraceID: mixedOwnerTraceID,
						SpanID:  mixedOwnerSpanID,
					},
					LinkedSpanCount: 0,
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create our enqueue request reference.
			r := EnqueueRequestForOwner(k8sClient.Scheme(), k8sClient.RESTMapper(), &corev1.Node{})

			// Create our tracing queue to attempt to add objects to.
			queue := tracingqueue.NewTracingQueue()

			// For each input, trigger a CreateEvent
			for _, input := range tt.inputs {
				r.Create(context.TODO(), event.CreateEvent{Object: &input}, queue)
			}

			// End queue length should match the number of requests we expected to be created
			assert.Equal(t, len(tt.expected_requests), queue.Len())

			// Validate that what is in our queue matches our expected requests.
			for _, expected_request := range tt.expected_requests {
				added_request, _ := queue.Get()
				assert.Equal(t, expected_request.LinkedSpanCount, added_request.LinkedSpanCount)
				if expected_request.LinkedSpanCount > 0 {
					for span_index, expected_linked_span := range expected_request.LinkedSpans {
						assert.Equal(t, expected_linked_span, added_request.LinkedSpans[span_index])
					}
				}
				assert.Equal(t, expected_request.Name, added_request.Name)
				assert.Equal(t, expected_request.Namespace, added_request.Namespace)
				assert.Equal(t, expected_request.Parent.Name, added_request.Parent.Name)
				assert.Equal(t, expected_request.Parent.Kind, added_request.Parent.Kind)
				assert.Equal(t, expected_request.Parent.TraceID, added_request.Parent.TraceID)
				assert.Equal(t, expected_request.Parent.SpanID, added_request.Parent.SpanID)
			}
		})
	}

}

func traceAnnotations(traceID, spanID string) map[string]string {
	if traceID == "" || spanID == "" {
		return map[string]string{}
	}
	return map[string]string{
		tracingconstants.DefaultTraceParentAnnotation: mustBuildTraceParent(traceID, spanID),
	}
}

func mustBuildTraceParent(traceID, spanID string) string {
	traceParent, err := tracecontext.TraceParentFromIDs(traceID, spanID)
	if err != nil {
		panic(err)
	}
	return traceParent
}

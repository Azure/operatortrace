package tracingqueue

import (
	"testing"

	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/types"
	ctrlreconcile "sigs.k8s.io/controller-runtime/pkg/reconcile"

	tracingtypes "github.com/Azure/operatortrace/operatortrace-go/pkg/types"
)

func TestAppendLinkedSpan(t *testing.T) {
	req := &tracingtypes.RequestWithTraceID{
		LinkedSpans:     [10]tracingtypes.LinkedSpan{},
		LinkedSpanCount: 0,
	}

	span1 := tracingtypes.LinkedSpan{TraceID: "1", SpanID: "a"}
	span2 := tracingtypes.LinkedSpan{TraceID: "2", SpanID: "b"}
	span3 := tracingtypes.LinkedSpan{TraceID: "3", SpanID: "c"}
	spanEmpty := tracingtypes.LinkedSpan{}

	// Start: add two spans
	appendLinkedSpan(req, span1)
	appendLinkedSpan(req, span2)

	require.Equal(t, 2, req.LinkedSpanCount)
	require.ElementsMatch(t, []tracingtypes.LinkedSpan{span1, span2}, req.LinkedSpans[:req.LinkedSpanCount])

	// Add third, expect three
	appendLinkedSpan(req, span3)

	require.Equal(t, 3, req.LinkedSpanCount)
	require.ElementsMatch(t, []tracingtypes.LinkedSpan{span1, span2, span3}, req.LinkedSpans[:req.LinkedSpanCount])

	// Try to add a duplicate
	appendLinkedSpan(req, span1)
	require.Equal(t, 3, req.LinkedSpanCount)
	require.ElementsMatch(t, []tracingtypes.LinkedSpan{span1, span2, span3}, req.LinkedSpans[:req.LinkedSpanCount])

	// Try to add an empty linked span
	appendLinkedSpan(req, spanEmpty)
	require.Equal(t, 3, req.LinkedSpanCount)
	require.ElementsMatch(t, []tracingtypes.LinkedSpan{span1, span2, span3}, req.LinkedSpans[:req.LinkedSpanCount])
}

func TestTracingQueuePrefersLatestParentForDuplicateKey(t *testing.T) {
	queue := NewTracingQueue()
	key := types.NamespacedName{Namespace: "default", Name: "sample1"}
	req1 := newRequest(key, tracingtypes.RequestParent{TraceID: "trace-old", SpanID: "span-old", Name: "sample1", Kind: "Sample", EventKind: "Update"})
	req2 := newRequest(key, tracingtypes.RequestParent{TraceID: "trace-new", SpanID: "span-new", Name: "sample1", Kind: "Sample", EventKind: "Update"})

	queue.Add(req1)
	queue.Add(req2)

	got, shutdown := queue.Get()
	require.False(t, shutdown)
	require.Equal(t, "trace-new", got.Parent.TraceID)
	require.Equal(t, "span-new", got.Parent.SpanID)
	require.Equal(t, 1, got.LinkedSpanCount)
	require.Equal(t, tracingtypes.LinkedSpan{TraceID: "trace-old", SpanID: "span-old"}, got.LinkedSpans[0])
	queue.Done(got)
}

func TestTracingQueueUsesLatestParentAfterDoneAndReAdd(t *testing.T) {
	queue := NewTracingQueue()
	key := types.NamespacedName{Namespace: "default", Name: "sample1"}
	req1 := newRequest(key, tracingtypes.RequestParent{TraceID: "trace-1", SpanID: "span-1", Name: "sample1", Kind: "Sample", EventKind: "Create"})
	req2 := newRequest(key, tracingtypes.RequestParent{TraceID: "trace-2", SpanID: "span-2", Name: "sample1", Kind: "Sample", EventKind: "Update"})

	queue.Add(req1)
	first, shutdown := queue.Get()
	require.False(t, shutdown)
	queue.Done(first)

	queue.Add(req2)
	got, shutdown := queue.Get()
	require.False(t, shutdown)
	require.Equal(t, "trace-2", got.Parent.TraceID)
	require.Equal(t, "span-2", got.Parent.SpanID)
	require.Equal(t, 0, got.LinkedSpanCount)
	queue.Done(got)
}

func newRequest(key types.NamespacedName, parent tracingtypes.RequestParent) tracingtypes.RequestWithTraceID {
	return tracingtypes.RequestWithTraceID{
		Request: ctrlreconcile.Request{NamespacedName: key},
		Parent:  parent,
	}
}

package tracingqueue

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/types"

	tracingtypes "github.com/Azure/operatortrace/operatortrace-go/pkg/types"
	ctrlreconcile "sigs.k8s.io/controller-runtime/pkg/reconcile"
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

func TestTracingQueueAddRequeuesWhileProcessing(t *testing.T) {
	tq := NewTracingQueue()
	defer tq.ShutDown()

	req := tracingtypes.RequestWithTraceID{
		Request: ctrlreconcile.Request{NamespacedName: types.NamespacedName{Name: "foo", Namespace: "default"}},
	}

	tq.Add(req)
	first, shutdown := tq.Get()
	require.False(t, shutdown)

	updated := req
	updated.Parent.TraceID = "trace-1"
	updated.Parent.SpanID = "span-1"
	tq.Add(updated)

	tq.Done(first)

	resultCh := make(chan tracingtypes.RequestWithTraceID, 1)
	go func() {
		next, _ := tq.Get()
		resultCh <- next
	}()

	select {
	case next := <-resultCh:
		require.Equal(t, req.Request.NamespacedName, next.Request.NamespacedName)
		tq.Done(next)
	case <-time.After(250 * time.Millisecond):
		t.Fatalf("expected queue to re-deliver key when Add occurs during processing")
	}
}

func TestTracingQueueAddAfterSchedulesWhenInFlight(t *testing.T) {
	tq := NewTracingQueue()
	defer tq.ShutDown()

	req := tracingtypes.RequestWithTraceID{
		Request: ctrlreconcile.Request{NamespacedName: types.NamespacedName{Name: "bar", Namespace: "default"}},
	}

	tq.Add(req)
	first, _ := tq.Get()

	tq.AddAfter(req, 10*time.Millisecond)

	tq.Done(first)

	resultCh := make(chan tracingtypes.RequestWithTraceID, 1)
	go func() {
		next, _ := tq.Get()
		resultCh <- next
	}()

	select {
	case next := <-resultCh:
		require.Equal(t, req.Request.NamespacedName, next.Request.NamespacedName)
		tq.Done(next)
	case <-time.After(300 * time.Millisecond):
		t.Fatalf("expected AddAfter to schedule key even while previous reconcile in-flight")
	}
}

func TestTracingQueueAddRateLimitedWhileProcessing(t *testing.T) {
	tq := NewTracingQueue()
	defer tq.ShutDown()

	req := tracingtypes.RequestWithTraceID{
		Request: ctrlreconcile.Request{NamespacedName: types.NamespacedName{Name: "baz", Namespace: "default"}},
	}

	tq.Add(req)
	first, _ := tq.Get()

	retry := req
	retry.Parent.TraceID = "retry-trace"
	retry.Parent.SpanID = "retry-span"
	tq.AddRateLimited(retry)

	tq.Done(first)

	resultCh := make(chan tracingtypes.RequestWithTraceID, 1)
	go func() {
		next, _ := tq.Get()
		resultCh <- next
	}()

	select {
	case next := <-resultCh:
		require.Equal(t, req.Request.NamespacedName, next.Request.NamespacedName)
		tq.Forget(next)
		tq.Done(next)
	case <-time.After(500 * time.Millisecond):
		t.Fatalf("expected AddRateLimited to requeue key while processing")
	}
}

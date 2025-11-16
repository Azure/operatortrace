// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// pkg/tracingqueue/tracingqueue.go

package tracingqueue

import (
	"sync"
	"time"

	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/workqueue"

	tracingtypes "github.com/Azure/operatortrace/operatortrace-go/pkg/types"
	ctrlreconcile "sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// TracingQueue wraps a typed workqueue and a map to provide deduplication and value merging.
type TracingQueue struct {
	queue       workqueue.TypedRateLimitingInterface[types.NamespacedName]
	mu          sync.Mutex
	m           map[types.NamespacedName]*tracingtypes.RequestWithTraceID
	softDeleted map[types.NamespacedName]*tracingtypes.RequestWithTraceID
}

// NewTracingQueue creates a new TracingQueue instance using generics and the recommended rate limiter.
func NewTracingQueue() *TracingQueue {
	return &TracingQueue{
		queue: workqueue.NewTypedRateLimitingQueue(
			workqueue.DefaultTypedControllerRateLimiter[types.NamespacedName](),
		),
		m:           make(map[types.NamespacedName]*tracingtypes.RequestWithTraceID),
		softDeleted: make(map[types.NamespacedName]*tracingtypes.RequestWithTraceID),
	}
}

var _ workqueue.TypedRateLimitingInterface[tracingtypes.RequestWithTraceID] = (*TracingQueue)(nil)

// Add adds or merges a tracing request into the queue, deduping by key.
func (tq *TracingQueue) Add(req tracingtypes.RequestWithTraceID) {
	tq.mu.Lock()
	defer tq.mu.Unlock()

	if _, found := tq.m[req.NamespacedName]; found {
		existing := tq.m[req.NamespacedName]
		if existing.Parent.TraceID != req.Parent.TraceID || existing.Parent.SpanID != req.Parent.SpanID {
			newLinkedSpan := tracingtypes.LinkedSpan{
				TraceID: req.Parent.TraceID,
				SpanID:  req.Parent.SpanID,
			}
			appendLinkedSpan(existing, newLinkedSpan)
		}
		// Mark dirty in underlying queue so it requeues after Done()
		tq.queue.Add(req.NamespacedName)
	} else {
		tval := req // Copy, to avoid retaining the caller's pointer.
		tq.m[req.NamespacedName] = &tval
		tq.queue.Add(req.NamespacedName)
	}
}

// AddAfter adds or merges a tracing request into the queue, deduping by key, with a delay.
func (tq *TracingQueue) AddAfter(req tracingtypes.RequestWithTraceID, duration time.Duration) {
	tq.mu.Lock()
	defer tq.mu.Unlock()

	if _, found := tq.m[req.NamespacedName]; !found {
		// Don't link to any previous span
		tval := req
		req.LinkedSpanCount = 0
		req.LinkedSpans = [10]tracingtypes.LinkedSpan{} // Reset linked spans
		req.Parent = tracingtypes.RequestParent{}
		tq.m[req.NamespacedName] = &tval
		tq.queue.AddAfter(req.NamespacedName, duration)
	}

	// If the request already exists, we do not update it here.
}

// AddRateLimited adds or merges a tracing request into the queue, deduping by key, with rate limiting.
func (tq *TracingQueue) AddRateLimited(req tracingtypes.RequestWithTraceID) {
	tq.mu.Lock()
	defer tq.mu.Unlock()

	// This is usually called after an error so keeping it linked to the previous span.
	if _, found := tq.m[req.NamespacedName]; found {
		existing := tq.m[req.NamespacedName]
		if existing.Parent.TraceID != req.Parent.TraceID || existing.Parent.SpanID != req.Parent.SpanID {
			newLinkedSpan := tracingtypes.LinkedSpan{
				TraceID: req.Parent.TraceID,
				SpanID:  req.Parent.SpanID,
			}
			appendLinkedSpan(existing, newLinkedSpan)
		}
		// Mark dirty in underlying queue so it requeues after Done()
		tq.queue.AddRateLimited(req.NamespacedName)
	} else {
		tval := req
		tq.m[req.NamespacedName] = &tval
		tq.queue.AddRateLimited(req.NamespacedName)
	}
}

// Forget removes a tracing request from the queue, if it exists.
func (tq *TracingQueue) Forget(req tracingtypes.RequestWithTraceID) {
	tq.mu.Lock()
	defer tq.mu.Unlock()

	if _, found := tq.m[req.NamespacedName]; found {
		delete(tq.m, req.NamespacedName)
		tq.queue.Forget(req.NamespacedName)
	}
}

// Len returns the number of items in the queue.
func (tq *TracingQueue) Len() int {
	tq.mu.Lock()
	defer tq.mu.Unlock()
	return len(tq.m)
}

// NumRequeues returns the number of requeues for a given request.
func (tq *TracingQueue) NumRequeues(req tracingtypes.RequestWithTraceID) int {
	// Since we are using a map to store the requests, we don't track requeues.
	// This is a no-op in this implementation.
	return 0
}

// ShutDownWithDrain stops accepting new work and drains the queue.
func (tq *TracingQueue) ShutDownWithDrain() {
	tq.queue.ShutDownWithDrain()
	tq.mu.Lock()
	defer tq.mu.Unlock()
	// Clear the maps when shutting down.
	for key := range tq.m {
		delete(tq.m, key)
	}
	for key := range tq.softDeleted {
		delete(tq.softDeleted, key)
	}
}

// Get returns and removes the next queued TracingRequest (merged value).
// Returns shutdown=true when queue is shutting down.
func (tq *TracingQueue) Get() (req tracingtypes.RequestWithTraceID, shutdown bool) {
	key, shutdown := tq.queue.Get()
	if shutdown {
		return tracingtypes.RequestWithTraceID{}, true
	}

	tq.mu.Lock()
	defer tq.mu.Unlock()
	valPtr, found := tq.m[key]
	if found && valPtr != nil {
		return *valPtr, false
	}
	// Check softDeleted map
	softPtr, softFound := tq.softDeleted[key]
	if softFound && softPtr != nil {
		return *softPtr, false
	}
	// Key not found in either map
	return tracingtypes.RequestWithTraceID{
		Request: ctrlreconcile.Request{
			NamespacedName: key,
		},
	}, false
}

// Done notifies the underlying queue that you're done with this key (for rate limiting).
func (tq *TracingQueue) Done(req tracingtypes.RequestWithTraceID) {
	tq.mu.Lock()
	defer tq.mu.Unlock()
	tq.queue.Done(req.NamespacedName)
	if val, found := tq.m[req.NamespacedName]; found {
		tq.softDeleted[req.NamespacedName] = val
		delete(tq.m, req.NamespacedName)
	}
}

// ShutDown stops accepting new work and shuts down the queue.
func (tq *TracingQueue) ShutDown() {
	tq.queue.ShutDown()
}

// ShuttingDown reports if the queue is shutting down.
func (tq *TracingQueue) ShuttingDown() bool {
	return tq.queue.ShuttingDown()
}

func appendLinkedSpan(req *tracingtypes.RequestWithTraceID, span tracingtypes.LinkedSpan) {
	// Don't add empty linked spans
	if len(span.TraceID) == 0 && len(span.SpanID) == 0 {
		return
	}

	for i := 0; i < req.LinkedSpanCount; i++ {
		if req.LinkedSpans[i] == span {
			return // Already present, skip duplicate
		}
	}
	if req.LinkedSpanCount < len(req.LinkedSpans) {
		req.LinkedSpans[req.LinkedSpanCount] = span
		req.LinkedSpanCount++
	}
}

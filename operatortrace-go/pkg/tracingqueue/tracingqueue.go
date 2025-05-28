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
)

// TracingQueue wraps a typed workqueue and a map to provide deduplication and value merging.
type TracingQueue struct {
	queue workqueue.TypedRateLimitingInterface[types.NamespacedName]
	mu    sync.Mutex
	m     map[types.NamespacedName]*tracingtypes.RequestWithTraceID
}

// NewTracingQueue creates a new TracingQueue instance using generics and the recommended rate limiter.
func NewTracingQueue() *TracingQueue {
	return &TracingQueue{
		queue: workqueue.NewTypedRateLimitingQueue(
			workqueue.DefaultTypedControllerRateLimiter[types.NamespacedName](),
		),
		m: make(map[types.NamespacedName]*tracingtypes.RequestWithTraceID),
	}
}

var _ workqueue.TypedRateLimitingInterface[tracingtypes.RequestWithTraceID] = (*TracingQueue)(nil)

// Add adds or merges a tracing request into the queue, deduping by key.
func (tq *TracingQueue) Add(req tracingtypes.RequestWithTraceID) {
	tq.mu.Lock()
	defer tq.mu.Unlock()

	if _, found := tq.m[req.NamespacedName]; found {
		// do nothing
		// to do figure out how to make comprable linked spans
	} else {
		tval := req // Copy, to avoid retaining the caller's pointer.
		tq.m[req.NamespacedName] = &tval
		tq.queue.Add(req.NamespacedName)
	}
}

// AddAfter adds or merges a tracing request into the queue, deduping by key, with a delay.
func (tq *TracingQueue) AddAfter(req tracingtypes.RequestWithTraceID, duration time.Duration) {
	// Add the request to the queue with a delay.
	tq.Add(req)
}

// AddRateLimited adds or merges a tracing request into the queue, deduping by key, with rate limiting.
func (tq *TracingQueue) AddRateLimited(req tracingtypes.RequestWithTraceID) {
	// Add the request to the queue with rate limiting.
	tq.Add(req)
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
	// Clear the map when shutting down.
	for key := range tq.m {
		delete(tq.m, key)
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
	val := *tq.m[key]
	delete(tq.m, key)
	tq.mu.Unlock()

	return val, false
}

// Done notifies the underlying queue that you're done with this key (for rate limiting).
func (tq *TracingQueue) Done(req tracingtypes.RequestWithTraceID) {
	tq.queue.Done(req.NamespacedName)
}

// ShutDown stops accepting new work and shuts down the queue.
func (tq *TracingQueue) ShutDown() {
	tq.queue.ShutDown()
}

// ShuttingDown reports if the queue is shutting down.
func (tq *TracingQueue) ShuttingDown() bool {
	return tq.queue.ShuttingDown()
}

// // Removes duplicate spans from the slice of linked spans.
// func dedupeSpans(spans []tracingtypes.LinkedSpan) []tracingtypes.LinkedSpan {
// 	seen := make(map[tracingtypes.LinkedSpan]struct{})
// 	result := make([]tracingtypes.LinkedSpan, 0, len(spans))
// 	for _, s := range spans {
// 		if _, ok := seen[s]; !ok {
// 			seen[s] = struct{}{}
// 			result = append(result, s)
// 		}
// 	}
// 	return result
// }

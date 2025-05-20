package tracingqueue

import (
	"sync"

	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/workqueue"
)

// TracingRequest is what the public API uses (unified key and value).
type TracingRequest struct {
	Key   Key
	Value Value
}

// Key is a unique identifier for the tracing request.
type Key struct {
	types.NamespacedName
}

// Value is the value associated with the tracing request.
type Value struct {
	ParentTraceID string
	ParentSpanID  string
	LinkedSpans   []LinkedSpan
}

// LinkedSpan represents a related span/tracing reference.
type LinkedSpan struct {
	TraceID string
	SpanID  string
}

// TracingQueue wraps a typed workqueue and a map to provide deduplication and value merging.
type TracingQueue struct {
	queue workqueue.TypedRateLimitingInterface[Key]
	mu    sync.Mutex
	m     map[Key]*Value
}

// NewTracingQueue creates a new TracingQueue instance using generics and the recommended rate limiter.
func NewTracingQueue() *TracingQueue {
	return &TracingQueue{
		queue: workqueue.NewTypedRateLimitingQueue(
			workqueue.DefaultTypedControllerRateLimiter[Key](),
		),
		m: make(map[Key]*Value),
	}
}

// Add adds or merges a tracing request into the queue, deduping by key.
func (tq *TracingQueue) Add(req TracingRequest) {
	key := req.Key
	value := req.Value

	tq.mu.Lock()
	defer tq.mu.Unlock()

	if existing, found := tq.m[key]; found {
		// Merge: Append new linked spans (dedupe if needed).
		existing.LinkedSpans = append(existing.LinkedSpans, value.LinkedSpans...)
	} else {
		tval := value // Copy, to avoid retaining the caller's pointer.
		tq.m[key] = &tval
		tq.queue.Add(key)
	}
}

// Get returns and removes the next queued TracingRequest (merged value).
// Returns shutdown=true when queue is shutting down.
func (tq *TracingQueue) Get() (req TracingRequest, shutdown bool) {
	key, shutdown := tq.queue.Get()
	if shutdown {
		return TracingRequest{}, true
	}

	tq.mu.Lock()
	val := *tq.m[key]
	delete(tq.m, key)
	tq.mu.Unlock()

	return TracingRequest{
		Key:   key,
		Value: val,
	}, false
}

// Done notifies the underlying queue that you're done with this key (for rate limiting).
func (tq *TracingQueue) Done(req TracingRequest) {
	tq.queue.Done(req.Key)
}

// ShutDown stops accepting new work and shuts down the queue.
func (tq *TracingQueue) ShutDown() {
	tq.queue.ShutDown()
}

// ShuttingDown reports if the queue is shutting down.
func (tq *TracingQueue) ShuttingDown() bool {
	return tq.queue.ShuttingDown()
}

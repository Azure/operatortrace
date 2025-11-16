// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// pkg/client/annotations.go

package client

import (
	"context"
	"time"

	"github.com/Azure/operatortrace/operatortrace-go/pkg/constants"
	"github.com/Azure/operatortrace/operatortrace-go/pkg/tracecontext"
	tracingtypes "github.com/Azure/operatortrace/operatortrace-go/pkg/types"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type storedTraceContext struct {
	TraceParent  string
	TraceState   string
	Timestamp    time.Time
	Relationship TraceParentRelationship
}

// addTraceAnnotations stores the current span context on the kubernetes object using traceparent/tracestate.
func addTraceAnnotations(ctx context.Context, obj client.Object, opts Options) {
	span := trace.SpanFromContext(ctx)
	spanContext := span.SpanContext()
	if !spanContext.IsValid() {
		return
	}

	annotations := ensureAnnotations(obj)
	carrier := propagation.MapCarrier{}
	propagator := otel.GetTextMapPropagator()
	propagator.Inject(trace.ContextWithSpanContext(context.Background(), spanContext), carrier)
	if traceState, err := tracecontext.BuildTraceStateString(spanContext, opts.traceStateTimestampKey(), time.Now()); err == nil && traceState != "" {
		carrier["tracestate"] = traceState
	}
	persistTraceCarrier(annotations, opts, carrier["traceparent"], carrier["tracestate"])
	obj.SetAnnotations(annotations)
}

// overrideTraceContextFromRequest persists the trace context from the request struct onto the object annotations.
func overrideTraceContextFromRequest(request tracingtypes.RequestWithTraceID, obj client.Object, opts Options) {
	parent := request.Parent
	if parent.TraceID == "" || parent.SpanID == "" {
		return
	}
	traceParent, err := tracecontext.TraceParentFromIDs(parent.TraceID, parent.SpanID)
	if err != nil || traceParent == "" {
		return
	}

	annotations := ensureAnnotations(obj)
	persistTraceCarrier(annotations, opts, traceParent, "")
	obj.SetAnnotations(annotations)
}

func ensureAnnotations(obj client.Object) map[string]string {
	annotations := obj.GetAnnotations()
	if annotations == nil {
		annotations = map[string]string{}
		obj.SetAnnotations(annotations)
	}
	return annotations
}

func extractTraceContextFromAnnotations(annotations map[string]string, opts Options) (storedTraceContext, bool) {
	baseCfg := tracecontext.AnnotationExtractionConfig{
		LegacyTraceIDKey:       opts.legacyTraceIDAnnotationKey(),
		LegacySpanIDKey:        opts.legacySpanIDAnnotationKey(),
		LegacyTimestampKey:     opts.legacyTraceTimeAnnotationKey(),
		TraceStateTimestampKey: opts.traceStateTimestampKey(),
	}

	type candidate struct {
		parentKey    string
		stateKey     string
		relationship TraceParentRelationship
	}

	emittedParentKey := opts.emittedTraceParentAnnotationKey()
	emittedStateKey := opts.emittedTraceStateAnnotationKey()
	defaultParentKey := constants.DefaultTraceParentAnnotation
	defaultStateKey := constants.DefaultTraceStateAnnotation

	candidates := []candidate{
		{
			parentKey:    opts.IncomingTraceParentAnnotation,
			stateKey:     opts.IncomingTraceStateAnnotation,
			relationship: opts.IncomingTraceRelationship,
		},
		{
			parentKey:    emittedParentKey,
			stateKey:     emittedStateKey,
			relationship: TraceParentRelationshipParent,
		},
	}
	if defaultParentKey != emittedParentKey || defaultStateKey != emittedStateKey {
		candidates = append(candidates, candidate{
			parentKey:    defaultParentKey,
			stateKey:     defaultStateKey,
			relationship: TraceParentRelationshipParent,
		})
	}

	for _, cand := range candidates {
		if cand.parentKey == "" {
			continue
		}
		cfg := baseCfg
		cfg.TraceParentKey = cand.parentKey
		cfg.TraceStateKey = cand.stateKey
		if result, ok := tracecontext.ExtractTraceContextFromAnnotations(annotations, cfg); ok {
			relationship := cand.relationship
			if relationship == "" {
				relationship = opts.IncomingTraceRelationship
			}
			return storedTraceContext{
				TraceParent:  result.TraceParent,
				TraceState:   result.TraceState,
				Timestamp:    result.Timestamp,
				Relationship: relationship,
			}, true
		}
	}

	if result, ok := tracecontext.ExtractTraceContextFromAnnotations(annotations, baseCfg); ok {
		return storedTraceContext{
			TraceParent:  result.TraceParent,
			TraceState:   result.TraceState,
			Timestamp:    result.Timestamp,
			Relationship: TraceParentRelationshipParent,
		}, true
	}

	return storedTraceContext{}, false
}

func persistTraceCarrier(annotations map[string]string, opts Options, traceParent, traceState string) {
	pruneLegacyTraceAnnotations(annotations, opts)
	if traceParent != "" {
		annotations[opts.emittedTraceParentAnnotationKey()] = traceParent
	} else {
		delete(annotations, opts.emittedTraceParentAnnotationKey())
	}
	if traceState != "" {
		annotations[opts.emittedTraceStateAnnotationKey()] = traceState
	} else {
		delete(annotations, opts.emittedTraceStateAnnotationKey())
	}
}

func pruneLegacyTraceAnnotations(annotations map[string]string, opts Options) {
	delete(annotations, opts.legacyTraceIDAnnotationKey())
	delete(annotations, opts.legacySpanIDAnnotationKey())
	delete(annotations, opts.legacyTraceTimeAnnotationKey())
}

func traceContextExpired(ts time.Time, opts Options) bool {
	if ts.IsZero() {
		return false
	}
	return time.Since(ts) > opts.traceExpiration()
}

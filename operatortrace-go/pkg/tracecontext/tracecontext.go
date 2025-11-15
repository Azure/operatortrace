// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// pkg/tracecontext/tracecontext.go

package tracecontext

import (
	"context"
	"fmt"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

// AnnotationExtractionConfig describes how to read trace context data from annotations.
type AnnotationExtractionConfig struct {
	TraceParentKeys        []string
	TraceStateKeys         []string
	LegacyTraceIDKey       string
	LegacySpanIDKey        string
	LegacyTimestampKey     string
	TraceStateTimestampKey string
}

// AnnotationTraceContext captures the reconstructed trace context from annotations.
type AnnotationTraceContext struct {
	TraceParent string
	TraceState  string
	Timestamp   time.Time
}

// TraceParentFromIDs constructs a traceparent header string from trace/span IDs.
func TraceParentFromIDs(traceIDHex, spanIDHex string) (string, error) {
	if traceIDHex == "" || spanIDHex == "" {
		return "", fmt.Errorf("missing trace or span id")
	}
	traceID, err := trace.TraceIDFromHex(traceIDHex)
	if err != nil {
		return "", err
	}
	spanID, err := trace.SpanIDFromHex(spanIDHex)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("00-%s-%s-01", traceID.String(), spanID.String()), nil
}

// SpanContextFromTraceData reconstructs a span context from traceparent/tracestate strings.
func SpanContextFromTraceData(traceParent, traceState string) (trace.SpanContext, error) {
	if traceParent == "" {
		return trace.SpanContext{}, fmt.Errorf("missing traceparent")
	}
	carrier := propagation.MapCarrier{}
	carrier["traceparent"] = traceParent
	if traceState != "" {
		carrier["tracestate"] = traceState
	}
	ctx := otel.GetTextMapPropagator().Extract(context.Background(), carrier)
	spanContext := trace.SpanContextFromContext(ctx)
	if !spanContext.IsValid() {
		return trace.SpanContext{}, fmt.Errorf("invalid trace context")
	}
	return spanContext, nil
}

// ExtractTimestampFromTraceState fetches a timestamp value out of tracestate.
func ExtractTimestampFromTraceState(raw, key string) (time.Time, bool) {
	if raw == "" || key == "" {
		return time.Time{}, false
	}
	ts, err := trace.ParseTraceState(raw)
	if err != nil {
		return time.Time{}, false
	}
	value := ts.Get(key)
	if value == "" {
		return time.Time{}, false
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, false
	}
	return parsed, true
}

// BuildTraceStateString inserts or updates the timestamp value inside tracestate.
func BuildTraceStateString(sc trace.SpanContext, timestampKey string, now time.Time) (string, error) {
	traceState := sc.TraceState()
	if timestampKey != "" {
		traceState = traceState.Delete(timestampKey)
		var err error
		traceState, err = traceState.Insert(timestampKey, now.UTC().Format(time.RFC3339Nano))
		if err != nil {
			return "", err
		}
	}
	return traceState.String(), nil
}

// ExtractTraceContextFromAnnotations attempts to read trace context information using the provided config.
func ExtractTraceContextFromAnnotations(annotations map[string]string, cfg AnnotationExtractionConfig) (AnnotationTraceContext, bool) {
	if len(annotations) == 0 {
		return AnnotationTraceContext{}, false
	}

	if traceParent := firstAnnotationValue(annotations, cfg.TraceParentKeys...); traceParent != "" {
		traceState := firstAnnotationValue(annotations, cfg.TraceStateKeys...)
		var timestamp time.Time
		if cfg.TraceStateTimestampKey != "" {
			if ts, ok := ExtractTimestampFromTraceState(traceState, cfg.TraceStateTimestampKey); ok {
				timestamp = ts
			}
		}
		return AnnotationTraceContext{TraceParent: traceParent, TraceState: traceState, Timestamp: timestamp}, true
	}

	if cfg.LegacyTraceIDKey == "" || cfg.LegacySpanIDKey == "" {
		return AnnotationTraceContext{}, false
	}
	traceID := annotations[cfg.LegacyTraceIDKey]
	spanID := annotations[cfg.LegacySpanIDKey]
	if traceID == "" || spanID == "" {
		return AnnotationTraceContext{}, false
	}
	traceParent, err := TraceParentFromIDs(traceID, spanID)
	if err != nil {
		return AnnotationTraceContext{}, false
	}
	var timestamp time.Time
	if cfg.LegacyTimestampKey != "" {
		if legacyTime := annotations[cfg.LegacyTimestampKey]; legacyTime != "" {
			if parsed, err := time.Parse(time.RFC3339, legacyTime); err == nil {
				timestamp = parsed
			}
		}
	}
	return AnnotationTraceContext{TraceParent: traceParent, Timestamp: timestamp}, true
}

func firstAnnotationValue(annotations map[string]string, keys ...string) string {
	for _, key := range keys {
		if key == "" {
			continue
		}
		if value := annotations[key]; value != "" {
			return value
		}
	}
	return ""
}

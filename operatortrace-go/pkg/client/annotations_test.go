package client

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Azure/operatortrace/operatortrace-go/pkg/tracecontext"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

func TestExtractTraceContextRelationshipSelection(t *testing.T) {
	opts := NewOptions()

	traceParent, err := tracecontext.TraceParentFromIDs("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "bbbbbbbbbbbbbbbb")
	require.NoError(t, err)

	annotations := map[string]string{
		opts.emittedTraceParentAnnotationKey(): traceParent,
	}

	stored, ok := extractTraceContextFromAnnotations(annotations, opts)
	require.True(t, ok)
	require.Equal(t, TraceParentRelationshipParent, stored.Relationship)
}

func TestExtractTraceContextRespectsIncomingRelationship(t *testing.T) {
	opts := NewOptions(
		WithIncomingTraceParentAnnotation("external/traceparent"),
		WithIncomingTraceStateAnnotation("external/tracestate"),
		WithIncomingTraceRelationship(TraceParentRelationshipLink),
	)

	traceParent, err := tracecontext.TraceParentFromIDs("cccccccccccccccccccccccccccccccc", "dddddddddddddddd")
	require.NoError(t, err)

	annotations := map[string]string{
		"external/traceparent": traceParent,
	}

	stored, ok := extractTraceContextFromAnnotations(annotations, opts)
	require.True(t, ok)
	require.Equal(t, TraceParentRelationshipLink, stored.Relationship)
}

func TestApplyStoredTraceContextUsesRelationship(t *testing.T) {
	otel.SetTextMapPropagator(propagation.TraceContext{})

	traceParent, err := tracecontext.TraceParentFromIDs("eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee", "ffffffffffffffff")
	require.NoError(t, err)

	stored := storedTraceContext{
		TraceParent:  traceParent,
		Relationship: TraceParentRelationshipParent,
	}

	ctx, link := applyStoredTraceContext(context.Background(), stored, NewOptions(), nil)
	require.Nil(t, link)

	sc := trace.SpanContextFromContext(ctx)
	require.True(t, sc.IsValid())
	require.True(t, sc.IsRemote())
	require.Equal(t, "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee", sc.TraceID().String())

	// When relationship is a link, context should remain untouched and a link returned
	storedLink := storedTraceContext{TraceParent: traceParent, Relationship: TraceParentRelationshipLink}
	ctxNoop, linkPtr := applyStoredTraceContext(context.Background(), storedLink, NewOptions(), nil)
	require.NotNil(t, linkPtr)
	require.False(t, trace.SpanContextFromContext(ctxNoop).IsValid())
}

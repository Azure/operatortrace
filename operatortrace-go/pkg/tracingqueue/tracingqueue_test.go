package tracingqueue

import (
	"testing"

	"github.com/stretchr/testify/require"

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
}

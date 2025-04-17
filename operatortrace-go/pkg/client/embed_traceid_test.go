// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// pkg/client/embed_traceid_test.go

package client

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestEmbedTraceID_ToString(t *testing.T) {
	embed := &EmbedTraceID{
		TraceID:    "trace-123",
		SpanID:     "span-456",
		ObjectKind: "Deployment",
		ObjectName: "my-deployment",
		KeyName:    "original-key",
	}

	expected := "trace-123;span-456;Deployment;my-deployment;original-key"
	assert.Equal(t, expected, embed.ToString())
}

func TestEmbedTraceID_FromString(t *testing.T) {
	t.Run("valid string", func(t *testing.T) {
		input := "trace-789;span-012;Pod;my-pod;pod-key"
		expected := &EmbedTraceID{
			TraceID:    "trace-789",
			SpanID:     "span-012",
			ObjectKind: "Pod",
			ObjectName: "my-pod",
			KeyName:    "pod-key",
		}

		actual := &EmbedTraceID{}
		err := actual.FromString(input)

		assert.NoError(t, err)
		assert.Equal(t, expected, actual)
	})

	t.Run("invalid string format", func(t *testing.T) {
		input := "trace-789;span-012;Pod;my-pod" // Missing one part

		actual := &EmbedTraceID{}
		err := actual.FromString(input)

		assert.Error(t, err)
		assert.EqualError(t, err, "invalid string format")
	})
}

func TestEmbedTraceID_ToStringAndFromString_RoundTrip(t *testing.T) {
	original := &EmbedTraceID{
		TraceID:    "trace-abc",
		SpanID:     "span-def",
		ObjectKind: "Service",
		ObjectName: "my-service",
		KeyName:    "service-key",
	}

	str := original.ToString()

	parsed := &EmbedTraceID{}
	err := parsed.FromString(str)

	assert.NoError(t, err)
	assert.Equal(t, original, parsed)
}

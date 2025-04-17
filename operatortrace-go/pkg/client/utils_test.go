// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package client

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func TestConvertToString(t *testing.T) {
	tests := []struct {
		name     string
		input    interface{}
		expected string
		wantErr  bool
	}{
		{"string input", "hello", "hello", false},
		{"fmt.Stringer input", client.ObjectKey{Namespace: "default", Name: "key"}, "default/key", false},
		{"unsupported type input", 12345, "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := convertToString(tt.input)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expected, result)
			}
		})
	}
}

func TestGetCallerKindFromNamespacedName(t *testing.T) {
	key := client.ObjectKey{Name: "traceid;spanid;Configmap;my-config;keyname"}
	assert.Equal(t, "Configmap", getCallerKindFromNamespacedName(key))

	keyInvalid := client.ObjectKey{Name: "invalid-format"}
	assert.Equal(t, "", getCallerKindFromNamespacedName(keyInvalid))
}

func TestGetCallerNameFromNamespacedName(t *testing.T) {
	key := client.ObjectKey{Name: "traceid;spanid;Configmap;my-config;keyname"}
	assert.Equal(t, "my-config", getCallerNameFromNamespacedName(key))

	keyInvalid := client.ObjectKey{Name: "invalid-format"}
	assert.Equal(t, "", getCallerNameFromNamespacedName(keyInvalid))
}

func TestGetNameFromNamespacedName(t *testing.T) {
	key := client.ObjectKey{Name: "traceid;spanid;Configmap;my-config;original-name"}
	assert.Equal(t, "original-name", getNameFromNamespacedName(key))

	keyInvalid := client.ObjectKey{Name: "not-formatted-name"}
	assert.Equal(t, "not-formatted-name", getNameFromNamespacedName(keyInvalid))
}

func TestEmbedTraceID_ToStringAndFromString(t *testing.T) {
	original := &EmbedTraceID{
		TraceID:    "traceid",
		SpanID:     "spanid",
		ObjectKind: "Deployment",
		ObjectName: "my-deployment",
		KeyName:    "original-key",
	}

	str := original.ToString()
	expectedStr := "traceid;spanid;Deployment;my-deployment;original-key"
	assert.Equal(t, expectedStr, str)

	parsed := &EmbedTraceID{}
	err := parsed.FromString(str)
	assert.NoError(t, err)
	assert.Equal(t, original, parsed)

	err = parsed.FromString("invalid-format-string")
	assert.Error(t, err)
}

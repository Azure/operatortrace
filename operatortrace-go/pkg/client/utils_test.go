// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// pkg/client/utils_test.go

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

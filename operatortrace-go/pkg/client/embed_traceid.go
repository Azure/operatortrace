// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// pkg/client/embed_traceid.go

package client

import (
	"fmt"
	"strings"
)

type EmbedTraceID struct {
	TraceID    string
	SpanID     string
	ObjectKind string
	ObjectName string
	KeyName    string
}

func (e *EmbedTraceID) ToString() string {
	return fmt.Sprintf("%s;%s;%s;%s;%s", e.TraceID, e.SpanID, e.ObjectKind, e.ObjectName, e.KeyName)
}

func (e *EmbedTraceID) FromString(s string) error {
	parts := strings.Split(s, ";")
	if len(parts) != 5 {
		return fmt.Errorf("invalid string format")
	}
	e.TraceID = parts[0]
	e.SpanID = parts[1]
	e.ObjectKind = parts[2]
	e.ObjectName = parts[3]
	e.KeyName = parts[4]
	return nil
}

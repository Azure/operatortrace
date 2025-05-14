// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// pkg/types/request.go

package types

import (
	ctrlreconcile "sigs.k8s.io/controller-runtime/pkg/reconcile"
)

type RequestWithTraceID struct {
	ctrlreconcile.Request
	TraceID    string
	SpanID     string
	SenderName string
	SenderKind string
	EventKind  string
}

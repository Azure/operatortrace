// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// pkg/types/request.go

package types

import (
	"sigs.k8s.io/controller-runtime/pkg/builder"
)

// NewControllerManagedBy returns a new controller builder that will be started by the provided Manager.
var NewControllerManagedBy = builder.TypedControllerManagedBy[RequestWithTraceID]

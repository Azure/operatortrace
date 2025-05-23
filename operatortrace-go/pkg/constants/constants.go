// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// pkg/constants/constants.go

package constants

const (
	TraceIDAnnotation     = "operatortrace.azure.microsoft.com/trace-id"
	SpanIDAnnotation      = "operatortrace.azure.microsoft.com/span-id"
	TraceIDTimeAnnotation = "operatortrace.azure.microsoft.com/trace-id-time"
	ResourceVersionKey    = "resourceVersion"
	TraceExpirationTime   = 20 // in minutes
)

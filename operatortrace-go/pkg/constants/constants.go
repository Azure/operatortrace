// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// pkg/constants/constants.go

package constants

import "time"

const (
	// DefaultAnnotationPrefix is the default prefix applied to operatortrace annotations.
	DefaultAnnotationPrefix = "operatortrace.azure.microsoft.com"

	// EmittedTraceParentAnnotationSuffix controls the suffix used for traceparent annotations emitted by operatortrace.
	EmittedTraceParentAnnotationSuffix = "traceparent"
	// EmittedTraceStateAnnotationSuffix controls the suffix used for tracestate annotations emitted by operatortrace.
	EmittedTraceStateAnnotationSuffix = "tracestate"

	DefaultTraceParentAnnotation = DefaultAnnotationPrefix + "/" + EmittedTraceParentAnnotationSuffix
	DefaultTraceStateAnnotation  = DefaultAnnotationPrefix + "/" + EmittedTraceStateAnnotationSuffix
	TraceStateTimestampKey       = "operatortrace_ts"

	// Legacy annotation keys are retained for backwards compatibility and migration logic.
	LegacyTraceIDAnnotation     = DefaultAnnotationPrefix + "/trace-id"
	LegacySpanIDAnnotation      = DefaultAnnotationPrefix + "/span-id"
	LegacyTraceIDTimeAnnotation = DefaultAnnotationPrefix + "/trace-id-time"

	ResourceVersionKey = "resourceVersion"

	// TraceExpirationTime is kept for backward compatibility (minutes).
	TraceExpirationTime = 20
)

const (
	// DefaultTraceExpiration controls how long previously recorded trace context stays valid.
	DefaultTraceExpiration = time.Duration(TraceExpirationTime) * time.Minute
)

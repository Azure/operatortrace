// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// pkg/client/options.go

package client

import (
	"strings"
	"time"

	"github.com/Azure/operatortrace/operatortrace-go/pkg/constants"
)

// TraceParentRelationship controls how an incoming traceparent should be attached to new spans.
type TraceParentRelationship string

const (
	TraceParentRelationshipLink   TraceParentRelationship = "link"
	TraceParentRelationshipParent TraceParentRelationship = "parent"
)

// Options holds configuration for tracing clients and helpers.
type Options struct {
	AnnotationPrefix string
	TraceExpiration  time.Duration

	TraceStateTimestampKey string

	EmittedTraceParentAnnotationSuffix string
	EmittedTraceStateAnnotationSuffix  string

	IncomingTraceParentAnnotation string
	IncomingTraceStateAnnotation  string

	IncomingTraceRelationship TraceParentRelationship
}

// Option mutates the Options struct during construction.
type Option func(*Options)

func defaultOptions() Options {
	return Options{
		AnnotationPrefix:                   constants.DefaultAnnotationPrefix,
		TraceExpiration:                    constants.DefaultTraceExpiration,
		TraceStateTimestampKey:             constants.TraceStateTimestampKey,
		EmittedTraceParentAnnotationSuffix: constants.EmittedTraceParentAnnotationSuffix,
		EmittedTraceStateAnnotationSuffix:  constants.EmittedTraceStateAnnotationSuffix,
		IncomingTraceRelationship:          TraceParentRelationshipLink,
	}
}

func newOptions(optFns ...Option) Options {
	opts := defaultOptions()
	for _, fn := range optFns {
		if fn == nil {
			continue
		}
		fn(&opts)
	}
	return opts
}

// NewOptions returns a fully-evaluated Options struct using the provided Option functions.
func NewOptions(optFns ...Option) Options {
	return newOptions(optFns...)
}

// WithAnnotationPrefix overrides the default annotation prefix used for trace metadata.
func WithAnnotationPrefix(prefix string) Option {
	return func(o *Options) {
		if prefix == "" {
			return
		}
		o.AnnotationPrefix = sanitizePrefix(prefix)
	}
}

// WithTraceExpiration configures how long persisted trace context should be reused.
func WithTraceExpiration(d time.Duration) Option {
	return func(o *Options) {
		if d <= 0 {
			return
		}
		o.TraceExpiration = d
	}
}

// WithTraceStateTimestampKey customizes the key recorded inside tracestate for timestamp bookkeeping.
func WithTraceStateTimestampKey(key string) Option {
	return func(o *Options) {
		if key == "" {
			return
		}
		o.TraceStateTimestampKey = key
	}
}

// WithIncomingTraceParentAnnotation overrides the annotation key that should be inspected for incoming traceparent data.
func WithIncomingTraceParentAnnotation(key string) Option {
	return func(o *Options) {
		if key == "" {
			return
		}
		o.IncomingTraceParentAnnotation = key
	}
}

// WithIncomingTraceStateAnnotation overrides the annotation key that should be inspected for incoming tracestate data.
func WithIncomingTraceStateAnnotation(key string) Option {
	return func(o *Options) {
		if key == "" {
			return
		}
		o.IncomingTraceStateAnnotation = key
	}
}

// WithIncomingTraceRelationship selects whether incoming traceparent contexts should become parents or links.
func WithIncomingTraceRelationship(rel TraceParentRelationship) Option {
	return func(o *Options) {
		if rel != TraceParentRelationshipLink && rel != TraceParentRelationshipParent {
			return
		}
		o.IncomingTraceRelationship = rel
	}
}

// WithEmittedAnnotationSuffixes customizes the suffixes operatortrace uses when emitting trace annotations.
func WithEmittedAnnotationSuffixes(traceParentSuffix, traceStateSuffix string) Option {
	return func(o *Options) {
		if traceParentSuffix != "" {
			o.EmittedTraceParentAnnotationSuffix = sanitizeSuffix(traceParentSuffix)
		}
		if traceStateSuffix != "" {
			o.EmittedTraceStateAnnotationSuffix = sanitizeSuffix(traceStateSuffix)
		}
	}
}

func (o Options) emittedTraceParentAnnotationKey() string {
	return buildAnnotationKey(o.annotationPrefix(), constants.DefaultTraceParentAnnotation, o.EmittedTraceParentAnnotationSuffix)
}

func (o Options) emittedTraceStateAnnotationKey() string {
	return buildAnnotationKey(o.annotationPrefix(), constants.DefaultTraceStateAnnotation, o.EmittedTraceStateAnnotationSuffix)
}

// EmittedTraceParentAnnotationKey returns the annotation key operatortrace will write when persisting traceparent values.
func (o Options) EmittedTraceParentAnnotationKey() string {
	return o.emittedTraceParentAnnotationKey()
}

// EmittedTraceStateAnnotationKey returns the annotation key operatortrace will write when persisting tracestate values.
func (o Options) EmittedTraceStateAnnotationKey() string {
	return o.emittedTraceStateAnnotationKey()
}

func (o Options) legacyTraceIDAnnotationKey() string {
	return buildAnnotationKey(constants.DefaultAnnotationPrefix, constants.LegacyTraceIDAnnotation, "trace-id")
}

func (o Options) legacySpanIDAnnotationKey() string {
	return buildAnnotationKey(constants.DefaultAnnotationPrefix, constants.LegacySpanIDAnnotation, "span-id")
}

func (o Options) legacyTraceTimeAnnotationKey() string {
	return buildAnnotationKey(constants.DefaultAnnotationPrefix, constants.LegacyTraceIDTimeAnnotation, "trace-id-time")
}

func (o Options) annotationPrefix() string {
	if o.AnnotationPrefix == "" {
		return constants.DefaultAnnotationPrefix
	}
	return sanitizePrefix(o.AnnotationPrefix)
}

func (o Options) traceStateTimestampKey() string {
	if o.TraceStateTimestampKey == "" {
		return constants.TraceStateTimestampKey
	}
	return o.TraceStateTimestampKey
}

func (o Options) traceExpiration() time.Duration {
	if o.TraceExpiration <= 0 {
		return constants.DefaultTraceExpiration
	}
	return o.TraceExpiration
}

func buildAnnotationKey(prefix, fallback, suffix string) string {
	if prefix == "" {
		if fallback != "" {
			return fallback
		}
		return suffix
	}
	prefix = sanitizePrefix(prefix)
	suffix = sanitizeSuffix(suffix)
	return prefix + "/" + suffix
}

func sanitizePrefix(prefix string) string {
	return strings.TrimSuffix(prefix, "/")
}

func sanitizeSuffix(suffix string) string {
	return strings.TrimPrefix(strings.TrimSpace(suffix), "/")
}

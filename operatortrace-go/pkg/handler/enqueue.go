// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// pkg/handler/enqueue.go

/*
Forked from: https://github.com/kubernetes-sigs/controller-runtime/blob/v0.19.6/pkg/handler/
Has been modified to suit the project's needs.
*/

/*
Copyright 2018 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package handler

import (
	"context"
	"reflect"

	tracingclient "github.com/Azure/operatortrace/operatortrace-go/pkg/client"
	"github.com/Azure/operatortrace/operatortrace-go/pkg/constants"
	"github.com/Azure/operatortrace/operatortrace-go/pkg/tracecontext"
	tracingtypes "github.com/Azure/operatortrace/operatortrace-go/pkg/types"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/workqueue"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	ctrlreconcile "sigs.k8s.io/controller-runtime/pkg/reconcile"
)

type empty struct{}

var _ EventHandler = &EnqueueRequestForObject{}

// EnqueueRequestForObject enqueues a Request containing the Name and Namespace of the object that is the source of the Event.
// (e.g. the created / deleted / updated objects Name and Namespace). handler.EnqueueRequestForObject is used by almost all
// Controllers that have associated Resources (e.g. CRDs) to reconcile the associated Resource.
type EnqueueRequestForObject = TypedEnqueueRequestForObject[client.Object]

// TypedEnqueueRequestForObject enqueues a Request containing the Name and Namespace of the object that is the source of the Event.
// (e.g. the created / deleted / updated objects Name and Namespace).  handler.TypedEnqueueRequestForObject is used by almost all
// Controllers that have associated Resources (e.g. CRDs) to reconcile the associated Resource.
//
// TypedEnqueueRequestForObject is experimental and subject to future change.
type TypedEnqueueRequestForObject[object client.Object] struct {
	// Scheme is used to determine the GVK for the object
	Scheme *runtime.Scheme

	// AnnotationConfig overrides which annotation keys are read for trace context.
	// If nil, defaults to the operatortrace default keys.
	AnnotationConfig *tracecontext.AnnotationExtractionConfig
}

// Create implements EventHandler.
func (e *TypedEnqueueRequestForObject[T]) Create(ctx context.Context, evt event.TypedCreateEvent[T], q workqueue.TypedRateLimitingInterface[tracingtypes.RequestWithTraceID]) {
	if isNil(evt.Object) {
		return
	}
	q.Add(e.objectToRequestWithTraceID(evt.Object, "Create"))
}

// Update implements EventHandler.
func (e *TypedEnqueueRequestForObject[T]) Update(ctx context.Context, evt event.TypedUpdateEvent[T], q workqueue.TypedRateLimitingInterface[tracingtypes.RequestWithTraceID]) {
	switch {
	case !isNil(evt.ObjectNew):
		q.Add(e.objectToRequestWithTraceID(evt.ObjectNew, "Update"))
	case !isNil(evt.ObjectOld):
		// Do not enqueue the old object, as it is not the source of the event.
	default:
		// No object to enqueue
	}
}

// Delete implements EventHandler.
func (e *TypedEnqueueRequestForObject[T]) Delete(ctx context.Context, evt event.TypedDeleteEvent[T], q workqueue.TypedRateLimitingInterface[tracingtypes.RequestWithTraceID]) {
	if isNil(evt.Object) {
		return
	}
	q.Add(e.objectToRequestWithTraceID(evt.Object, "Delete"))
}

// Generic implements EventHandler.
func (e *TypedEnqueueRequestForObject[T]) Generic(ctx context.Context, evt event.TypedGenericEvent[T], q workqueue.TypedRateLimitingInterface[tracingtypes.RequestWithTraceID]) {
	if isNil(evt.Object) {
		return
	}
	q.Add(e.objectToRequestWithTraceID(evt.Object, "Generic"))
}

func isNil(arg any) bool {
	if v := reflect.ValueOf(arg); !v.IsValid() || ((v.Kind() == reflect.Ptr ||
		v.Kind() == reflect.Interface ||
		v.Kind() == reflect.Slice ||
		v.Kind() == reflect.Map ||
		v.Kind() == reflect.Chan ||
		v.Kind() == reflect.Func) && v.IsNil()) {
		return true
	}
	return false
}

func (e *TypedEnqueueRequestForObject[T]) objectToRequestWithTraceID(obj client.Object, eventKind string) tracingtypes.RequestWithTraceID {
	traceID, spanID := traceAndSpanIDsFromAnnotations(obj.GetAnnotations(), e.annotationConfig())
	if (traceID == "" || spanID == "") && e.Scheme != nil {
		if condTraceID, condSpanID := traceAndSpanIDsFromStatus(obj, e.Scheme); condTraceID != "" && condSpanID != "" {
			traceID, spanID = condTraceID, condSpanID
		}
	}
	senderName := obj.GetName()
	senderKind := ""

	// Use apiutil to get the GVK from the scheme, as GetObjectKind() is typically empty for objects from the API
	if e.Scheme != nil {
		gvk, err := apiutil.GVKForObject(obj, e.Scheme)
		if err == nil {
			senderKind = gvk.GroupKind().Kind
		}
	}

	return tracingtypes.RequestWithTraceID{
		Request: ctrlreconcile.Request{
			NamespacedName: types.NamespacedName{
				Name:      obj.GetName(),
				Namespace: obj.GetNamespace(),
			},
		},
		Parent: tracingtypes.RequestParent{
			TraceID:   traceID,
			SpanID:    spanID,
			Name:      senderName,
			Kind:      senderKind,
			EventKind: eventKind,
		},
	}
}

func (e *TypedEnqueueRequestForObject[T]) annotationConfig() tracecontext.AnnotationExtractionConfig {
	if e.AnnotationConfig != nil {
		return *e.AnnotationConfig
	}
	return defaultAnnotationExtractionConfig()
}

func defaultAnnotationExtractionConfig() tracecontext.AnnotationExtractionConfig {
	return tracecontext.AnnotationExtractionConfig{
		TraceParentKey:   constants.DefaultTraceParentAnnotation,
		TraceStateKey:    constants.DefaultTraceStateAnnotation,
		LegacyTraceIDKey: constants.LegacyTraceIDAnnotation,
		LegacySpanIDKey:  constants.LegacySpanIDAnnotation,
	}
}

func traceAndSpanIDsFromAnnotations(annotations map[string]string, cfg tracecontext.AnnotationExtractionConfig) (string, string) {
	tc, found := tracecontext.ExtractTraceContextFromAnnotations(annotations, cfg)
	if !found {
		return "", ""
	}

	spanContext, err := tracecontext.SpanContextFromTraceData(tc.TraceParent, tc.TraceState)
	if err != nil || !spanContext.IsValid() {
		return "", ""
	}

	return spanContext.TraceID().String(), spanContext.SpanID().String()
}

func traceAndSpanIDsFromStatus(obj client.Object, scheme *runtime.Scheme) (string, string) {
	traceID, err := tracingclient.GetConditionMessage("TraceID", obj, scheme)
	if err != nil || traceID == "" {
		return "", ""
	}
	spanID, err := tracingclient.GetConditionMessage("SpanID", obj, scheme)
	if err != nil || spanID == "" {
		return "", ""
	}
	return traceID, spanID
}

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

	"github.com/Azure/operatortrace/operatortrace-go/pkg/constants"
	"github.com/Azure/operatortrace/operatortrace-go/pkg/tracecontext"
	tracingtypes "github.com/Azure/operatortrace/operatortrace-go/pkg/types"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/workqueue"
	"sigs.k8s.io/controller-runtime/pkg/client"
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
type TypedEnqueueRequestForObject[object client.Object] struct{}

// Create implements EventHandler.
func (e *TypedEnqueueRequestForObject[T]) Create(ctx context.Context, evt event.TypedCreateEvent[T], q workqueue.TypedRateLimitingInterface[tracingtypes.RequestWithTraceID]) {
	if isNil(evt.Object) {
		return
	}
	q.Add(objectToRequestWithTraceID(evt.Object))
}

// Update implements EventHandler.
func (e *TypedEnqueueRequestForObject[T]) Update(ctx context.Context, evt event.TypedUpdateEvent[T], q workqueue.TypedRateLimitingInterface[tracingtypes.RequestWithTraceID]) {
	switch {
	case !isNil(evt.ObjectNew):
		q.Add(objectToRequestWithTraceID(evt.ObjectNew))
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
	q.Add(objectToRequestWithTraceID(evt.Object))
}

// Generic implements EventHandler.
func (e *TypedEnqueueRequestForObject[T]) Generic(ctx context.Context, evt event.TypedGenericEvent[T], q workqueue.TypedRateLimitingInterface[tracingtypes.RequestWithTraceID]) {
	if isNil(evt.Object) {
		return
	}
	q.Add(objectToRequestWithTraceID(evt.Object))
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

func objectToRequestWithTraceID(obj client.Object) tracingtypes.RequestWithTraceID {
	traceID, spanID := traceAndSpanIDsFromAnnotations(obj.GetAnnotations())
	senderName := obj.GetName()
	senderKind := obj.GetObjectKind().GroupVersionKind().Kind

	return tracingtypes.RequestWithTraceID{
		Request: ctrlreconcile.Request{
			NamespacedName: types.NamespacedName{
				Name:      obj.GetName(),
				Namespace: obj.GetNamespace(),
			},
		},
		Parent: tracingtypes.RequestParent{
			TraceID: traceID,
			SpanID:  spanID,
			Name:    senderName,
			Kind:    senderKind,
		},
	}
}

var defaultAnnotationExtractionConfig = tracecontext.AnnotationExtractionConfig{
	TraceParentKeys: []string{
		constants.DefaultTraceParentAnnotation,
	},
	TraceStateKeys: []string{
		constants.DefaultTraceStateAnnotation,
	},
	LegacyTraceIDKey: constants.LegacyTraceIDAnnotation,
	LegacySpanIDKey:  constants.LegacySpanIDAnnotation,
}

func traceAndSpanIDsFromAnnotations(annotations map[string]string) (string, string) {
	tc, found := tracecontext.ExtractTraceContextFromAnnotations(annotations, defaultAnnotationExtractionConfig)
	if !found {
		return "", ""
	}

	spanContext, err := tracecontext.SpanContextFromTraceData(tc.TraceParent, tc.TraceState)
	if err != nil || !spanContext.IsValid() {
		return "", ""
	}

	return spanContext.TraceID().String(), spanContext.SpanID().String()
}

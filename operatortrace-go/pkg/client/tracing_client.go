// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package client

import (
	"context"
	"fmt"
	"reflect"
	"strings"

	constants "github.com/Azure/operatortrace/operatortrace-go/pkg/constants"
	"github.com/go-logr/logr"
	"go.opentelemetry.io/otel/trace"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
)

// TracingClient wraps the Kubernetes client to add tracing functionality
type tracingClient struct {
	scheme *runtime.Scheme
	client.Client
	client.Reader
	trace.Tracer
	Logger logr.Logger
}

type tracingStatusClient struct {
	scheme *runtime.Scheme
	client.StatusWriter
	trace.Tracer
	Logger logr.Logger
}

type TracingClient interface {
	client.Client
	trace.Tracer
	// We use this to which calls client.Client Get
	StartTrace(ctx context.Context, key *client.ObjectKey, obj client.Object, opts ...client.GetOption) (context.Context, trace.Span, error)
	EndTrace(ctx context.Context, obj client.Object, opts ...client.PatchOption) (client.Object, error)
	StartSpan(ctx context.Context, operationName string) (context.Context, trace.Span)
	EmbedTraceIDInNamespacedName(key *client.ObjectKey, obj client.Object) error
}

var _ TracingClient = (*tracingClient)(nil)
var _ client.StatusWriter = (*tracingStatusClient)(nil)

// NewTracingClient initializes and returns a new TracingClient
// optional scheme.  If not, it will use client-go scheme
func NewTracingClient(c client.Client, r client.Reader, t trace.Tracer, l logr.Logger, scheme ...*runtime.Scheme) TracingClient {
	tracingScheme := clientgoscheme.Scheme
	if len(scheme) > 0 {
		tracingScheme = scheme[0]
	}

	return &tracingClient{
		scheme: tracingScheme,
		Client: c,
		Reader: r,
		Tracer: t,
		Logger: l,
	}
}

// Create adds tracing and traceID annotation around the original client's Create method
func (tc *tracingClient) Create(ctx context.Context, obj client.Object, opts ...client.CreateOption) error {
	gvk, err := apiutil.GVKForObject(obj, tc.scheme)
	if err != nil {
		return fmt.Errorf("problem getting the scheme: %w", err)
	}

	kind := gvk.GroupKind().Kind
	ctx, span := startSpanFromContext(ctx, tc.Logger, tc.Tracer, obj, tc.scheme, fmt.Sprintf("Create %s %s", kind, obj.GetName()))
	defer span.End()

	addTraceIDAnnotation(ctx, obj)
	tc.Logger.Info("Creating object", "object", obj.GetName())
	err = tc.Client.Create(ctx, obj, opts...)
	if err != nil {
		span.RecordError(err)
	}

	return err
}

// Update adds tracing and traceID annotation around the original client's Update method
func (tc *tracingClient) Update(ctx context.Context, obj client.Object, opts ...client.UpdateOption) error {
	gvk, err := apiutil.GVKForObject(obj, tc.scheme)
	if err != nil {
		return fmt.Errorf("problem getting the scheme: %w", err)
	}

	kind := gvk.GroupKind().Kind

	ctx, span := startSpanFromContext(ctx, tc.Logger, tc.Tracer, obj, tc.scheme, fmt.Sprintf("Update %s %s", kind, obj.GetName()))
	defer span.End()

	addTraceIDAnnotation(ctx, obj)
	tc.Logger.Info("Updating object", "object", obj.GetName())

	err = tc.Client.Update(ctx, obj, opts...)
	if err != nil {
		span.RecordError(err)
	}

	return err
}

func (tc *tracingClient) StartSpan(ctx context.Context, operationName string) (context.Context, trace.Span) {
	return startSpanFromContext(ctx, tc.Logger, tc.Tracer, nil, tc.scheme, operationName)
}

// EmbedTraceIDInNamespacedName embeds the traceID and spanID in the key.Name
func (tc *tracingClient) EmbedTraceIDInNamespacedName(key *client.ObjectKey, obj client.Object) error {
	traceID := obj.GetAnnotations()[constants.TraceIDAnnotation]
	spanID := obj.GetAnnotations()[constants.SpanIDAnnotation]
	if traceID == "" || spanID == "" {
		return nil
	}

	gvk, err := apiutil.GVKForObject(obj, tc.scheme)
	if err != nil {
		return fmt.Errorf("problem getting the scheme: %w", err)
	}

	objectKind := gvk.GroupKind().Kind
	objectName := obj.GetName()

	key.Name = fmt.Sprintf("%s;%s;%s;%s;%s", traceID, spanID, objectKind, objectName, key.Name)
	tc.Logger.Info("EmbedTraceIDInNamespacedName", "objectName", key.Name)
	return nil
}

// Get adds tracing around the original client's Get method
// IMPORTANT: Caller MUST call `defer span.End()` to end the trace from the calling function
func (tc *tracingClient) StartTrace(ctx context.Context, key *client.ObjectKey, obj client.Object, opts ...client.GetOption) (context.Context, trace.Span, error) {
	name := getNameFromNamespacedName(*key)
	incomingKey := *key
	key.Name = name

	// Create or retrieve the span from the context
	getErr := tc.Reader.Get(ctx, *key, obj, opts...)
	if getErr != nil {
		ctx, span := startSpanFromContext(ctx, tc.Logger, tc.Tracer, obj, tc.scheme, fmt.Sprintf("StartTrace Unknown Object %s", name))
		return trace.ContextWithSpan(ctx, span), span, getErr
	}
	overrideTraceIDFromNamespacedName(incomingKey, obj)

	gvk, err := apiutil.GVKForObject(obj, tc.scheme)
	objectKind := ""
	if err == nil {
		objectKind = gvk.GroupKind().Kind
	}
	callerName := getCallerNameFromNamespacedName(incomingKey)
	callerKind := getCallerKindFromNamespacedName(incomingKey)

	operationName := ""

	if callerKind != "" && callerName != "" {
		operationName = fmt.Sprintf("StartTrace %s/%s Triggered By Changed Object %s/%s", objectKind, name, callerKind, callerName)
	} else {
		operationName = fmt.Sprintf("StartTrace %s %s", objectKind, name)
	}

	ctx, span := startSpanFromContext(ctx, tc.Logger, tc.Tracer, obj, tc.scheme, operationName)

	if err != nil {
		span.RecordError(err)
	}

	tc.Logger.Info("Getting object", "object", key.Name)
	return trace.ContextWithSpan(ctx, span), span, err
}

// Ends the trace by clearing the traceid from the object
func (tc *tracingClient) EndTrace(ctx context.Context, obj client.Object, opts ...client.PatchOption) (client.Object, error) {
	ctx, span := startSpanFromContext(ctx, tc.Logger, tc.Tracer, obj, tc.scheme, fmt.Sprintf("EndTrace %s %s", obj.GetObjectKind().GroupVersionKind().Kind, obj.GetName()))
	defer span.End()

	annotations := obj.GetAnnotations()
	if annotations == nil {
		return obj, nil
	}

	// get the current object and ensure that current object has the expected traceid and spanid annotations
	currentObjFromServer := obj.DeepCopyObject().(client.Object)
	err := tc.Reader.Get(ctx, client.ObjectKeyFromObject(obj), currentObjFromServer)

	if err != nil {
		span.RecordError(err)
	}

	// compare the traceid and spanid from currentobj to ensure that the traceid and spanid are not changed
	if currentObjFromServer.GetAnnotations()[constants.TraceIDAnnotation] != obj.GetAnnotations()[constants.TraceIDAnnotation] {
		tc.Logger.Info("TraceID has changed, skipping patch", "object", obj.GetName())
		span.RecordError(fmt.Errorf("TraceID has changed, skipping patch: object %s", obj.GetName()))
		return obj, nil
	}

	// Remove the traceid and spanid annotations and create a patch
	original := obj.DeepCopyObject().(client.Object)
	patch := client.MergeFrom(original)

	delete(annotations, constants.TraceIDAnnotation)
	delete(annotations, constants.SpanIDAnnotation)
	obj.SetAnnotations(annotations)

	tc.Logger.Info("Patching object", "object", obj.GetName())
	// Use the Patch function to apply the patch

	err = tc.Client.Patch(ctx, obj, patch, opts...)

	if err != nil {
		span.RecordError(err)
	}

	// remove the traceid and spanid conditions from the object and create a status().patch
	deleteConditionAsMap("TraceID", obj, tc.scheme)
	deleteConditionAsMap("SpanID", obj, tc.scheme)
	original = obj.DeepCopyObject().(client.Object)
	patch = client.MergeFrom(original)

	tc.Logger.Info("Patching object status", "object", obj.GetName())
	err = tc.Status().Patch(ctx, obj, patch)

	if err != nil {
		span.RecordError(err)
	}

	return obj, err
}

// Get adds tracing around the original client's Get method
func (tc *tracingClient) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	// Create or retrieve the span from the context
	gvk, err := apiutil.GVKForObject(obj, tc.scheme)
	if err != nil {
		return fmt.Errorf("problem getting the scheme: %w", err)
	}

	kind := gvk.GroupKind().Kind

	ctx, span := startSpanFromContext(ctx, tc.Logger, tc.Tracer, obj, tc.scheme, fmt.Sprintf("Get %s %s", kind, key.Name))
	defer span.End()

	tc.Logger.Info("Getting object", "object", key.Name)

	err = tc.Client.Get(ctx, key, obj, opts...)

	if err != nil {
		span.RecordError(err)
	}

	return err
}

func (tc *tracingClient) List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
	gvk, _ := apiutil.GVKForObject(list, tc.scheme)
	kind := gvk.GroupKind().Kind
	ctx, span := startSpanFromContextList(ctx, tc.Logger, tc.Tracer, list, kind)
	defer span.End()

	tc.Logger.Info("Getting List", "object", kind)
	err := tc.Client.List(ctx, list, opts...)
	if err != nil {
		span.RecordError(err)
	}
	return err
}

// Patch  adds tracing and traceID annotation around the original client's Patch method
func (tc *tracingClient) Patch(ctx context.Context, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
	gvk, err := apiutil.GVKForObject(obj, tc.scheme)
	if err != nil {
		return fmt.Errorf("problem getting the scheme: %w", err)
	}

	kind := gvk.GroupKind().Kind

	ctx, span := startSpanFromContext(ctx, tc.Logger, tc.Tracer, obj, tc.scheme, fmt.Sprintf("Patch %s %s", kind, obj.GetName()))
	defer span.End()

	addTraceIDAnnotation(ctx, obj)
	tc.Logger.Info("Patching object", "object", obj.GetName())
	err = tc.Client.Patch(ctx, obj, patch, opts...)
	if err != nil {
		span.RecordError(err)
	}

	return err
}

// Delete adds tracing around the original client's Delete method
func (tc *tracingClient) Delete(ctx context.Context, obj client.Object, opts ...client.DeleteOption) error {
	gvk, err := apiutil.GVKForObject(obj, tc.scheme)
	if err != nil {
		return fmt.Errorf("problem getting the scheme: %w", err)
	}

	kind := gvk.GroupKind().Kind

	ctx, span := startSpanFromContext(ctx, tc.Logger, tc.Tracer, obj, tc.scheme, fmt.Sprintf("Delete %s %s", kind, obj.GetName()))
	defer span.End()

	tc.Logger.Info("Deleting object", "object", obj.GetName())
	err = tc.Client.Delete(ctx, obj, opts...)
	if err != nil {
		span.RecordError(err)
	}
	return err
}

func (tc *tracingClient) DeleteAllOf(ctx context.Context, obj client.Object, opts ...client.DeleteAllOfOption) error {
	gvk, err := apiutil.GVKForObject(obj, tc.scheme)
	if err != nil {
		return fmt.Errorf("problem getting the scheme: %w", err)
	}

	kind := gvk.GroupKind().Kind

	ctx, span := startSpanFromContext(ctx, tc.Logger, tc.Tracer, obj, tc.scheme, fmt.Sprintf("DeleteAllOf %s %s", kind, obj.GetName()))
	defer span.End()

	tc.Logger.Info("Deleting all of object", "object", obj.GetName())
	err = tc.Client.DeleteAllOf(ctx, obj, opts...)
	if err != nil {
		span.RecordError(err)
	}
	return err

}

func (tc *tracingClient) Status() client.StatusWriter {
	return &tracingStatusClient{
		scheme:       tc.scheme,
		Logger:       tc.Logger,
		StatusWriter: tc.Client.Status(),
		Tracer:       tc.Tracer,
	}
}

func (ts *tracingStatusClient) Update(ctx context.Context, obj client.Object, opts ...client.SubResourceUpdateOption) error {
	gvk, err := apiutil.GVKForObject(obj, ts.scheme)
	if err != nil {
		return fmt.Errorf("problem getting the scheme: %w", err)
	}

	kind := gvk.GroupKind().Kind

	ctx, span := startSpanFromContext(ctx, ts.Logger, ts.Tracer, obj, ts.scheme, fmt.Sprintf("StatusUpdate %s %s", kind, obj.GetName()))
	defer span.End()

	setConditionMessage("TraceID", span.SpanContext().TraceID().String(), obj, ts.scheme)
	setConditionMessage("SpanID", span.SpanContext().SpanID().String(), obj, ts.scheme)

	ts.Logger.Info("updating status object", "object", obj.GetName())
	err = ts.StatusWriter.Update(ctx, obj, opts...)
	if err != nil {
		span.RecordError(err)
	}
	return err
}

func (ts *tracingStatusClient) Patch(ctx context.Context, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
	gvk, err := apiutil.GVKForObject(obj, ts.scheme)
	if err != nil {
		return fmt.Errorf("problem getting the scheme: %w", err)
	}

	kind := gvk.GroupKind().Kind

	ctx, span := startSpanFromContext(ctx, ts.Logger, ts.Tracer, obj, ts.scheme, fmt.Sprintf("StatusPatch %s %s", kind, obj.GetName()))
	defer span.End()

	setConditionMessage("TraceID", span.SpanContext().TraceID().String(), obj, ts.scheme)
	setConditionMessage("SpanID", span.SpanContext().SpanID().String(), obj, ts.scheme)

	ts.Logger.Info("patching status object", "object", obj.GetName())
	err = ts.StatusWriter.Patch(ctx, obj, patch, opts...)
	if err != nil {
		span.RecordError(err)
	}

	return err
}

func (ts *tracingStatusClient) Create(ctx context.Context, obj client.Object, subResource client.Object, opts ...client.SubResourceCreateOption) error {
	gvk, err := apiutil.GVKForObject(obj, ts.scheme)
	if err != nil {
		return fmt.Errorf("problem getting the scheme: %w", err)
	}

	kind := gvk.GroupKind().Kind

	ctx, span := startSpanFromContext(ctx, ts.Logger, ts.Tracer, obj, ts.scheme, fmt.Sprintf("StatusCreate %s %s", kind, obj.GetName()))
	defer span.End()

	setConditionMessage("TraceID", span.SpanContext().TraceID().String(), obj, ts.scheme)
	setConditionMessage("SpanID", span.SpanContext().SpanID().String(), obj, ts.scheme)

	ts.Logger.Info("creating status object", "object", obj.GetName())
	err = ts.StatusWriter.Create(ctx, obj, subResource, opts...)
	if err != nil {
		span.RecordError(err)
	}
	return err
}

// startSpanFromContext starts a new span from the context and attaches trace information to the object
func startSpanFromContext(ctx context.Context, logger logr.Logger, tracer trace.Tracer, obj client.Object, scheme *runtime.Scheme, operationName string) (context.Context, trace.Span) {
	span := trace.SpanFromContext(ctx)
	if span.SpanContext().IsValid() {
		spanContext := trace.NewSpanContext(trace.SpanContextConfig{
			TraceID: span.SpanContext().TraceID(),
			SpanID:  span.SpanContext().SpanID(),
		})
		ctx = trace.ContextWithRemoteSpanContext(ctx, spanContext)
		ctx, span = tracer.Start(ctx, operationName)
		return ctx, span
	}

	if !span.SpanContext().IsValid() {
		if obj != nil {
			// no valid trace ID in context, check object conditions
			if traceID, err := getConditionMessage("TraceID", obj, scheme); err == nil {
				if traceIDValue, err := trace.TraceIDFromHex(traceID); err == nil {
					spanContext := trace.NewSpanContext(trace.SpanContextConfig{})
					if spanID, err := getConditionMessage("SpanID", obj, scheme); err == nil {
						if spanIDValue, err := trace.SpanIDFromHex(spanID); err == nil {
							spanContext = trace.NewSpanContext(trace.SpanContextConfig{
								TraceID: traceIDValue,
								SpanID:  spanIDValue,
							})
						} else {
							spanContext = trace.NewSpanContext(trace.SpanContextConfig{
								TraceID: traceIDValue,
							})
						}
					}
					ctx = trace.ContextWithRemoteSpanContext(ctx, spanContext)
				}
			} else {
				// No valid trace ID in context, check object annotations
				if traceID, ok := obj.GetAnnotations()[constants.TraceIDAnnotation]; ok {
					if traceIDValue, err := trace.TraceIDFromHex(traceID); err == nil {
						spanContext := trace.NewSpanContext(trace.SpanContextConfig{})
						if spanID, ok := obj.GetAnnotations()[constants.SpanIDAnnotation]; ok {
							if spanIDValue, err := trace.SpanIDFromHex(spanID); err == nil {
								spanContext = trace.NewSpanContext(trace.SpanContextConfig{
									TraceID: traceIDValue,
									SpanID:  spanIDValue,
								})
							} else {
								spanContext = trace.NewSpanContext(trace.SpanContextConfig{
									TraceID: traceIDValue,
								})
							}
						}
						ctx = trace.ContextWithRemoteSpanContext(ctx, spanContext)
					} else {
						logger.Error(err, "Invalid trace ID", "traceID", traceID)
					}
				}
			}
		}
	}

	// Create a new span
	ctx, span = tracer.Start(ctx, operationName)
	return ctx, span
}

// if the key.Name looks like this: f620f5cad0af940c294f980c5366a6a1;45f359cdc1c8ab06;Configmap;pod-configmap01;default-pod
// this will return the corrected Kind (Configmap)
func getCallerKindFromNamespacedName(key client.ObjectKey) string {

	keyNameParts := strings.Split(key.Name, ";")
	if len(keyNameParts) != 5 {
		return ""
	}
	return keyNameParts[2]
}

// if the key.Name looks like this: f620f5cad0af940c294f980c5366a6a1;45f359cdc1c8ab06;Configmap;pod-configmap01;default-pod
// this will return the corrected caller-name (pod-configmap01)
func getCallerNameFromNamespacedName(key client.ObjectKey) string {
	keyNameParts := strings.Split(key.Name, ";")
	if len(keyNameParts) != 5 {
		return ""
	}
	return keyNameParts[3]
}

// if the key.Name looks like this: f620f5cad0af940c294f980c5366a6a1;45f359cdc1c8ab06;Configmap;pod-configmap01;default-pod
// this will return the corrected key.name (default-pod)
func getNameFromNamespacedName(key client.ObjectKey) string {
	keyNameParts := strings.Split(key.Name, ";")
	if len(keyNameParts) != 5 {
		return key.Name
	}
	return keyNameParts[4]
}

// if the key.Name looks like this: f620f5cad0af940c294f980c5366a6a1;45f359cdc1c8ab06;Configmap;pod-configmap01;default-pod
// then we can extract the traceID and spanID from the key.Name
// and override the traceID and spanID in the object annotations
func overrideTraceIDFromNamespacedName(key client.ObjectKey, obj client.Object) error {
	keyNameParts := strings.Split(key.Name, ";")
	if len(keyNameParts) != 5 {
		return nil
	}

	traceID := keyNameParts[0]
	spanID := keyNameParts[1]

	if obj.GetAnnotations() == nil {
		obj.SetAnnotations(map[string]string{})
	}
	annotations := obj.GetAnnotations()
	annotations[constants.TraceIDAnnotation] = traceID
	annotations[constants.SpanIDAnnotation] = spanID
	obj.SetAnnotations(annotations)
	return nil
}

func startSpanFromContextList(ctx context.Context, logger logr.Logger, tracer trace.Tracer, obj client.ObjectList, operationName string) (context.Context, trace.Span) {
	span := trace.SpanFromContext(ctx)
	if span.SpanContext().IsValid() {
		spanContext := trace.NewSpanContext(trace.SpanContextConfig{
			TraceID: span.SpanContext().TraceID(),
			SpanID:  span.SpanContext().SpanID(),
		})
		ctx = trace.ContextWithRemoteSpanContext(ctx, spanContext)
		ctx, span = tracer.Start(ctx, operationName)
		return trace.ContextWithSpan(ctx, span), span
	}

	// Create a new span
	ctx, span = tracer.Start(ctx, operationName)
	return ctx, span
}

// addTraceIDAnnotation adds the traceID as an annotation to the object
func addTraceIDAnnotation(ctx context.Context, obj client.Object) {
	span := trace.SpanFromContext(ctx)
	traceID := span.SpanContext().TraceID().String()
	if traceID != "" {
		if obj.GetAnnotations() == nil {
			obj.SetAnnotations(map[string]string{})
		}
		annotations := obj.GetAnnotations()
		annotations[constants.TraceIDAnnotation] = traceID
		obj.SetAnnotations(annotations)
	}
	spanID := span.SpanContext().SpanID().String()
	if spanID != "" {
		if obj.GetAnnotations() == nil {
			obj.SetAnnotations(map[string]string{})
		}
		annotations := obj.GetAnnotations()
		annotations[constants.SpanIDAnnotation] = spanID
		obj.SetAnnotations(annotations)
	}
}

// getConditionMessage retrieves the message for a specific condition type from a Kubernetes object.
func getConditionMessage(conditionType string, obj client.Object, scheme *runtime.Scheme) (string, error) {
	conditions, err := getConditionsAsMap(obj, scheme)
	if err != nil {
		return "", err
	}

	for _, condition := range conditions {
		// Check if "Type" key exists
		conType, exists := condition["Type"]
		if !exists {
			return "", fmt.Errorf("condition does not contain a 'Type' field")
		}

		// Convert conType to string using reflection
		conTypeStr, err := convertToString(conType)
		if err != nil {
			return "", fmt.Errorf("failed to convert 'Type' field to string: %v", err)
		}

		if conTypeStr == conditionType {
			message := condition["Message"].(string)
			return message, nil
		}
	}

	return "", fmt.Errorf("condition of type %s not found", conditionType)
}

// setConditionMessage sets the message for a specific condition type in a Kubernetes object.
func setConditionMessage(conditionType, message string, obj client.Object, scheme *runtime.Scheme) error {
	deleteConditionAsMap(conditionType, obj, scheme)

	conditions, err := getConditionsAsMap(obj, scheme)
	if err != nil {
		return err
	}

	newCondition := map[string]interface{}{
		"Type":               conditionType,
		"Status":             metav1.ConditionUnknown,
		"LastTransitionTime": metav1.Now(),
		"Message":            message,
	}
	conditions = append(conditions, newCondition)

	return setConditionsFromMap(obj, conditions, scheme)
}

func deleteConditionAsMap(conditionType string, obj client.Object, scheme *runtime.Scheme) error {
	// Retrieve the current conditions as a map
	conditions, err := getConditionsAsMap(obj, scheme)
	if err != nil {
		return err
	}

	var outConditions []map[string]interface{}
	for _, condition := range conditions {
		// Check if "Type" key exists
		conType, exists := condition["Type"]
		if !exists {
			return fmt.Errorf("condition does not contain a 'Type' field")
		}

		// Convert conType to string using reflection
		conTypeStr, err := convertToString(conType)
		if err != nil {
			return fmt.Errorf("failed to convert 'Type' field to string: %v", err)
		}

		if conTypeStr != conditionType {
			outConditions = append(outConditions, condition)
		}
	}

	// Set the updated conditions back to the object
	return setConditionsFromMap(obj, outConditions, scheme)
}

func getConditionsAsMap(obj client.Object, scheme *runtime.Scheme) ([]map[string]interface{}, error) {
	gvk, err := apiutil.GVKForObject(obj, scheme)
	if err != nil {
		return nil, fmt.Errorf("problem getting the GVK: %w", err)
	}

	objTyped, err := scheme.New(gvk)
	if err != nil {
		return nil, fmt.Errorf("problem creating new object of kind %s: %w", gvk.Kind, err)
	}

	if err := scheme.Convert(obj, objTyped, nil); err != nil {
		return nil, fmt.Errorf("problem converting object to kind %s: %w", gvk.Kind, err)
	}

	val := reflect.ValueOf(objTyped)
	statusField := val.Elem().FieldByName("Status")
	if !statusField.IsValid() {
		return nil, fmt.Errorf("status field not found in kind %s", gvk.Kind)
	}

	conditionsField := statusField.FieldByName("Conditions")
	if !conditionsField.IsValid() {
		return nil, fmt.Errorf("conditions field not found in kind %s", gvk.Kind)
	}

	conditionsValue := conditionsField.Interface()
	val = reflect.ValueOf(conditionsValue)
	if val.Kind() != reflect.Slice {
		return nil, fmt.Errorf("conditions field is not a slice")
	}

	var conditionsAsMap []map[string]interface{}
	for i := 0; i < val.Len(); i++ {
		conditionVal := val.Index(i)
		if conditionVal.Kind() == reflect.Ptr {
			conditionVal = conditionVal.Elem()
		}

		conditionMap := make(map[string]interface{})
		for _, field := range reflect.VisibleFields(conditionVal.Type()) {
			fieldValue := conditionVal.FieldByIndex(field.Index)
			conditionMap[field.Name] = fieldValue.Interface()
		}

		conditionsAsMap = append(conditionsAsMap, conditionMap)
	}

	return conditionsAsMap, nil
}

func setConditionsFromMap(obj client.Object, conditionsAsMap []map[string]interface{}, scheme *runtime.Scheme) error {
	gvk, err := apiutil.GVKForObject(obj, scheme)
	if err != nil {
		return fmt.Errorf("problem getting the GVK: %w", err)
	}

	objTyped, err := scheme.New(gvk)
	if err != nil {
		return fmt.Errorf("problem creating new object of kind %s: %w", gvk.Kind, err)
	}

	if err := scheme.Convert(obj, objTyped, nil); err != nil {
		return fmt.Errorf("problem converting object to kind %s: %w", gvk.Kind, err)
	}

	val := reflect.ValueOf(objTyped)
	statusField := val.Elem().FieldByName("Status")
	if !statusField.IsValid() {
		return fmt.Errorf("status field not found in kind %s", gvk.Kind)
	}

	conditionsField := statusField.FieldByName("Conditions")
	if !conditionsField.IsValid() {
		return fmt.Errorf("conditions field not found in kind %s", gvk.Kind)
	}

	elemType := conditionsField.Type().Elem()
	result := reflect.MakeSlice(conditionsField.Type(), len(conditionsAsMap), len(conditionsAsMap))

	for i, conditionMap := range conditionsAsMap {
		targetCond := reflect.New(elemType).Elem()
		for key, value := range conditionMap {
			field := targetCond.FieldByName(key)
			if field.IsValid() {
				val := reflect.ValueOf(value)
				if val.Type().ConvertibleTo(field.Type()) {
					field.Set(val.Convert(field.Type()))
				} else {
					return fmt.Errorf("cannot convert value of field %s from %s to %s", key, val.Type(), field.Type())
				}
			}
		}
		if conditionsField.Type().Elem().Kind() == reflect.Ptr {
			result.Index(i).Set(targetCond.Addr())
		} else {
			result.Index(i).Set(targetCond)
		}
	}

	conditionsField.Set(result)
	if err := scheme.Convert(objTyped, obj, nil); err != nil {
		return fmt.Errorf("problem converting object back to unstructured: %w", err)
	}

	return nil
}

func mapToStruct(structVal reflect.Value, data map[string]interface{}) error {
	for key, value := range data {
		field := structVal.FieldByName(key)
		if field.IsValid() {
			switch field.Kind() {
			case reflect.String:
				field.SetString(value.(string))
			case reflect.Bool:
				field.SetBool(value.(bool))
			case reflect.Int32:
				field.SetInt(int64(value.(int32)))
			case reflect.Int64:
				field.SetInt(value.(int64))
			case reflect.Float64:
				field.SetFloat(value.(float64))
			default:
				field.Set(reflect.ValueOf(value))
			}
		}
	}
	return nil
}

func convertToString(value interface{}) (string, error) {
	v := reflect.ValueOf(value)
	switch v.Kind() {
	case reflect.String:
		return v.String(), nil
	case reflect.Interface:
		// Handle the case where the value is an interface
		return convertToString(v.Elem().Interface())
	default:
		// Check if the value has a String() method
		stringer, ok := value.(fmt.Stringer)
		if ok {
			return stringer.String(), nil
		}
		return "", fmt.Errorf("unsupported type: %T", value)
	}
}

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
	StartTrace(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) (context.Context, trace.Span, error)
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
func (tc *tracingClient) StartTrace(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) (context.Context, trace.Span, error) {
	name := getNameFromNamespacedName(key)
	initialKey := client.ObjectKey{Name: name, Namespace: key.Namespace}

	// Create or retrieve the span from the context
	_ = tc.Reader.Get(ctx, initialKey, obj, opts...)
	overrideTraceIDFromNamespacedName(key, obj)

	gvk, err := apiutil.GVKForObject(obj, tc.scheme)
	objectKind := ""
	if err == nil {
		objectKind = gvk.GroupKind().Kind
	}
	callerName := getCallerNameFromNamespacedName(key)
	callerKind := getCallerKindFromNamespacedName(key)

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
	deleteCondition("TraceID", obj, tc.scheme)
	deleteCondition("SpanID", obj, tc.scheme)
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

// getConditions retrieves the "conditions" field from the status of a Kubernetes object using type casting and returns it as []metav1.Condition.
func getConditions(obj client.Object, scheme *runtime.Scheme) ([]metav1.Condition, error) {
	gvk, err := apiutil.GVKForObject(obj, scheme)
	if err != nil {
		return nil, fmt.Errorf("problem getting the GVK: %w", err)
	}

	// Use the scheme to get the specific type of the object.
	objTyped, err := scheme.New(gvk)
	if err != nil {
		return nil, fmt.Errorf("problem creating new object of kind %s: %w", gvk.Kind, err)
	}

	// Cast the object to its specific type.
	if err := scheme.Convert(obj, objTyped, nil); err != nil {
		return nil, fmt.Errorf("problem converting object to kind %s: %w", gvk.Kind, err)
	}

	// Use reflection to access the conditions field.
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
	conditions, err := convertToMetaV1Conditions(conditionsValue)
	if err != nil {
		return nil, fmt.Errorf("error converting conditions for kind %s: %w", gvk.Kind, err)
	}

	return conditions, nil
}

// getConditionMessage retrieves the message for a specific condition type from a Kubernetes object.
func getConditionMessage(conditionType string, obj client.Object, scheme *runtime.Scheme) (string, error) {
	conditions, err := getConditions(obj, scheme)
	if err != nil {
		return "", err
	}

	for _, condition := range conditions {
		if condition.Type == conditionType {
			return condition.Message, nil
		}
	}

	return "", fmt.Errorf("condition of type %s not found", conditionType)
}

// convertToMetaV1Conditions converts conditions of any supported type to []metav1.Condition.
func convertToMetaV1Conditions(conditionsValue interface{}) ([]metav1.Condition, error) {
	val := reflect.ValueOf(conditionsValue)
	if val.Kind() != reflect.Slice {
		return nil, fmt.Errorf("conditions field is not a slice")
	}

	var metav1Conditions []metav1.Condition
	for i := 0; i < val.Len(); i++ {
		conditionVal := val.Index(i)
		if conditionVal.Kind() == reflect.Ptr {
			conditionVal = conditionVal.Elem()
		}

		condition := metav1.Condition{}
		for _, field := range reflect.VisibleFields(conditionVal.Type()) {
			fieldValue := conditionVal.FieldByIndex(field.Index)
			switch field.Name {
			case "Type":
				condition.Type = fieldValue.String()
			case "Status":
				condition.Status = metav1.ConditionStatus(fieldValue.String())
			case "Reason":
				condition.Reason = fieldValue.String()
			case "Message":
				condition.Message = fieldValue.String()
			case "LastTransitionTime":
				condition.LastTransitionTime = fieldValue.Interface().(metav1.Time)
			}
		}
		metav1Conditions = append(metav1Conditions, condition)
	}

	return metav1Conditions, nil
}

// convertFromMetaV1 converts []metav1.Condition to the specific type of conditions used by the Kubernetes object.
func convertFromMetaV1(conditions []metav1.Condition, targetType reflect.Type) (interface{}, error) {
	if targetType.Kind() != reflect.Slice {
		return nil, fmt.Errorf("target type is not a slice")
	}

	elemType := targetType.Elem()
	if elemType.Kind() == reflect.Ptr {
		elemType = elemType.Elem()
	}

	result := reflect.MakeSlice(targetType, len(conditions), len(conditions))
	for i, cond := range conditions {
		targetCond := reflect.New(elemType).Elem()

		for _, field := range reflect.VisibleFields(elemType) {
			fieldValue := targetCond.FieldByIndex(field.Index)
			switch field.Name {
			case "Type":
				fieldValue.SetString(cond.Type)
			case "Status":
				fieldValue.SetString(string(cond.Status))
			case "Reason":
				fieldValue.SetString(cond.Reason)
			case "Message":
				fieldValue.SetString(cond.Message)
			case "LastTransitionTime":
				fieldValue.Set(reflect.ValueOf(cond.LastTransitionTime))
			}
		}

		if targetType.Elem().Kind() == reflect.Ptr {
			result.Index(i).Set(targetCond.Addr())
		} else {
			result.Index(i).Set(targetCond)
		}
	}

	return result.Interface(), nil
}

// setConditions sets the "conditions" field in the status of a Kubernetes object using type casting.
func setConditions(obj client.Object, conditions []metav1.Condition, scheme *runtime.Scheme) error {
	gvk, err := apiutil.GVKForObject(obj, scheme)
	if err != nil {
		return fmt.Errorf("problem getting the GVK: %w", err)
	}

	// Use the scheme to get the specific type of the object.
	objTyped, err := scheme.New(gvk)
	if err != nil {
		return fmt.Errorf("problem creating new object of kind %s: %w", gvk.Kind, err)
	}

	// Cast the object to its specific type.
	if err := scheme.Convert(obj, objTyped, nil); err != nil {
		return fmt.Errorf("problem converting object to kind %s: %w", gvk.Kind, err)
	}

	// Use reflection to set the conditions field.
	val := reflect.ValueOf(objTyped)
	statusField := val.Elem().FieldByName("Status")
	if !statusField.IsValid() {
		return fmt.Errorf("status field not found in kind %s", gvk.Kind)
	}

	conditionsField := statusField.FieldByName("Conditions")
	if !conditionsField.IsValid() {
		return fmt.Errorf("conditions field not found in kind %s", gvk.Kind)
	}

	convertedConditions, err := convertFromMetaV1(conditions, conditionsField.Type())
	if err != nil {
		return fmt.Errorf("error converting conditions for kind %s: %w", gvk.Kind, err)
	}

	conditionsField.Set(reflect.ValueOf(convertedConditions))

	// Convert the typed object back to the unstructured object.
	if err := scheme.Convert(objTyped, obj, nil); err != nil {
		return fmt.Errorf("problem converting object back to unstructured: %w", err)
	}

	return nil
}

// setConditionMessage sets the message for a specific condition type in a Kubernetes object.
func setConditionMessage(conditionType, message string, obj client.Object, scheme *runtime.Scheme) error {
	// this prevents any accidental duplicates
	deleteCondition(conditionType, obj, scheme)

	conditions, err := getConditions(obj, scheme)
	if err != nil {
		return err
	}

	// Add the condition if it doesn't exist
	newCondition := metav1.Condition{
		Type:               conditionType,
		Status:             metav1.ConditionUnknown,
		LastTransitionTime: metav1.Now(),
		Message:            message,
	}
	conditions = append(conditions, newCondition)

	// Set the updated conditions back to the object
	return setConditions(obj, conditions, scheme)
}

func deleteCondition(conditionType string, obj client.Object, scheme *runtime.Scheme) error {
	conditions, err := getConditions(obj, scheme)
	if err != nil {
		return err
	}

	outConditions := []metav1.Condition{}
	for _, condition := range conditions {
		if condition.Type != conditionType {
			outConditions = append(outConditions, condition)
		}
	}

	// Set the updated conditions back to the object
	return setConditions(obj, outConditions, scheme)
}

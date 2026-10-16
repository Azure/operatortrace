// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// pkg/predicates/ignore_trace_annotation_update.go

package predicates

import (
	"fmt"

	"github.com/Azure/operatortrace/operatortrace-go/pkg/constants"
	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

type IgnoreTraceAnnotationUpdatePredicate = TypedIgnoreTraceAnnotationUpdatePredicate[client.Object]

// NewTypedIgnoreAnnotationUpdatePredicate creates a predicate that ignores updates
// where only the provided annotation keys change (or defaults to trace-related keys
// when none are provided).
func NewTypedIgnoreAnnotationUpdatePredicate[T client.Object](ignoredAnnotationKeys ...string) TypedIgnoreTraceAnnotationUpdatePredicate[T] {
	return TypedIgnoreTraceAnnotationUpdatePredicate[T]{
		ignoredAnnotationKeys: ignoredAnnotationKeys,
	}
}

// IgnoreTraceAnnotationUpdatePredicate implements a predicate that ignores updates
// where only the trace ID and span ID annotations, or resource version changes.
type TypedIgnoreTraceAnnotationUpdatePredicate[T client.Object] struct {
	predicate.Funcs
	ignoredAnnotationKeys []string
}

// Create implements the create event check for the predicate.
func (TypedIgnoreTraceAnnotationUpdatePredicate[T]) Create(e event.TypedCreateEvent[T]) bool {
	return true
}

// Delete implements the delete event check for the predicate.
func (TypedIgnoreTraceAnnotationUpdatePredicate[T]) Delete(e event.TypedDeleteEvent[T]) bool {
	return true
}

// Generic implements the generic event check for the predicate.
func (TypedIgnoreTraceAnnotationUpdatePredicate[T]) Generic(e event.TypedGenericEvent[T]) bool {
	return true
}

// Update implements the update event check for the predicate.
func (p TypedIgnoreTraceAnnotationUpdatePredicate[T]) Update(e event.TypedUpdateEvent[T]) bool {
	changed, err := p.updateWithError(e)
	if err != nil {
		log.Log.Error(err, "Unable to compare objects, processing update")
	}
	return changed || err != nil
}

func (p TypedIgnoreTraceAnnotationUpdatePredicate[T]) updateWithError(e event.TypedUpdateEvent[T]) (bool, error) {
	oldObj := e.ObjectOld.DeepCopyObject()
	newObj := e.ObjectNew.DeepCopyObject()
	if oldObj == nil || newObj == nil {
		return true, nil
	}

	oldAnnotations := e.ObjectOld.GetAnnotations()
	newAnnotations := e.ObjectNew.GetAnnotations()

	ignoredAnnotations := append(
		[]string{
			constants.DefaultTraceParentAnnotation,
			constants.DefaultTraceStateAnnotation,
			constants.LegacyTraceIDAnnotation,
			constants.LegacySpanIDAnnotation,
			constants.LegacyTraceIDTimeAnnotation,
		},
		p.ignoredAnnotationKeys...,
	)

	// check if metadata except annotations have changed
	labelsChanged := !equality.Semantic.DeepEqual(e.ObjectOld.GetLabels(), e.ObjectNew.GetLabels())
	finalizersChanged := !equality.Semantic.DeepEqual(e.ObjectOld.GetFinalizers(), e.ObjectNew.GetFinalizers())
	ownerReferenceChanged := !equality.Semantic.DeepEqual(e.ObjectOld.GetOwnerReferences(), e.ObjectNew.GetOwnerReferences())

	otherAnnotationsChanged := !equalExcept(
		oldAnnotations,
		newAnnotations,
		ignoredAnnotations...,
	)

	// if other annotations changed or spec/status changed, we want to process the update
	if labelsChanged || finalizersChanged || ownerReferenceChanged || otherAnnotationsChanged {
		return true, nil
	}

	return hasSpecOrStatusOrDataChanged(oldObj, newObj)
}

// HasSignificantUpdate returns true if there's a significant difference between two objects,
// ignoring trace/span annotations and resourceVersion changes. Inspection failures return true.
func HasSignificantUpdate(oldObj, newObj runtime.Object) bool {
	changed, err := HasSignificantUpdateWithError(oldObj, newObj)
	if err != nil {
		log.Log.Error(err, "Unable to compare objects, treating update as significant")
	}
	return changed || err != nil
}

// HasSignificantUpdateWithError reports significant changes without hiding inspection failures.
// Callers decide whether to perform a write when comparison is inconclusive.
func HasSignificantUpdateWithError(oldObj, newObj runtime.Object) (bool, error) {
	oldObject, ok := oldObj.(client.Object)
	if !ok || oldObject == nil {
		return false, fmt.Errorf("old object %T does not implement client.Object", oldObj)
	}
	newObject, ok := newObj.(client.Object)
	if !ok || newObject == nil {
		return false, fmt.Errorf("new object %T does not implement client.Object", newObj)
	}
	updateEvent := event.UpdateEvent{
		ObjectOld: oldObject,
		ObjectNew: newObject,
	}
	predicate := TypedIgnoreTraceAnnotationUpdatePredicate[client.Object]{}
	return predicate.updateWithError(updateEvent)
}

// hasSpecOrStatusOrDataChanged checks if the spec, status, or data fields have changed.
func hasSpecOrStatusOrDataChanged(oldObj, newObj runtime.Object) (bool, error) {
	oldUnstructured, err := objToUnstructured(oldObj)
	if err != nil {
		return false, fmt.Errorf("failed to convert old object: %w", err)
	}
	newUnstructured, err := objToUnstructured(newObj)
	if err != nil {
		return false, fmt.Errorf("failed to convert new object: %w", err)
	}

	// Replace empty structs or slices with nil
	replaceEmptyStructsAndSlicesWithNil(oldUnstructured)
	replaceEmptyStructsAndSlicesWithNil(newUnstructured)

	oldStatus, err := getFieldExcludingObservedGeneration(oldUnstructured, "status")
	if err != nil {
		return false, fmt.Errorf("failed to inspect old status: %w", err)
	}
	newStatus, err := getFieldExcludingObservedGeneration(newUnstructured, "status")
	if err != nil {
		return false, fmt.Errorf("failed to inspect new status: %w", err)
	}

	specChanged, err := hasFieldChanged(oldUnstructured, newUnstructured, "spec")
	if err != nil {
		return false, err
	}
	statusChanged := !equality.Semantic.DeepEqual(oldStatus, newStatus)
	dataChanged, err := hasFieldChanged(oldUnstructured, newUnstructured, "data")
	if err != nil {
		return false, err
	}

	return specChanged || statusChanged || dataChanged, nil
}

// getFieldExcludingObservedGeneration retrieves the field and excludes the observedGeneration.
func getFieldExcludingObservedGeneration(obj map[string]interface{}, field string) (interface{}, error) {
	status, found, err := unstructured.NestedFieldNoCopy(obj, field)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, nil
	}
	if statusMap, ok := status.(map[string]interface{}); ok {
		delete(statusMap, "observedGeneration")
		if err := removeTraceAndSpanConditions(statusMap); err != nil {
			return nil, err
		}
		return statusMap, nil
	}
	return status, nil
}

// hasFieldChanged checks if a specific field has changed between old and new unstructured objects.
func hasFieldChanged(oldUnstructured, newUnstructured map[string]interface{}, field string) (bool, error) {
	oldField, foundOld, errOld := unstructuredNestedFieldNoCopy(oldUnstructured, field)
	newField, foundNew, errNew := unstructuredNestedFieldNoCopy(newUnstructured, field)

	if errOld != nil {
		return false, fmt.Errorf("failed to inspect old %s: %w", field, errOld)
	}
	if errNew != nil {
		return false, fmt.Errorf("failed to inspect new %s: %w", field, errNew)
	}
	if foundOld != foundNew {
		return true, nil
	}

	// Check if the fields are semantically equal
	return !equality.Semantic.DeepEqual(oldField, newField), nil
}

// Checks if two maps are equal, ignoring certain keys.
func equalExcept(a, b map[string]string, keysToIgnore ...string) bool {
	ignored := make(map[string]struct{})
	for _, key := range keysToIgnore {
		ignored[key] = struct{}{}
	}

	for key, aValue := range a {
		if _, isIgnored := ignored[key]; !isIgnored {
			if bValue, exists := b[key]; !exists || aValue != bValue {
				return false
			}
		}
	}
	for key := range b {
		if _, exists := a[key]; !exists {
			if _, isIgnored := ignored[key]; !isIgnored {
				return false
			}
		}
	}
	return true
}

// Recursively replaces empty structs or slices in the map with nil.
func replaceEmptyStructsAndSlicesWithNil(m map[string]interface{}) {
	for k, v := range m {
		switch val := v.(type) {
		case map[string]interface{}:
			if len(val) == 0 {
				m[k] = nil
			} else {
				replaceEmptyStructsAndSlicesWithNil(val)
			}
		case []interface{}:
			if len(val) == 0 {
				m[k] = nil
			} else {
				allElementsEmpty := true
				for _, elem := range val {
					if elemMap, ok := elem.(map[string]interface{}); ok {
						replaceEmptyStructsAndSlicesWithNil(elemMap)
						if len(elemMap) > 0 {
							allElementsEmpty = false
						}
					} else {
						allElementsEmpty = false
					}
				}
				if allElementsEmpty {
					m[k] = nil
				}
			}
		}
	}
}

func objToUnstructured(obj runtime.Object) (map[string]interface{}, error) {
	return runtime.DefaultUnstructuredConverter.ToUnstructured(obj)
}

func unstructuredNestedFieldNoCopy(obj map[string]interface{}, fields ...string) (interface{}, bool, error) {
	val, found, err := unstructured.NestedFieldNoCopy(obj, fields...)
	if !found || err != nil {
		return nil, false, err
	}
	return val, true, nil
}

// removeTraceAndSpanConditions removes conditions with Type 'TraceID' or 'SpanID' from the status.
func removeTraceAndSpanConditions(statusMap map[string]interface{}) error {
	if statusMap["conditions"] == nil {
		return nil
	}
	conditions, found, err := unstructured.NestedSlice(statusMap, "conditions")
	if err != nil {
		return err
	}
	if !found {
		return nil
	}
	filteredConditions := []interface{}{}
	for _, condition := range conditions {
		conditionMap, ok := condition.(map[string]interface{})
		if !ok {
			filteredConditions = append(filteredConditions, condition)
			continue
		}
		conditionType, _, err := unstructured.NestedString(conditionMap, "type")
		if err != nil {
			return err
		}
		if conditionType != "TraceID" && conditionType != "SpanID" {
			filteredConditions = append(filteredConditions, condition)
		}
	}
	statusMap["conditions"] = filteredConditions
	return nil
}

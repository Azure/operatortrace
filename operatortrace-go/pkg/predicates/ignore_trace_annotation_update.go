// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// pkg/predicates/ignore_trace_annotation_update.go

package predicates

import (
	"github.com/Azure/operatortrace/operatortrace-go/pkg/constants"
	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

// IgnoreTraceAnnotationUpdatePredicate implements a predicate that ignores updates
// where only the trace ID and span ID annotations, or resource version changes.
type IgnoreTraceAnnotationUpdatePredicate struct {
	predicate.Funcs
}

// Update implements the update event check for the predicate.
func (IgnoreTraceAnnotationUpdatePredicate) Update(e event.UpdateEvent) bool {
	if e.ObjectOld == nil || e.ObjectNew == nil {
		return true
	}

	oldAnnotations := e.ObjectOld.GetAnnotations()
	newAnnotations := e.ObjectNew.GetAnnotations()

	// check if metadata except annotations have changed
	labelsChanged := !equality.Semantic.DeepEqual(e.ObjectOld.GetLabels(), e.ObjectNew.GetLabels())
	finalizersChanged := !equality.Semantic.DeepEqual(e.ObjectOld.GetFinalizers(), e.ObjectNew.GetFinalizers())
	ownerReferenceChanged := !equality.Semantic.DeepEqual(e.ObjectOld.GetOwnerReferences(), e.ObjectNew.GetOwnerReferences())

	otherAnnotationsChanged := !equalExcept(oldAnnotations, newAnnotations, constants.TraceIDAnnotation, constants.SpanIDAnnotation, constants.TraceIDTimeAnnotation)

	// Check if the spec or status fields have changed
	specOrStatusChanged := hasSpecOrStatusChanged(e.ObjectOld, e.ObjectNew)

	// if other annotations changed or spec/status changed, we want to process the update
	if labelsChanged || finalizersChanged || ownerReferenceChanged || otherAnnotationsChanged || specOrStatusChanged {
		return true
	}

	// Otherwise, indicate the update should not be processed
	return false
}

// HasSignificantUpdate returns true if there's a significant difference between two objects,
// ignoring trace/span annotations and resourceVersion changes.
func HasSignificantUpdate(oldObj, newObj runtime.Object) bool {
	updateEvent := event.UpdateEvent{
		ObjectOld: oldObj.(client.Object),
		ObjectNew: newObj.(client.Object),
	}
	predicate := IgnoreTraceAnnotationUpdatePredicate{}
	return predicate.Update(updateEvent)
}

// hasSpecOrStatusChanged checks if the spec or status fields have changed.
func hasSpecOrStatusChanged(oldObj, newObj runtime.Object) bool {
	oldUnstructured := objToUnstructured(oldObj)
	newUnstructured := objToUnstructured(newObj)

	// Replace empty structs or slices with nil
	replaceEmptyStructsAndSlicesWithNil(oldUnstructured)
	replaceEmptyStructsAndSlicesWithNil(newUnstructured)

	oldStatus := getFieldExcludingObservedGeneration(oldUnstructured, "status")
	newStatus := getFieldExcludingObservedGeneration(newUnstructured, "status")

	return hasFieldChanged(oldUnstructured, newUnstructured, "spec") || !equality.Semantic.DeepEqual(oldStatus, newStatus)
}

// getFieldExcludingObservedGeneration retrieves the field and excludes the observedGeneration.
func getFieldExcludingObservedGeneration(obj map[string]interface{}, field string) interface{} {
	status, found, err := unstructured.NestedFieldNoCopy(obj, field)
	if err != nil || !found {
		return nil
	}
	if statusMap, ok := status.(map[string]interface{}); ok {
		delete(statusMap, "observedGeneration")
		removeTraceAndSpanConditions(statusMap)
		return statusMap
	}
	return status
}

// hasFieldChanged checks if a specific field has changed between old and new unstructured objects.
func hasFieldChanged(oldUnstructured, newUnstructured map[string]interface{}, field string) bool {
	oldField, foundOld, errOld := unstructuredNestedFieldNoCopy(oldUnstructured, field)
	newField, foundNew, errNew := unstructuredNestedFieldNoCopy(newUnstructured, field)

	// If there was an error accessing the field, or if one found and the other not found
	if errOld != nil || errNew != nil || foundOld != foundNew {
		return true
	}

	// Check if the fields are semantically equal
	return !equality.Semantic.DeepEqual(oldField, newField)
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

func objToUnstructured(obj runtime.Object) map[string]interface{} {
	unstructuredMap, _ := runtime.DefaultUnstructuredConverter.ToUnstructured(obj)
	return unstructuredMap
}

func unstructuredNestedFieldNoCopy(obj map[string]interface{}, fields ...string) (interface{}, bool, error) {
	val, found, err := unstructured.NestedFieldNoCopy(obj, fields...)
	if !found || err != nil {
		return nil, false, err
	}
	return val, true, nil
}

// removeTraceAndSpanConditions removes conditions with Type 'TraceID' or 'SpanID' from the status.
func removeTraceAndSpanConditions(statusMap map[string]interface{}) {
	conditions, found, err := unstructured.NestedSlice(statusMap, "conditions")
	if err != nil || !found {
		return
	}
	filteredConditions := []interface{}{}
	for _, condition := range conditions {
		if conditionMap, ok := condition.(map[string]interface{}); ok {
			conditionType, _, _ := unstructured.NestedString(conditionMap, "type")
			if conditionType != "TraceID" && conditionType != "SpanID" {
				filteredConditions = append(filteredConditions, condition)
			}
		}
	}
	statusMap["conditions"] = filteredConditions
}

// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// pkg/client/conditions.go

package client

import (
	"fmt"
	"reflect"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
)

// getConditionTime retrieves the time for a specific condition type from a Kubernetes object.
func getConditionTime(conditionType string, obj client.Object, scheme *runtime.Scheme) (metav1.Time, error) {
	conditions, err := getConditionsAsMap(obj, scheme)
	if err != nil {
		return metav1.Time{}, err
	}

	for _, condition := range conditions {
		// Check if "Type" key exists
		conType, exists := condition["Type"]
		if !exists {
			return metav1.Time{}, fmt.Errorf("condition does not contain a 'Type' field")
		}

		// Convert conType to string using reflection
		conTypeStr, err := convertToString(conType)
		if err != nil {
			return metav1.Time{}, fmt.Errorf("failed to convert 'Type' field to string: %v", err)
		}

		if conTypeStr == conditionType {
			time := condition["LastTransitionTime"].(metav1.Time)
			return time, nil
		}
	}

	return metav1.Time{}, fmt.Errorf("condition of type %s not found", conditionType)
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

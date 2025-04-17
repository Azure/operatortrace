// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// pkg/client/utils.go

package client

import (
	"fmt"
	"reflect"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

// getCallerKindFromNamespacedName extracts the caller kind from the key.Name
func getCallerKindFromNamespacedName(key client.ObjectKey) string {
	embedTraceID := &EmbedTraceID{}
	if err := embedTraceID.FromString(key.Name); err != nil {
		return ""
	}
	return embedTraceID.ObjectKind
}

// getCallerNameFromNamespacedName extracts the caller name from the key.Name
func getCallerNameFromNamespacedName(key client.ObjectKey) string {
	embedTraceID := &EmbedTraceID{}
	if err := embedTraceID.FromString(key.Name); err != nil {
		return ""
	}
	return embedTraceID.ObjectName
}

// getNameFromNamespacedName extracts the original name from the key.Name
func getNameFromNamespacedName(key client.ObjectKey) string {
	embedTraceID := &EmbedTraceID{}
	if err := embedTraceID.FromString(key.Name); err != nil {
		return key.Name
	}
	return embedTraceID.KeyName
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

// objectSemanticEqual compares two objects, ignoring traceid/spanid annotations
func objectSemanticEqual(obj1, obj2 client.Object) bool {
	copyObj1 := obj1.DeepCopyObject().(client.Object)
	copyObj2 := obj2.DeepCopyObject().(client.Object)

	return reflect.DeepEqual(copyObj1, copyObj2)
}

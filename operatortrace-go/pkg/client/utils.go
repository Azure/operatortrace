// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// pkg/client/utils.go

package client

import (
	"fmt"
	"reflect"

	tracingtypes "github.com/Azure/operatortrace/operatortrace-go/pkg/types"
	"k8s.io/apimachinery/pkg/types"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	ctrlreconcile "sigs.k8s.io/controller-runtime/pkg/reconcile"
)

func ClientObjectToRequestWithTraceID(key *ctrlclient.ObjectKey) tracingtypes.RequestWithTraceID {
	return tracingtypes.RequestWithTraceID{
		Request: ctrlreconcile.Request{
			NamespacedName: types.NamespacedName{
				Name:      key.Name,
				Namespace: key.Namespace,
			},
		},
	}
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

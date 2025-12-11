/*
Copyright 2025.

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

package controller

import (
	"context"

	"github.com/go-logr/logr"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	appv1 "github.com/Azure/operatortrace/example/example-operator/api/v1"
	operatortrace "github.com/Azure/operatortrace/operatortrace-go/pkg/client"
	tracingreconcile "github.com/Azure/operatortrace/operatortrace-go/pkg/reconcile"
	tracingtypes "github.com/Azure/operatortrace/operatortrace-go/pkg/types"
	otelnoop "go.opentelemetry.io/otel/trace/noop"
)

var _ = Describe("Sample Controller", func() {
	Context("When reconciling a resource", func() {
		const resourceName = "test-resource"

		ctx := context.Background()

		typeNamespacedName := types.NamespacedName{
			Name:      resourceName,
			Namespace: "default", // TODO(user):Modify as needed
		}
		sample := &appv1.Sample{}

		BeforeEach(func() {
			By("creating the custom resource for the Kind Sample")
			err := k8sClient.Get(ctx, typeNamespacedName, sample)
			if err != nil && errors.IsNotFound(err) {
				resource := &appv1.Sample{
					ObjectMeta: metav1.ObjectMeta{
						Name:      resourceName,
						Namespace: "default",
					},
					Spec: appv1.SampleSpec{
						Foo: "bar",
						Bar: 1,
					},
				}
				Expect(k8sClient.Create(ctx, resource)).To(Succeed())
			}
		})

		AfterEach(func() {
			// TODO(user): Cleanup logic after each test, like removing the resource instance.
			resource := &appv1.Sample{}
			err := k8sClient.Get(ctx, typeNamespacedName, resource)
			Expect(err).NotTo(HaveOccurred())

			By("Cleanup the specific resource instance Sample")
			Expect(k8sClient.Delete(ctx, resource)).To(Succeed())
		})
		It("should successfully reconcile the resource", func() {
			By("Reconciling the created resource via tracing wrapper")
			tracer := otelnoop.NewTracerProvider().Tracer("test-tracer")
			tracingClient := operatortrace.NewTracingClientWithOptions(
				k8sClient,
				k8sClient,
				tracer,
				logr.Discard(),
				k8sClient.Scheme(),
				operatortrace.WithIncomingTraceRelationship(operatortrace.TraceParentRelationshipParent),
			)

			sampleReconciler := &SampleReconciler{
				Client: tracingClient,
				Scheme: k8sClient.Scheme(),
			}

			tracingReconciler := tracingreconcile.AsTracingReconciler(tracingClient, sampleReconciler)

			tracingRequest := tracingtypes.RequestWithTraceID{
				Request: reconcile.Request{
					NamespacedName: typeNamespacedName,
				},
			}

			_, err := tracingReconciler.Reconcile(ctx, tracingRequest)
			Expect(err).NotTo(HaveOccurred())
			// TODO(user): Add more specific assertions depending on your controller's reconciliation logic.
			// Example: If you expect a certain status condition after reconciliation, verify it here.
		})
	})
})

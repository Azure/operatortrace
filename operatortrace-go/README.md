# operatortrace-go
Golang library Implementation for OperatorTrace

## Installation

To install operatortrace, add it as a dependency to your Go project:

```bash
go get github.com/Azure/operatortrace/operatortrace-go/pkg/client
```

## Usage

### Integrating OperatorTrace in Your Controller

To integrate OperatorTrace into your Kubernetes controller, follow these steps:

1. Import OperatorTrace:

```golang
import (
    "context"
    "github.com/go-logr/logr"
    "github.com/Azure/operatortrace/operatortrace-go/pkg/client"
    "github.com/Azure/operatortrace/operatortrace-go/pkg/predicates"
    "go.opentelemetry.io/otel"
    "sigs.k8s.io/controller-runtime/pkg/client"
    "sigs.k8s.io/controller-runtime/pkg/reconcile"
    corev1 "k8s.io/api/core/v1"
)
```

2. Initialize OperatorTrace in Your Controller:

```golang
type MyController struct {
    Client *operatortrace.TracingClient
    Logger logr.Logger
}

func (r *MyController) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
    // Get the resource being reconciled
    pod := &corev1.Pod{}
    if err := r.Client.Get(ctx, req.NamespacedName, pod); err != nil {
        return reconcile.Result{}, client.IgnoreNotFound(err)
    }

    // Perform reconcile logic here
    // ...

    // Example: Trigger another resource update within the same trace context
    childPod := &corev1.Pod{}
    // Update or create the child resource
    if err := r.Client.Update(ctx, childPod); err != nil {
        return reconcile.Result{}, err
    }

    return reconcile.Result{}, nil
}
```

3. Create the Add Function

```golang
func Add(mgr manager.Manager, logger logr.Logger) error {
    // Setup the controller
    c, err := controller.New("my-controller", mgr, controller.Options{
        Reconciler: &MyController{
            Client: operatortrace.NewTracingClient(mgr.GetClient(), otel.Tracer("operatortrace"), logger),
            Logger: logger,
        },
    })
    if err != nil {
        return err
    }

    // Watch for changes to primary resource
    err = c.Watch(&source.Kind{Type: &corev1.Pod{}}, &handler.EnqueueRequestForObject{}, predicates.IgnoreTraceAnnotationUpdatePredicate{})
    if err != nil {
        return err
    }

    return nil
}
```

4. Set Up the Manager in the Main Package:

```golang
package main

import (
    "os"
    "github.com/go-logr/logr"
    "github.com/Azure/operatortrace/operatortrace-go/pkg/controllers"
    "sigs.k8s.io/controller-runtime/pkg/manager"
    "sigs.k8s.io/controller-runtime/pkg/log/zap"
    "sigs.k8s.io/controller-runtime/pkg/manager/signals"
)

func main() {
    // Set up the logger
    logger := zap.New(zap.UseDevMode(true))

    // Create a new manager
    mgr, err := manager.New(manager.GetConfigOrDie(), manager.Options{})
    if err != nil {
        logger.Error(err, "Unable to set up overall controller manager")
        os.Exit(1)
    }

    // Add the controller to the manager
    if err := controllers.Add(mgr, logger); err != nil {
        logger.Error(err, "Unable to add controller to manager")
        os.Exit(1)
    }

    // Start the manager
    if err := mgr.Start(signals.SetupSignalHandler()); err != nil {
        logger.Error(err, "Unable to start manager")
        os.Exit(1)
    }
} 
```

### Trace Cleanup

`EndTrace` removes only the configured emitted traceparent/tracestate annotations and legacy trace-id, span-id, and trace-id-time annotations that are present. It preserves unrelated annotations, including annotations written concurrently, and skips the metadata patch when no owned trace keys are present.

A status-subresource patch is sent only when legacy `TraceID` or `SpanID` conditions need removal. Cleanup uses the fetched object or newer metadata-patch response with an optimistic lock, so concurrent status updates produce conflicts instead of lost conditions. Resources without those conditions do not need status-patch permissions for trace cleanup. Read and patch failures are returned and recorded on the cleanup span.

When the supplied object has neither owned trace annotations nor legacy trace conditions, `EndTrace` performs no API reads or writes.

### Update Conflicts

When `Update` falls back to a merge patch because resource versions differ, it excludes differences in Kubernetes `apiVersion` and `kind` while retaining the caller's `resourceVersion` as an optimistic lock. A stale caller still receives a conflict rather than overwriting a newer server version.

### Release Notes (Unreleased)

Direct callers of `EndTrace` can now receive safety-read and metadata-patch errors that could previously be swallowed. Legacy status cleanup can also return resource-version conflicts. `AsTracingReconciler` continues to treat cleanup as best-effort and does not return those cleanup errors from reconciliation.

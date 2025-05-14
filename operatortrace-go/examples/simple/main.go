package main

import (
	"context"
	"os"

	tracingclient "github.com/Azure/operatortrace/operatortrace-go/pkg/client"
	operatortracePredicates "github.com/Azure/operatortrace/operatortrace-go/pkg/predicates"
	tracingtypes "github.com/Azure/operatortrace/operatortrace-go/pkg/types"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	_ "k8s.io/client-go/plugin/pkg/client/auth/gcp"
	ctrl "sigs.k8s.io/controller-runtime"
	api "sigs.k8s.io/controller-runtime/examples/crd/pkg"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

var (
	setupLog = ctrl.Log.WithName("setup")
)

type reconciler struct {
	tracingclient.TracingClient
	scheme *runtime.Scheme
}

func (r *reconciler) Reconcile(ctx context.Context, req tracingtypes.RequestWithTraceID) (ctrl.Result, error) {
	log := log.FromContext(ctx).WithValues("node", req.NamespacedName)
	log.V(1).Info("reconciling node")

	instance := &corev1.Node{}
	ctx, span, err := r.TracingClient.StartTrace(ctx, &req, instance)

	defer span.End()
	if err != nil {
		span.RecordError(err)
		return ctrl.Result{}, ctrlclient.IgnoreNotFound(err)
	}
	// log, _, spanextractor := util.Extractor(ctx, log, instance, "CERTIFICATEREQUEST_CONTROLLER")
	// defer spanextractor.End()

	return ctrl.Result{}, nil
}

func main() {
	ctrl.SetLogger(zap.New())

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{})
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	// in a real controller, we'd create a new scheme for this
	err = api.AddToScheme(mgr.GetScheme())
	if err != nil {
		setupLog.Error(err, "unable to add scheme")
		os.Exit(1)
	}

	// Create a real tracer
	otelTracer := initTracer()

	r := &reconciler{
		TracingClient: tracingclient.NewTracingClient(mgr.GetClient(), mgr.GetAPIReader(), otelTracer, zap.New(), mgr.GetScheme()),
		scheme:        mgr.GetScheme(),
	}
	err = tracingtypes.NewControllerManagedBy(mgr).
		For(&corev1.Node{}, builder.WithPredicates(operatortracePredicates.IgnoreTraceAnnotationUpdatePredicate{})).
		Complete(r)
	if err != nil {
		setupLog.Error(err, "unable to create controller")
		os.Exit(1)
	}

	setupLog.Info("starting manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}

func initTracer() trace.Tracer {
	exporter, err := stdouttrace.New(stdouttrace.WithPrettyPrint())
	if err != nil {
		panic(err)
	}
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
	)
	otel.SetTracerProvider(tp)

	return tp.Tracer("operatortrace")
}

package main

import (
	"context"
	"log"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.12.0"
)

func main() {
	ctx := context.Background()

	// Create OTLP HTTP exporter
	exporter, err := otlptracehttp.New(ctx, otlptracehttp.WithEndpoint("tempo.monitoring.svc.cluster.local:4318"), otlptracehttp.WithInsecure())
	if err != nil {
		log.Fatalf("Failed to create exporter: %v", err)
	}

	// Create trace provider
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(resource.NewWithAttributes(
			semconv.SchemaURL,
			semconv.ServiceNameKey.String("example-service"),
		)),
	)
	defer func() {
		if err := tp.Shutdown(ctx); err != nil {
			log.Fatalf("Failed to shutdown TracerProvider: %v", err)
		}
	}()

	otel.SetTracerProvider(tp)
	tracer := otel.Tracer("example-tracer")

	for {
		// Create a span
		_, span := tracer.Start(ctx, "example-operation")
		time.Sleep(2 * time.Second) // Simulate work
		span.End()

		log.Println("Span emitted!")

		// Sleep for a while before emitting the next span
		time.Sleep(5 * time.Second)
	}
}

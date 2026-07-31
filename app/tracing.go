package main

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
)

const tracerName = "github.com/lvcas-dotcom/go-observability-starter"

// setupTracing configura o exporter OTLP apontando para o Tempo e devolve a
// função de shutdown, que faz o flush do que ainda estiver em buffer.
//
// Sem endpoint configurado, cai para um tracer no-op em vez de derrubar o
// boot: quem adaptar este starter vai arrancar o backend de tracing primeiro.
func setupTracing(ctx context.Context, cfg Config) (trace.Tracer, func(context.Context) error, error) {
	// Os propagadores são definidos mesmo sem exporter, senão um traceparent
	// que chegue no header é descartado em silêncio.
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	if cfg.OTLPEndpoint == "" {
		tp := noop.NewTracerProvider()
		otel.SetTracerProvider(tp)
		return tp.Tracer(tracerName), func(context.Context) error { return nil }, nil
	}

	exporter, err := otlptracehttp.New(ctx,
		otlptracehttp.WithEndpoint(cfg.OTLPEndpoint),
		otlptracehttp.WithInsecure(),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("build otlp exporter: %w", err)
	}

	res, err := resource.Merge(
		resource.Default(),
		resource.NewSchemaless(
			attribute.String("service.name", cfg.ServiceName),
			attribute.String("service.version", cfg.ServiceVersion),
			attribute.String("deployment.environment", cfg.Environment),
		),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("build resource: %w", err)
	}

	provider := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
		// ParentBased deixa o trace distribuído tudo-ou-nada: decidido o
		// sampling na origem, todos os saltos seguintes acompanham.
		sdktrace.WithSampler(sdktrace.ParentBased(
			sdktrace.TraceIDRatioBased(cfg.TraceSampleRate),
		)),
	)
	otel.SetTracerProvider(provider)

	return provider.Tracer(tracerName), provider.Shutdown, nil
}

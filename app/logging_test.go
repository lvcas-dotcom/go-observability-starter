package main

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"testing"

	"go.opentelemetry.io/otel/trace"
)

// Se o trace_id parar de sair, todo link "Logs for this span" do Grafana fica
// vazio sem dar erro.
func TestLoggerInjectsTraceAndSpanID(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	logger := newLogger(&buf, Config{
		ServiceName:    "svc",
		ServiceVersion: "v1",
		Environment:    "test",
		LogLevel:       slog.LevelInfo,
	})

	traceID, err := trace.TraceIDFromHex("4bf92f3577b34da6a3ce929d0e0e4736")
	if err != nil {
		t.Fatalf("trace id: %v", err)
	}
	spanID, err := trace.SpanIDFromHex("00f067aa0ba902b7")
	if err != nil {
		t.Fatalf("span id: %v", err)
	}
	ctx := trace.ContextWithSpanContext(context.Background(), trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    traceID,
		SpanID:     spanID,
		TraceFlags: trace.FlagsSampled,
	}))

	logger.InfoContext(ctx, "hello")

	var rec map[string]any
	if err := json.Unmarshal(buf.Bytes(), &rec); err != nil {
		t.Fatalf("log line is not valid JSON: %v (%s)", err, buf.String())
	}
	if got := rec["trace_id"]; got != traceID.String() {
		t.Errorf("trace_id = %v, want %s", got, traceID)
	}
	if got := rec["span_id"]; got != spanID.String() {
		t.Errorf("span_id = %v, want %s", got, spanID)
	}
	for _, key := range []string{"service", "version", "env"} {
		if _, ok := rec[key]; !ok {
			t.Errorf("log line is missing the %q field", key)
		}
	}
}

func TestLoggerWithoutSpanOmitsTraceFields(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	logger := newLogger(&buf, Config{LogLevel: slog.LevelInfo})
	logger.InfoContext(context.Background(), "no span here")

	var rec map[string]any
	if err := json.Unmarshal(buf.Bytes(), &rec); err != nil {
		t.Fatalf("log line is not valid JSON: %v", err)
	}
	if _, ok := rec["trace_id"]; ok {
		t.Error("trace_id was emitted without an active span")
	}
}

// WithAttrs e WithGroup devolvem handler novo: sem reembrulhar, o logger
// derivado perde a correlação.
func TestLoggerKeepsTraceCorrelationAfterWith(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	logger := newLogger(&buf, Config{LogLevel: slog.LevelInfo}).With(slog.String("component", "worker"))

	traceID, _ := trace.TraceIDFromHex("4bf92f3577b34da6a3ce929d0e0e4736")
	spanID, _ := trace.SpanIDFromHex("00f067aa0ba902b7")
	ctx := trace.ContextWithSpanContext(context.Background(), trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: traceID, SpanID: spanID, TraceFlags: trace.FlagsSampled,
	}))

	logger.InfoContext(ctx, "derived")

	var rec map[string]any
	if err := json.Unmarshal(buf.Bytes(), &rec); err != nil {
		t.Fatalf("log line is not valid JSON: %v", err)
	}
	if _, ok := rec["trace_id"]; !ok {
		t.Error("derived logger lost trace correlation")
	}
}

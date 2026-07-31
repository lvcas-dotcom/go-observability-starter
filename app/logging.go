package main

import (
	"context"
	"io"
	"log/slog"

	"go.opentelemetry.io/otel/trace"
)

// traceHandler injeta trace_id e span_id do span ativo em cada registro. É o
// que faz o link log↔trace do Grafana funcionar, e só vale se o handler logar
// com contexto (InfoContext, não Info).
type traceHandler struct {
	slog.Handler
}

func (h traceHandler) Handle(ctx context.Context, rec slog.Record) error {
	if sc := trace.SpanContextFromContext(ctx); sc.IsValid() {
		rec.AddAttrs(
			slog.String("trace_id", sc.TraceID().String()),
			slog.String("span_id", sc.SpanID().String()),
		)
	}
	return h.Handler.Handle(ctx, rec)
}

// WithAttrs e WithGroup devolvem um handler novo; sem reembrulhar, qualquer
// logger derivado perde a correlação.
func (h traceHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return traceHandler{Handler: h.Handler.WithAttrs(attrs)}
}

func (h traceHandler) WithGroup(name string) slog.Handler {
	return traceHandler{Handler: h.Handler.WithGroup(name)}
}

func newLogger(w io.Writer, cfg Config) *slog.Logger {
	base := slog.NewJSONHandler(w, &slog.HandlerOptions{Level: cfg.LogLevel})
	return slog.New(traceHandler{Handler: base}).With(
		slog.String("service", cfg.ServiceName),
		slog.String("version", cfg.ServiceVersion),
		slog.String("env", cfg.Environment),
	)
}

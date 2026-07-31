package main

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
)

// Percentuais do comportamento simulado. Sem eles os painéis de erro e de
// latência não teriam o que mostrar no primeiro boot.
const (
	pctInvalidRequest = 3  // responde 400
	pctQueryFailure   = 8  // responde 500
	pctSlowQuery      = 10 // pega o caminho lento
)

var (
	errInvalidRequest   = errors.New("invalid request payload")
	errSimulatedFailure = errors.New("simulated downstream failure")
)

type errorBody struct {
	Error string `json:"error"`
}

// writeJSON centraliza a ordem dos headers: Content-Type tem que ser definido
// antes do WriteHeader, senão é ignorado.
func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if body == nil {
		return
	}
	_ = json.NewEncoder(w).Encode(body)
}

func (s *server) handleRoot(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{
		"service": s.cfg.ServiceName,
		"version": s.cfg.ServiceVersion,
		"docs":    "https://github.com/lvcas-dotcom/go-observability-starter",
	})
}

// handleWork simula uma requisição com trabalho real: spans aninhados,
// latência variável e falha ocasional.
func (s *server) handleWork(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if err := s.stepValidate(ctx); err != nil {
		writeJSON(w, http.StatusBadRequest, errorBody{Error: err.Error()})
		return
	}

	rows, err := s.stepQueryDatabase(ctx)
	if err != nil {
		// O detalhe fica no log, que carrega o trace_id. O cliente recebe
		// mensagem genérica — erro interno não vaza no body.
		s.logger.ErrorContext(ctx, "work failed", slog.String("error", err.Error()))
		writeJSON(w, http.StatusInternalServerError, errorBody{Error: "internal server error"})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"result": "ok", "rows": rows})
}

func (s *server) stepValidate(ctx context.Context) error {
	_, span := s.tracer.Start(ctx, "validate-request")
	defer span.End()

	sleep(ctx, time.Duration(rand.IntN(15))*time.Millisecond)

	if rand.IntN(100) < pctInvalidRequest {
		span.SetStatus(codes.Error, errInvalidRequest.Error())
		return errInvalidRequest
	}
	return nil
}

// stepQueryDatabase simula a dependência que quebra de verdade em produção:
// quase sempre rápida, às vezes lenta, de vez em quando fora.
func (s *server) stepQueryDatabase(ctx context.Context) (int, error) {
	ctx, span := s.tracer.Start(ctx, "query-database")
	defer span.End()

	span.SetAttributes(
		attribute.String("db.system", "postgresql"),
		attribute.String("db.operation", "SELECT"),
	)

	latency := time.Duration(20+rand.IntN(120)) * time.Millisecond
	if rand.IntN(100) < pctSlowQuery {
		latency += time.Duration(300+rand.IntN(700)) * time.Millisecond
		span.SetAttributes(attribute.Bool("db.slow_query", true))
	}
	sleep(ctx, latency)

	if rand.IntN(100) < pctQueryFailure {
		span.RecordError(errSimulatedFailure)
		span.SetStatus(codes.Error, errSimulatedFailure.Error())
		return 0, errSimulatedFailure
	}

	rows := rand.IntN(500)
	span.SetAttributes(attribute.Int("db.rows_affected", rows))
	return rows, nil
}

func (s *server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleReady é separado do liveness de propósito: juntar os dois é como
// começa um restart loop durante indisponibilidade de dependência.
func (s *server) handleReady(w http.ResponseWriter, r *http.Request) {
	if !s.ready.Load() {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "shutting down"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}

func (s *server) handleNotFound(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusNotFound, errorBody{Error: "not found"})
}

// sleep respeita o cancelamento do contexto: se o cliente desistir, a
// goroutine é liberada na hora em vez de esperar o prazo inteiro.
func sleep(ctx context.Context, d time.Duration) {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
	case <-timer.C:
	}
}

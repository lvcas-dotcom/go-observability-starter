package main

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"go.opentelemetry.io/otel/trace/noop"
)

func newTestServer(t *testing.T) *server {
	t.Helper()

	cfg := Config{
		ServiceName:    "test-service",
		ServiceVersion: "test",
		Environment:    "test",
		Port:           "0",
		LogLevel:       slog.LevelError, // silencia a saída do teste
	}
	logger := newLogger(io.Discard, cfg)
	return newServer(cfg, logger, newMetrics(cfg), noop.NewTracerProvider().Tracer("test"))
}

func TestRoutes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		method      string
		path        string
		wantStatus  []int
		wantJSONKey string
	}{
		{name: "root", method: http.MethodGet, path: "/", wantStatus: []int{http.StatusOK}, wantJSONKey: "service"},
		{name: "liveness", method: http.MethodGet, path: "/healthz", wantStatus: []int{http.StatusOK}, wantJSONKey: "status"},
		{name: "readiness", method: http.MethodGet, path: "/readyz", wantStatus: []int{http.StatusOK}, wantJSONKey: "status"},
		// /work falha de propósito, então os três desfechos valem.
		{name: "work", method: http.MethodGet, path: "/work", wantStatus: []int{http.StatusOK, http.StatusBadRequest, http.StatusInternalServerError}},
		{name: "unknown path is a real 404", method: http.MethodGet, path: "/definitely-not-a-route", wantStatus: []int{http.StatusNotFound}, wantJSONKey: "error"},
		{name: "nested unknown path is a real 404", method: http.MethodGet, path: "/a/b/c", wantStatus: []int{http.StatusNotFound}, wantJSONKey: "error"},
		{name: "wrong method is rejected", method: http.MethodPost, path: "/work", wantStatus: []int{http.StatusMethodNotAllowed, http.StatusNotFound}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			srv := newTestServer(t)
			rec := httptest.NewRecorder()
			srv.routes().ServeHTTP(rec, httptest.NewRequest(tc.method, tc.path, nil))

			if !slices.Contains(tc.wantStatus, rec.Code) {
				t.Fatalf("status = %d, want one of %v (body: %s)", rec.Code, tc.wantStatus, rec.Body.String())
			}
			if tc.wantJSONKey == "" {
				return
			}

			if ct := rec.Header().Get("Content-Type"); ct != "application/json; charset=utf-8" {
				t.Errorf("Content-Type = %q, want JSON", ct)
			}
			var body map[string]any
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("response is not valid JSON: %v (body: %s)", err, rec.Body.String())
			}
			if _, ok := body[tc.wantJSONKey]; !ok {
				t.Errorf("response is missing key %q: %v", tc.wantJSONKey, body)
			}
		})
	}
}

// A resposta de erro já saiu sem Content-Type porque o header nunca era
// definido no caminho de falha.
func TestErrorResponsesAreTypedJSON(t *testing.T) {
	t.Parallel()

	srv := newTestServer(t)
	rec := httptest.NewRecorder()
	srv.routes().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/nope", nil))

	if got, want := rec.Header().Get("Content-Type"), "application/json; charset=utf-8"; got != want {
		t.Fatalf("Content-Type = %q, want %q", got, want)
	}
	var body errorBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("error body is not valid JSON: %v", err)
	}
	if body.Error == "" {
		t.Error("error body has an empty error field")
	}
}

// Readiness cai sozinho: durante o shutdown o processo continua vivo, mas
// precisa parar de receber tráfego.
func TestReadinessFailsOnceDraining(t *testing.T) {
	t.Parallel()

	srv := newTestServer(t)
	srv.ready.Store(false)
	handler := srv.routes()

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("readyz while draining = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}

	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("healthz while draining = %d, want %d (liveness must not follow readiness)", rec.Code, http.StatusOK)
	}
}

// Detalhe de erro interno fica no log, que carrega o trace ID. No body ele
// vazaria implementação para o cliente.
func TestServerErrorsDoNotLeakInternalDetail(t *testing.T) {
	t.Parallel()

	srv := newTestServer(t)
	handler := srv.routes()

	for range 200 {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/work", nil))
		if rec.Code != http.StatusInternalServerError {
			continue
		}
		if strings.Contains(rec.Body.String(), errSimulatedFailure.Error()) {
			t.Fatalf("500 body leaked the internal error: %s", rec.Body.String())
		}
		return // viu pelo menos um 500 e ele estava limpo
	}
	t.Skip("no 500 observed in 200 attempts")
}

func TestMetricsEndpointExposesAppAndRuntimeMetrics(t *testing.T) {
	t.Parallel()

	srv := newTestServer(t)
	handler := srv.routes()

	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("/metrics status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{
		"http_requests_total",
		"http_request_duration_seconds",
		"http_requests_in_flight",
		"app_build_info",
		"go_goroutines",              // collector de runtime do Go
		"process_start_time_seconds", // collector de processo
	} {
		if !strings.Contains(body, want) {
			t.Errorf("/metrics output is missing %q", want)
		}
	}
}

// O scrape não pode aparecer na taxa de requisição do próprio serviço.
func TestMetricsEndpointIsNotSelfInstrumented(t *testing.T) {
	t.Parallel()

	srv := newTestServer(t)
	handler := srv.routes()

	for range 3 {
		handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/metrics", nil))
	}

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if strings.Contains(rec.Body.String(), `route="/metrics"`) {
		t.Error("/metrics is counting its own scrapes, which inflates the request rate")
	}
}

package main

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestInstrumentRecordsRedMetrics(t *testing.T) {
	t.Parallel()

	srv := newTestServer(t)
	handler := srv.instrument("/probe", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusTeapot, map[string]string{"hello": "world"})
	}))

	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/probe", nil))

	got := testutil.ToFloat64(srv.metrics.requests.WithLabelValues("GET", "/probe", "418"))
	if got != 1 {
		t.Errorf("http_requests_total{route=/probe,status=418} = %v, want 1", got)
	}
	if n := testutil.CollectAndCount(srv.metrics.duration); n == 0 {
		t.Error("http_request_duration_seconds recorded no observation")
	}
	if n := testutil.CollectAndCount(srv.metrics.respSize); n == 0 {
		t.Error("http_response_size_bytes recorded no observation")
	}
}

// O label vem do padrão de rota, nunca da URL crua: um endpoint com ID no
// path explodiria a cardinalidade.
func TestInstrumentUsesRouteLabelNotRawPath(t *testing.T) {
	t.Parallel()

	srv := newTestServer(t)
	handler := srv.instrument("/items/{id}", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	for _, id := range []string{"1", "2", "3", "4", "5"} {
		handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/items/"+id, nil))
	}

	if n := testutil.CollectAndCount(srv.metrics.requests); n != 1 {
		t.Errorf("distinct http_requests_total series = %d, want 1 (cardinality leak from the raw path)", n)
	}
	if got := testutil.ToFloat64(srv.metrics.requests.WithLabelValues("GET", "/items/{id}", "200")); got != 5 {
		t.Errorf("counter for the route = %v, want 5", got)
	}
}

// Handler que só chama Write, sem WriteHeader, tem que ser gravado como 200
// e não como status zero.
func TestInstrumentDefaultsToStatus200(t *testing.T) {
	t.Parallel()

	srv := newTestServer(t)
	handler := srv.instrument("/plain", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("no explicit WriteHeader here"))
	}))

	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/plain", nil))

	if got := testutil.ToFloat64(srv.metrics.requests.WithLabelValues("GET", "/plain", "200")); got != 1 {
		t.Errorf("counter for implicit 200 = %v, want 1", got)
	}
}

func TestInstrumentTracksInFlightAndReturnsToZero(t *testing.T) {
	t.Parallel()

	srv := newTestServer(t)
	var inFlightDuringRequest float64

	handler := srv.instrument("/slow", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		inFlightDuringRequest = testutil.ToFloat64(srv.metrics.inFlight)
		w.WriteHeader(http.StatusOK)
	}))
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/slow", nil))

	if inFlightDuringRequest != 1 {
		t.Errorf("in-flight during the request = %v, want 1", inFlightDuringRequest)
	}
	if got := testutil.ToFloat64(srv.metrics.inFlight); got != 0 {
		t.Errorf("in-flight after the request = %v, want 0 (gauge leak)", got)
	}
}

// Envolver o ResponseWriter sem repassar Flush quebra SSE em silêncio.
func TestResponseRecorderPreservesFlusher(t *testing.T) {
	t.Parallel()

	inner := httptest.NewRecorder()
	rec := newResponseRecorder(inner)

	if _, ok := any(rec).(http.Flusher); !ok {
		t.Fatal("responseRecorder does not implement http.Flusher; streaming handlers would break")
	}
	rec.WriteHeader(http.StatusOK)
	http.NewResponseController(rec).Flush()

	if !inner.Flushed {
		t.Error("Flush did not reach the underlying ResponseWriter")
	}
}

func TestResponseRecorderCountsBytes(t *testing.T) {
	t.Parallel()

	rec := newResponseRecorder(httptest.NewRecorder())
	payload := []byte("twenty-four bytes long..")
	if _, err := rec.Write(payload); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if rec.bytes != int64(len(payload)) {
		t.Errorf("bytes = %d, want %d", rec.bytes, len(payload))
	}
}

// O primeiro status vence: escrever o body e depois tentar trocar o status
// não pode corromper o que foi gravado.
func TestResponseRecorderKeepsFirstStatus(t *testing.T) {
	t.Parallel()

	rec := newResponseRecorder(httptest.NewRecorder())
	rec.WriteHeader(http.StatusCreated)
	rec.WriteHeader(http.StatusInternalServerError)

	if rec.status != http.StatusCreated {
		t.Errorf("status = %d, want %d", rec.status, http.StatusCreated)
	}
}

// Handler que entra em panic tem que aparecer nas métricas. Com o recover por
// fora da instrumentação, o panic passa direto pelo código que grava e o
// endpoint que mais quebra some do gráfico.
func TestPanicIsRecordedInMetrics(t *testing.T) {
	t.Parallel()

	srv := newTestServer(t)
	handler := srv.wrap("/boom", func(w http.ResponseWriter, r *http.Request) {
		panic("boom")
	})

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/boom", nil))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	if got := testutil.ToFloat64(srv.metrics.requests.WithLabelValues("GET", "/boom", "500")); got != 1 {
		t.Errorf("http_requests_total for the panicking route = %v, want 1", got)
	}
	if got := testutil.ToFloat64(srv.metrics.inFlight); got != 0 {
		t.Errorf("in-flight after a panic = %v, want 0 (gauge leak)", got)
	}
}

func TestRecoverPanicReturns500(t *testing.T) {
	t.Parallel()

	srv := newTestServer(t)
	handler := srv.recoverPanic(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("boom")
	}))

	rec := httptest.NewRecorder()
	// Sem o recover funcionando, isso derruba o binário de teste.
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status after panic = %d, want 500", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json; charset=utf-8" {
		t.Errorf("Content-Type after panic = %q, want JSON", ct)
	}
}

// http.ErrAbortHandler derruba a conexão de propósito e precisa continuar
// subindo em vez de virar 500.
func TestRecoverPanicRepanicsOnErrAbortHandler(t *testing.T) {
	t.Parallel()

	srv := newTestServer(t)
	handler := srv.recoverPanic(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic(http.ErrAbortHandler)
	}))

	defer func() {
		rvr := recover()
		if err, ok := rvr.(error); !ok || !errors.Is(err, http.ErrAbortHandler) {
			t.Errorf("recovered %v, want ErrAbortHandler to propagate", rvr)
		}
	}()
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
}

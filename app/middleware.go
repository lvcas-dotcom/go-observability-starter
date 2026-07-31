package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"runtime/debug"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// responseRecorder guarda o status e o tamanho da resposta para o middleware
// de métricas e log.
//
// Envolver o ResponseWriter sem repassar Flush e Hijack quebra SSE e WebSocket
// em silêncio: o wrapper deixa de satisfazer as interfaces e o handler de
// baixo não tem como perceber.
type responseRecorder struct {
	http.ResponseWriter
	status      int
	bytes       int64
	wroteHeader bool
}

func newResponseRecorder(w http.ResponseWriter) *responseRecorder {
	return &responseRecorder{ResponseWriter: w, status: http.StatusOK}
}

func (r *responseRecorder) WriteHeader(status int) {
	if r.wroteHeader {
		return
	}
	r.status = status
	r.wroteHeader = true
	r.ResponseWriter.WriteHeader(status)
}

func (r *responseRecorder) Write(b []byte) (int, error) {
	if !r.wroteHeader {
		r.WriteHeader(http.StatusOK)
	}
	n, err := r.ResponseWriter.Write(b)
	r.bytes += int64(n)
	return n, err
}

// Unwrap atende quem chega via http.NewResponseController; os métodos abaixo
// atendem quem ainda faz type assertion direto.
func (r *responseRecorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

func (r *responseRecorder) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (r *responseRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	h, ok := r.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, errors.New("responseRecorder: underlying writer is not an http.Hijacker")
	}
	return h.Hijack()
}

func (r *responseRecorder) ReadFrom(src io.Reader) (int64, error) {
	if !r.wroteHeader {
		r.WriteHeader(http.StatusOK)
	}
	if rf, ok := r.ResponseWriter.(io.ReaderFrom); ok {
		n, err := rf.ReadFrom(src)
		r.bytes += n
		return n, err
	}
	n, err := io.Copy(r.ResponseWriter, src)
	r.bytes += n
	return n, err
}

// instrument grava as métricas RED e o access log de uma rota.
//
// O label vem do padrão de rota, nunca de r.URL.Path: path cru vira
// cardinalidade infinita no instante em que aparecer ID na URL.
func (s *server) instrument(route string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := newResponseRecorder(w)
		s.metrics.inFlight.Inc()

		// Em defer para que uma requisição que entrou em panic ainda seja
		// medida. Fora do defer, o endpoint que mais quebra some do gráfico.
		defer func() {
			s.metrics.inFlight.Dec()
			s.record(r, rec, route, time.Since(start))
		}()

		next.ServeHTTP(rec, r)
	})
}

func (s *server) record(r *http.Request, rec *responseRecorder, route string, elapsed time.Duration) {
	status := strconv.Itoa(rec.status)

	// O trace ID como exemplar é o que transforma um pico no gráfico de p95
	// em um clique até o trace lento.
	var exemplar prometheus.Labels
	if sc := trace.SpanContextFromContext(r.Context()); sc.IsSampled() {
		exemplar = prometheus.Labels{"trace_id": sc.TraceID().String()}
	}

	counter := s.metrics.requests.WithLabelValues(r.Method, route, status)
	if adder, ok := counter.(prometheus.ExemplarAdder); ok && exemplar != nil {
		adder.AddWithExemplar(1, exemplar)
	} else {
		counter.Inc()
	}

	observer := s.metrics.duration.WithLabelValues(r.Method, route, status)
	if obs, ok := observer.(prometheus.ExemplarObserver); ok && exemplar != nil {
		obs.ObserveWithExemplar(elapsed.Seconds(), exemplar)
	} else {
		observer.Observe(elapsed.Seconds())
	}

	s.metrics.respSize.WithLabelValues(route).Observe(float64(rec.bytes))

	level := slog.LevelInfo
	if rec.status >= http.StatusInternalServerError {
		level = slog.LevelError
	}
	s.logger.LogAttrs(r.Context(), level, "http request",
		slog.String("route", route),
		slog.String("method", r.Method),
		slog.Int("status", rec.status),
		slog.Float64("duration_ms", float64(elapsed.Nanoseconds())/1e6),
		slog.Int64("bytes", rec.bytes),
		slog.String("user_agent", r.UserAgent()),
	)
}

// recoverPanic transforma um panic em 500 logado com stack e marca o span como
// erro. Roda por dentro do instrument, senão o 500 não entra nas métricas.
func (s *server) recoverPanic(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			rvr := recover()
			if rvr == nil {
				return
			}
			// ErrAbortHandler é a forma documentada de derrubar a conexão de
			// propósito; precisa continuar subindo.
			if err, ok := rvr.(error); ok && errors.Is(err, http.ErrAbortHandler) {
				panic(rvr)
			}

			err := fmt.Errorf("panic: %v", rvr)
			span := trace.SpanFromContext(r.Context())
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())

			s.logger.ErrorContext(r.Context(), "recovered from panic",
				slog.String("error", err.Error()),
				slog.String("path", r.URL.Path),
				slog.String("stack", string(debug.Stack())),
			)
			writeJSON(w, http.StatusInternalServerError, errorBody{Error: "internal server error"})
		}()
		next.ServeHTTP(w, r)
	})
}

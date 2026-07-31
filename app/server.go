package main

import (
	"log/slog"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel/trace"
)

type server struct {
	cfg     Config
	logger  *slog.Logger
	metrics *metrics
	tracer  trace.Tracer
	ready   atomic.Bool
}

func newServer(cfg Config, logger *slog.Logger, m *metrics, tracer trace.Tracer) *server {
	s := &server{cfg: cfg, logger: logger, metrics: m, tracer: tracer}
	s.ready.Store(true)
	return s
}

// wrap monta a cadeia de uma rota: métricas por fora, recover por dentro.
// Invertido, o panic viraria 500 depois de o instrument já ter medido, e a
// requisição não apareceria em métrica nenhuma.
func (s *server) wrap(route string, h http.HandlerFunc) http.Handler {
	return s.instrument(route, s.recoverPanic(h))
}

func (s *server) routes() http.Handler {
	mux := http.NewServeMux()

	// "GET /{$}" casa exatamente a raiz. Um "/" sozinho é catch-all e
	// responderia 200 para qualquer path desconhecido.
	mux.Handle("GET /{$}", s.wrap("/", s.handleRoot))
	mux.Handle("GET /work", s.wrap("/work", s.handleWork))
	mux.Handle("GET /healthz", s.wrap("/healthz", s.handleHealth))
	mux.Handle("GET /readyz", s.wrap("/readyz", s.handleReady))
	mux.Handle("/", s.wrap("unmatched", s.handleNotFound))

	// Fora do instrument: contar o próprio scrape infla a taxa de requisição
	// do serviço que ele mede.
	mux.Handle("GET /metrics", promhttp.HandlerFor(s.metrics.registry, promhttp.HandlerOpts{
		Registry: s.metrics.registry,
		// Sem OpenMetrics os exemplars não saem no /metrics.
		EnableOpenMetrics: true,
	}))

	// otelhttp por fora de tudo: o span precisa existir antes de o instrument
	// procurar o trace ID do exemplar.
	return otelhttp.NewHandler(mux, "http.server",
		otelhttp.WithFilter(func(r *http.Request) bool {
			switch r.URL.Path {
			case "/metrics", "/healthz", "/readyz":
				return false
			default:
				return true
			}
		}),
		otelhttp.WithSpanNameFormatter(func(_ string, r *http.Request) string {
			return r.Method + " " + r.URL.Path
		}),
	)
}

// httpServer preenche os timeouts que o http.ListenAndServe deixa zerados —
// zero ali significa "sem timeout", que é como um servidor Go acaba
// vulnerável a Slowloris.
func (s *server) httpServer(handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              ":" + s.cfg.Port,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    1 << 20,
		ErrorLog:          slog.NewLogLogger(s.logger.Handler(), slog.LevelError),
	}
}

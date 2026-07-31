package main

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
)

// metrics usa registry próprio em vez do global do client_golang, o que mantém
// os testes isolados entre si.
type metrics struct {
	registry *prometheus.Registry

	requests *prometheus.CounterVec
	duration *prometheus.HistogramVec
	respSize *prometheus.HistogramVec
	inFlight prometheus.Gauge
}

func newMetrics(cfg Config) *metrics {
	reg := prometheus.NewRegistry()

	// Runtime do Go e do processo: goroutine leak, crescimento de heap e GC
	// saem de graça daqui.
	reg.MustRegister(
		collectors.NewGoCollector(
			collectors.WithGoCollectorRuntimeMetrics(collectors.MetricsAll),
		),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)

	m := &metrics{
		registry: reg,

		requests: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "http_requests_total",
				Help: "Total number of HTTP requests handled, by route, method and status.",
			},
			[]string{"method", "route", "status"},
		),

		// Buckets clássicos funcionam em qualquer Prometheus; a configuração
		// de native histogram é usada só onde o recurso estiver habilitado.
		duration: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:                            "http_request_duration_seconds",
				Help:                            "HTTP request latency in seconds, by route, method and status.",
				Buckets:                         []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5},
				NativeHistogramBucketFactor:     1.1,
				NativeHistogramMaxBucketNumber:  100,
				NativeHistogramMinResetDuration: time.Hour,
			},
			[]string{"method", "route", "status"},
		),

		respSize: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "http_response_size_bytes",
				Help:    "HTTP response body size in bytes, by route.",
				Buckets: prometheus.ExponentialBuckets(64, 4, 8),
			},
			[]string{"route"},
		),

		inFlight: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "http_requests_in_flight",
			Help: "Number of HTTP requests currently being served.",
		}),
	}

	buildInfo := prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "app_build_info",
			Help: "Build metadata of the running service; always 1.",
		},
		[]string{"service", "version", "env"},
	)
	buildInfo.WithLabelValues(cfg.ServiceName, cfg.ServiceVersion, cfg.Environment).Set(1)

	reg.MustRegister(m.requests, m.duration, m.respSize, m.inFlight, buildInfo)
	return m
}

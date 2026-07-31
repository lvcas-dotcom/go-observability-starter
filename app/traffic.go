package main

import (
	"context"
	"io"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

const (
	minInterval = 200 * time.Millisecond
	maxInterval = 800 * time.Millisecond
)

// trafficGenerator chama os próprios endpoints do serviço em intervalo
// aleatório, para que os painéis já tenham dados quando a stack sobe.
//
// As chamadas passam pelo transport do otelhttp, então cada uma gera um span
// de cliente que é pai do span de servidor. É isso que faz o service graph do
// Grafana desenhar uma aresta em vez de um nó solto.
type trafficGenerator struct {
	client   *http.Client
	baseURL  string
	logger   *slog.Logger
	weighted []string
}

func newTrafficGenerator(baseURL string, logger *slog.Logger) *trafficGenerator {
	return &trafficGenerator{
		client: &http.Client{
			Timeout:   5 * time.Second,
			Transport: otelhttp.NewTransport(http.DefaultTransport),
		},
		baseURL: baseURL,
		logger:  logger,
		// /work repetido para pesar mais o endpoint interessante; /nope gera
		// os 404 do painel.
		weighted: []string{"/", "/work", "/work", "/work", "/healthz", "/nope"},
	}
}

// Run bloqueia até o contexto ser cancelado. Toda espera é um select em
// ctx.Done, então o shutdown é imediato.
func (g *trafficGenerator) Run(ctx context.Context) {
	g.logger.InfoContext(ctx, "demo traffic generator started", slog.String("target", g.baseURL))
	defer g.logger.InfoContext(ctx, "demo traffic generator stopped")

	timer := time.NewTimer(nextInterval())
	defer timer.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			g.fire(ctx)
			timer.Reset(nextInterval())
		}
	}
}

func (g *trafficGenerator) fire(ctx context.Context) {
	path := g.weighted[rand.IntN(len(g.weighted))]

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, g.baseURL+path, nil)
	if err != nil {
		g.logger.WarnContext(ctx, "demo traffic: build request failed", slog.String("error", err.Error()))
		return
	}

	resp, err := g.client.Do(req)
	if err != nil {
		// Falhar durante o shutdown é esperado, não merece log de erro.
		if ctx.Err() == nil {
			g.logger.WarnContext(ctx, "demo traffic: request failed", slog.String("error", err.Error()))
		}
		return
	}
	// Drenar antes de fechar devolve a conexão para o pool de keep-alive em
	// vez de derrubá-la a cada chamada.
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	_ = resp.Body.Close()
}

func nextInterval() time.Duration {
	return minInterval + time.Duration(rand.N(maxInterval-minInterval))
}

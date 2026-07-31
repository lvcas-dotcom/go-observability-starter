package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"
)

// Prazo próprio para o flush dos spans, separado do prazo de shutdown do HTTP.
const traceFlushTimeout = 5 * time.Second

// buildVersion é injetado no link com -ldflags "-X main.buildVersion=...".
var buildVersion = "dev"

func main() {
	healthcheck := flag.Bool("healthcheck", false,
		"probe the local health endpoint and exit; used by the container HEALTHCHECK")
	flag.Parse()

	if *healthcheck {
		if err := probeHealth(); err != nil {
			fmt.Fprintln(os.Stderr, "healthcheck failed:", err)
			os.Exit(1)
		}
		return
	}

	if err := run(); err != nil {
		slog.Error("fatal", slog.String("error", err.Error()))
		os.Exit(1)
	}
}

// probeHealth faz o binário consultar a si mesmo. A imagem final é distroless
// e não tem curl nem wget para o HEALTHCHECK do Docker chamar.
func probeHealth() error {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	url := "http://127.0.0.1:" + env("PORT", "8080") + "/healthz"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status %s", resp.Status)
	}
	return nil
}

// run concentra o ciclo de vida para que os defers rodem. Chamar os.Exit
// direto do main pularia todos eles e perderia o último lote de spans —
// justamente os traces que interessam depois de um crash.
func run() error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}

	logger := newLogger(os.Stdout, cfg)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	tracer, shutdownTracing, err := setupTracing(ctx, cfg)
	if err != nil {
		return err
	}

	srv := newServer(cfg, logger, newMetrics(cfg), tracer)
	httpSrv := srv.httpServer(srv.routes())

	var wg sync.WaitGroup
	if cfg.DemoTraffic {
		gen := newTrafficGenerator("http://localhost:"+cfg.Port, logger)
		wg.Add(1)
		go func() {
			defer wg.Done()
			gen.Run(ctx)
		}()
	}

	serverErr := make(chan error, 1)
	go func() {
		logger.InfoContext(ctx, "server listening",
			slog.String("addr", httpSrv.Addr),
			slog.Bool("demo_traffic", cfg.DemoTraffic),
			slog.String("otlp_endpoint", cfg.OTLPEndpoint),
		)
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErr <- err
		}
	}()

	select {
	case err := <-serverErr:
		return err
	case <-ctx.Done():
		logger.Info("shutdown signal received")
	}

	// Readiness cai primeiro: o balanceador para de mandar requisição nova
	// enquanto as que já estão em voo terminam abaixo.
	srv.ready.Store(false)

	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()

	var shutdownErr error
	if err := httpSrv.Shutdown(shutdownCtx); err != nil {
		shutdownErr = err
		logger.Error("graceful shutdown failed, forcing close", slog.String("error", err.Error()))
		_ = httpSrv.Close()
	}

	wg.Wait()

	// Contexto novo, não o shutdownCtx: se o Shutdown acima tiver consumido
	// todo o prazo, o flush receberia um contexto já expirado e os últimos
	// spans se perderiam em silêncio.
	flushCtx, cancelFlush := context.WithTimeout(context.Background(), traceFlushTimeout)
	defer cancelFlush()

	if err := shutdownTracing(flushCtx); err != nil {
		logger.Error("tracer shutdown failed", slog.String("error", err.Error()))
	}

	logger.Info("shutdown complete")
	return shutdownErr
}

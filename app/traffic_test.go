package main

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// O gerador já dormiu fora do select, e aí só percebia o cancelamento depois
// da espera inteira. O shutdown tem que ser imediato.
func TestTrafficGeneratorStopsOnContextCancel(t *testing.T) {
	t.Parallel()

	var hits atomic.Int64
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	gen := newTrafficGenerator(backend.URL, newLogger(io.Discard, Config{LogLevel: slog.LevelError}))

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		gen.Run(ctx)
	}()

	// Tempo de disparar ao menos uma vez antes de cancelar.
	time.Sleep(900 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("generator did not stop within 2s of cancellation")
	}

	if hits.Load() == 0 {
		t.Error("generator produced no traffic at all")
	}
}

func TestTrafficGeneratorCoversTheInterestingRoutes(t *testing.T) {
	t.Parallel()

	gen := newTrafficGenerator("http://example.invalid", newLogger(io.Discard, Config{LogLevel: slog.LevelError}))

	seen := map[string]bool{}
	for _, p := range gen.weighted {
		seen[p] = true
	}
	// /work é o que alimenta latência, erro e trace nos painéis.
	for _, want := range []string{"/", "/work"} {
		if !seen[want] {
			t.Errorf("generator never calls %q", want)
		}
	}
}

func TestNextIntervalStaysWithinBounds(t *testing.T) {
	t.Parallel()

	for range 200 {
		d := nextInterval()
		if d < 200*time.Millisecond || d >= 800*time.Millisecond {
			t.Fatalf("interval %v is outside the expected [200ms, 800ms) range", d)
		}
	}
}

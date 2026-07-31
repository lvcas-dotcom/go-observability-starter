package main

import (
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config reúne tudo que o serviço permite ajustar. Só variável de ambiente,
// para o container não precisar de arquivo de config montado.
type Config struct {
	Port            string
	ServiceName     string
	ServiceVersion  string
	Environment     string
	OTLPEndpoint    string
	TraceSampleRate float64
	DemoTraffic     bool
	LogLevel        slog.Level
	ShutdownTimeout time.Duration
}

func loadConfig() (Config, error) {
	cfg := Config{
		Port:            env("PORT", "8080"),
		ServiceName:     env("SERVICE_NAME", "go-observability-starter"),
		ServiceVersion:  env("SERVICE_VERSION", buildVersion),
		Environment:     env("ENVIRONMENT", "local"),
		OTLPEndpoint:    env("OTEL_EXPORTER_OTLP_ENDPOINT", "tempo:4318"),
		TraceSampleRate: 1.0,
		DemoTraffic:     true,
		LogLevel:        slog.LevelInfo,
		ShutdownTimeout: 10 * time.Second,
	}

	var err error
	if cfg.TraceSampleRate, err = envFloat("TRACE_SAMPLE_RATE", 1.0); err != nil {
		return cfg, err
	}
	if cfg.DemoTraffic, err = envBool("DEMO_TRAFFIC", true); err != nil {
		return cfg, err
	}
	if cfg.LogLevel, err = envLevel("LOG_LEVEL", slog.LevelInfo); err != nil {
		return cfg, err
	}
	if cfg.ShutdownTimeout, err = envDuration("SHUTDOWN_TIMEOUT", 10*time.Second); err != nil {
		return cfg, err
	}

	if cfg.TraceSampleRate < 0 || cfg.TraceSampleRate > 1 {
		return cfg, fmt.Errorf("TRACE_SAMPLE_RATE must be between 0 and 1, got %v", cfg.TraceSampleRate)
	}
	return cfg, nil
}

func env(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return fallback
}

func envBool(key string, fallback bool) (bool, error) {
	raw, ok := os.LookupEnv(key)
	if !ok || raw == "" {
		return fallback, nil
	}
	v, err := strconv.ParseBool(raw)
	if err != nil {
		return fallback, fmt.Errorf("invalid %s=%q: want a boolean", key, raw)
	}
	return v, nil
}

func envFloat(key string, fallback float64) (float64, error) {
	raw, ok := os.LookupEnv(key)
	if !ok || raw == "" {
		return fallback, nil
	}
	v, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return fallback, fmt.Errorf("invalid %s=%q: want a number", key, raw)
	}
	return v, nil
}

func envDuration(key string, fallback time.Duration) (time.Duration, error) {
	raw, ok := os.LookupEnv(key)
	if !ok || raw == "" {
		return fallback, nil
	}
	v, err := time.ParseDuration(raw)
	if err != nil {
		return fallback, fmt.Errorf("invalid %s=%q: want a duration like 10s", key, raw)
	}
	return v, nil
}

func envLevel(key string, fallback slog.Level) (slog.Level, error) {
	raw, ok := os.LookupEnv(key)
	if !ok || raw == "" {
		return fallback, nil
	}
	var level slog.Level
	if err := level.UnmarshalText([]byte(strings.ToUpper(raw))); err != nil {
		return fallback, fmt.Errorf("invalid %s=%q: want debug, info, warn or error", key, raw)
	}
	return level, nil
}

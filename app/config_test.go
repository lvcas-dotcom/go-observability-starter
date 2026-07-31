package main

import (
	"log/slog"
	"testing"
	"time"
)

func TestLoadConfigDefaults(t *testing.T) {
	cfg, err := loadConfig()
	if err != nil {
		t.Fatalf("loadConfig with a clean env: %v", err)
	}

	if cfg.Port != "8080" {
		t.Errorf("Port = %q, want 8080", cfg.Port)
	}
	// Tráfego ligado por padrão é a premissa do starter: painel populado no
	// primeiro boot, sem curl na mão.
	if !cfg.DemoTraffic {
		t.Error("DemoTraffic must default to true")
	}
	if cfg.ShutdownTimeout != 10*time.Second {
		t.Errorf("ShutdownTimeout = %v, want 10s", cfg.ShutdownTimeout)
	}
	if cfg.LogLevel != slog.LevelInfo {
		t.Errorf("LogLevel = %v, want info", cfg.LogLevel)
	}
}

func TestLoadConfigFromEnv(t *testing.T) {
	t.Setenv("PORT", "9999")
	t.Setenv("DEMO_TRAFFIC", "false")
	t.Setenv("LOG_LEVEL", "debug")
	t.Setenv("TRACE_SAMPLE_RATE", "0.25")
	t.Setenv("SHUTDOWN_TIMEOUT", "30s")
	t.Setenv("SERVICE_VERSION", "1.2.3")

	cfg, err := loadConfig()
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}

	if cfg.Port != "9999" {
		t.Errorf("Port = %q, want 9999", cfg.Port)
	}
	if cfg.DemoTraffic {
		t.Error("DEMO_TRAFFIC=false was not honoured")
	}
	if cfg.LogLevel != slog.LevelDebug {
		t.Errorf("LogLevel = %v, want debug", cfg.LogLevel)
	}
	if cfg.TraceSampleRate != 0.25 {
		t.Errorf("TraceSampleRate = %v, want 0.25", cfg.TraceSampleRate)
	}
	if cfg.ShutdownTimeout != 30*time.Second {
		t.Errorf("ShutdownTimeout = %v, want 30s", cfg.ShutdownTimeout)
	}
	if cfg.ServiceVersion != "1.2.3" {
		t.Errorf("ServiceVersion = %q, want 1.2.3", cfg.ServiceVersion)
	}
}

// Erro de digitação em variável de ambiente tem que quebrar o boot, não cair
// calado num padrão que ninguém pediu.
func TestLoadConfigRejectsBadValues(t *testing.T) {
	tests := []struct {
		name  string
		key   string
		value string
	}{
		{name: "non-boolean demo traffic", key: "DEMO_TRAFFIC", value: "yes-please"},
		{name: "non-numeric sample rate", key: "TRACE_SAMPLE_RATE", value: "half"},
		{name: "sample rate above 1", key: "TRACE_SAMPLE_RATE", value: "1.5"},
		{name: "negative sample rate", key: "TRACE_SAMPLE_RATE", value: "-0.1"},
		{name: "unparseable duration", key: "SHUTDOWN_TIMEOUT", value: "soon"},
		{name: "unknown log level", key: "LOG_LEVEL", value: "chatty"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(tc.key, tc.value)
			if _, err := loadConfig(); err == nil {
				t.Fatalf("loadConfig accepted %s=%q, want an error", tc.key, tc.value)
			}
		})
	}
}

// Vazio conta como não definido: variável declarada em branco no compose cai
// no padrão em vez de quebrar o boot.
func TestEmptyEnvFallsBackToDefault(t *testing.T) {
	t.Setenv("PORT", "")
	t.Setenv("DEMO_TRAFFIC", "")

	cfg, err := loadConfig()
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if cfg.Port != "8080" {
		t.Errorf("Port = %q, want the 8080 default", cfg.Port)
	}
	if !cfg.DemoTraffic {
		t.Error("DemoTraffic = false, want the true default")
	}
}

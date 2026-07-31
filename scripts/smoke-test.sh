#!/usr/bin/env bash
#
# Boots the stack and asserts that each piece is genuinely working — not just
# that the containers are running. This is what CI runs, and it is the reason
# the README can claim "one command" without it quietly rotting.
#
set -euo pipefail

COMPOSE=${COMPOSE:-docker compose}
APP=http://localhost:${APP_PORT:-8080}
PROM=http://localhost:${PROMETHEUS_PORT:-9090}
GRAFANA=http://localhost:${GRAFANA_PORT:-3000}
LOKI=http://localhost:${LOKI_PORT:-3100}
TEMPO=http://localhost:${TEMPO_PORT:-3200}
ALERTMANAGER=http://localhost:${ALERTMANAGER_PORT:-9093}

pass() { printf "  \033[32mPASS\033[0m  %s\n" "$1"; }
fail() { printf "  \033[31mFAIL\033[0m  %s\n" "$1"; FAILURES=$((FAILURES + 1)); }
info() { printf "\n\033[1m%s\033[0m\n" "$1"; }

FAILURES=0

# retry <seconds> <description> <command...>
retry() {
  local deadline=$(( SECONDS + $1 )); shift
  local what="$1"; shift
  while (( SECONDS < deadline )); do
    if "$@" >/dev/null 2>&1; then
      pass "$what"
      return 0
    fi
    sleep 2
  done
  fail "$what (timed out)"
  return 1
}

# Asserts that a URL returns a body matching a pattern.
body_matches() {
  local url="$1" pattern="$2"
  curl -fsS --max-time 5 "$url" 2>/dev/null | grep -qE "$pattern"
}

# Asserts that a URL returns an exact HTTP status.
status_is() {
  local url="$1" want="$2"
  [ "$(curl -s -o /dev/null -w '%{http_code}' --max-time 5 "$url")" = "$want" ]
}

info "Starting the stack"
$COMPOSE up -d --build

info "Waiting for services to come up"
retry 120 "app answers /healthz"            body_matches "$APP/healthz" '"status":"ok"' || true
retry 120 "app answers /readyz"             body_matches "$APP/readyz" '"status":"ready"' || true
retry 120 "Prometheus is ready"             curl -fsS --max-time 5 "$PROM/-/ready" || true
retry 120 "Loki is ready"                   curl -fsS --max-time 5 "$LOKI/ready" || true
retry 120 "Tempo is ready"                  curl -fsS --max-time 5 "$TEMPO/ready" || true
retry 120 "Alertmanager is healthy"         curl -fsS --max-time 5 "$ALERTMANAGER/-/healthy" || true
retry 180 "Grafana is healthy"              body_matches "$GRAFANA/api/health" '"database": *"ok"' || true

info "Application endpoints"
retry 30 "GET / returns JSON"               body_matches "$APP/" '"service"' || true
retry 30 "unknown paths return a real 404"  status_is "$APP/definitely-not-a-route" 404 || true
retry 30 "/metrics exposes app metrics"     body_matches "$APP/metrics" '^http_requests_total' || true
retry 30 "/metrics exposes Go runtime"      body_matches "$APP/metrics" '^go_goroutines' || true

info "Metrics pipeline"
retry 120 "Prometheus scrapes the app"      body_matches "$PROM/api/v1/query?query=up%7Bjob%3D%22go-app%22%7D" '"value":\[[0-9.]+,"1"\]' || true
retry 120 "request counter has data"        body_matches "$PROM/api/v1/query?query=sum(http_requests_total)" '"result":\[\{' || true
retry 120 "recording rules evaluated"       body_matches "$PROM/api/v1/query?query=job%3Ahttp_requests%3Arate5m" '"result":\[\{' || true
# Ligar native-histograms faz o Prometheus descartar os buckets clássicos se
# always_scrape_classic_histograms não estiver setado, e aí todo painel de
# latência fica vazio sem nenhum erro aparecer. Checa a query real do dashboard.
retry 120 "latency buckets are queryable"   body_matches "$PROM/api/v1/query?query=count(http_request_duration_seconds_bucket)" '"result":\[\{' || true
retry 120 "alert rules are loaded"          body_matches "$PROM/api/v1/rules" 'HighErrorRate' || true

info "Logs pipeline"
retry 180 "Loki has logs from the app"      body_matches "$LOKI/loki/api/v1/label/container/values" 'go-app' || true

info "Traces pipeline"
retry 60  "Tempo answers its API"           body_matches "$TEMPO/api/echo" 'echo' || true
# Asserting on the span metrics proves the entire trace path in one check:
# the app exported spans, Tempo ingested them, its generator derived metrics,
# and the remote write into Prometheus landed.
retry 240 "spans reached Tempo end to end"  body_matches "$PROM/api/v1/query?query=sum(traces_spanmetrics_calls_total)" '"result":\[\{' || true

info "Grafana provisioning"
retry 60 "Prometheus datasource exists"     body_matches "$GRAFANA/api/datasources/uid/prometheus" '"type": *"prometheus"' || true
retry 60 "Loki datasource exists"           body_matches "$GRAFANA/api/datasources/uid/loki" '"type": *"loki"' || true
retry 60 "Tempo datasource exists"          body_matches "$GRAFANA/api/datasources/uid/tempo" '"type": *"tempo"' || true
# Matched on the uid rather than the title: the dashboards are written in
# pt-BR and renaming a panel must not break CI.
retry 60 "overview dashboard provisioned"   body_matches "$GRAFANA/api/dashboards/uid/go-app-overview" '"uid": *"go-app-overview"' || true
retry 60 "runtime dashboard provisioned"    body_matches "$GRAFANA/api/dashboards/uid/go-runtime" '"uid": *"go-runtime"' || true

info "Result"
if [ "$FAILURES" -gt 0 ]; then
  printf "\033[31m%d check(s) failed\033[0m\n\n" "$FAILURES"
  echo "Recent container logs:"
  $COMPOSE logs --tail 40
  exit 1
fi

printf "\033[32mAll checks passed.\033[0m\n"

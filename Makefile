.DEFAULT_GOAL := help
# ?= para dar pra sobrescrever pelo ambiente, ex.: se você precisa de sudo
#   COMPOSE="sudo docker compose" make up
COMPOSE ?= docker compose
APP_DIR := app

.PHONY: help
help: ## Show this help
	@grep -hE '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) \
		| awk 'BEGIN{FS=":.*?## "}{printf "  \033[36m%-16s\033[0m %s\n", $$1, $$2}'

.PHONY: up
up: ## Build and start the whole stack
	$(COMPOSE) up --build -d
	@echo ""
	@echo "  Grafana        http://localhost:3000  (dashboard opens by default)"
	@echo "  App            http://localhost:8080"
	@echo "  Prometheus     http://localhost:9090"
	@echo "  Alertmanager   http://localhost:9093"
	@echo ""

.PHONY: down
down: ## Stop the stack, keeping the data volumes
	$(COMPOSE) down

.PHONY: clean
clean: ## Stop the stack and delete all collected data
	$(COMPOSE) down -v --remove-orphans

.PHONY: logs
logs: ## Tail logs from every service
	$(COMPOSE) logs -f

.PHONY: ps
ps: ## Show service status
	$(COMPOSE) ps

.PHONY: restart
restart: ## Rebuild and restart just the app
	$(COMPOSE) up -d --build app

.PHONY: test
test: ## Run the Go test suite with the race detector
	cd $(APP_DIR) && go test -race -count=1 ./...

.PHONY: cover
cover: ## Run tests and open the HTML coverage report
	cd $(APP_DIR) && go test -race -count=1 -coverprofile=coverage.out ./... \
		&& go tool cover -html=coverage.out

.PHONY: lint
lint: ## Run go vet and golangci-lint if it is installed
	cd $(APP_DIR) && go vet ./...
	@command -v golangci-lint >/dev/null 2>&1 \
		&& (cd $(APP_DIR) && golangci-lint run) \
		|| echo "golangci-lint not installed, skipping (https://golangci-lint.run)"

.PHONY: tidy
tidy: ## Tidy and verify Go modules
	cd $(APP_DIR) && go mod tidy && go mod verify

.PHONY: validate
validate: ## Validate the compose file and every stack config
	$(COMPOSE) config --quiet && echo "compose OK"
	@command -v promtool >/dev/null 2>&1 \
		&& promtool check config prometheus/prometheus.yml \
		|| echo "promtool not installed, skipping Prometheus checks"
	@command -v amtool >/dev/null 2>&1 \
		&& amtool check-config alertmanager/alertmanager.yml \
		|| echo "amtool not installed, skipping Alertmanager checks"
	@command -v alloy >/dev/null 2>&1 \
		&& alloy validate alloy/config.alloy \
		|| echo "alloy not installed, skipping Alloy checks"

.PHONY: smoke
smoke: ## Boot the stack and assert every component is actually working
	./scripts/smoke-test.sh

.PHONY: load
load: ## Fire a burst of requests to make the dashboards move
	@for i in $$(seq 1 200); do curl -s -o /dev/null http://localhost:8080/work & done; wait; echo "sent 200 requests"

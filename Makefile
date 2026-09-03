# FundKit developer tasks
# Engineered by Dhanush C N (github.com/dhanush-cn)

SERVICES := api-gateway order-service portfolio-service notification-service

.PHONY: help up down logs build test test-integration test-frontend ci vet fmt tidy metrics monitoring

help:
	@echo "up     - start the full stack with docker compose"
	@echo "down   - stop the stack"
	@echo "logs   - follow logs for all services"
	@echo "build  - compile every Go service"
	@echo "test   - run every Go unit test suite"
	@echo "test-integration - run the Postgres/Redis/Kafka integration suites"
	@echo "test-frontend    - type check, lint and test the dashboard"
	@echo "ci     - the local approximation of the CI pipeline"
	@echo "vet    - run go vet across all modules"
	@echo "fmt    - gofmt every module"
	@echo "tidy   - go mod tidy every module"
	@echo "metrics    - curl the /metrics endpoint of every service"
	@echo "monitoring - print the Prometheus and Grafana URLs"

up:
	docker compose up -d --build

down:
	docker compose down

logs:
	docker compose logs -f

build:
	@for svc in $(SERVICES); do echo "==> building $$svc"; (cd $$svc && go build ./...) || exit 1; done

test:
	@for svc in $(SERVICES); do echo "==> testing $$svc"; (cd $$svc && go test ./...) || exit 1; done

vet:
	@for svc in $(SERVICES); do echo "==> vetting $$svc"; (cd $$svc && go vet ./...) || exit 1; done

fmt:
	@for svc in $(SERVICES); do (cd $$svc && gofmt -l -w .); done

tidy:
	@for svc in $(SERVICES); do echo "==> tidying $$svc"; (cd $$svc && go mod tidy) || exit 1; done

test-integration:
	@echo "==> starting backing services"
	docker compose up -d postgres redis kafka
	@echo "==> running integration suites (requires the stack to be healthy)"
	@FUNDKIT_TEST_DB_URL="postgres://fundkit:password@localhost:5433/fundkit_db?sslmode=disable" \
	 FUNDKIT_TEST_REDIS_URL="localhost:6380" \
	 FUNDKIT_TEST_KAFKA_BROKERS="localhost:29092" \
	 sh -c 'for svc in api-gateway order-service; do echo "==> $$svc"; (cd $$svc && go test -tags=integration -count=1 ./...) || exit 1; done'

test-frontend:
	cd frontend && npm ci && npm run typecheck && npm run lint && npm test

# Each service publishes its admin listener on a distinct host port purely so
# they can be curled side by side during development. Inside the compose network
# Prometheus scrapes :9100 on every one of them.
metrics:
	@for entry in api-gateway:9101 order-service:9102 portfolio-service:9103 notification-service:9104; do \
	  svc=$${entry%%:*}; port=$${entry##*:}; \
	  printf "\n==> %s (localhost:%s/metrics)\n" "$$svc" "$$port"; \
	  curl -sf "http://localhost:$$port/metrics" | grep -E "^(http_requests_total|http_request_duration_seconds_count|grpc_server_requests_total|kafka_events_published_total|kafka_events_consumed_total|kafka_consumer_lag)[{ ]" || echo "    no FundKit series yet - send some traffic first"; \
	done

monitoring:
	@echo "Prometheus targets : http://localhost:9090/targets"
	@echo "Alert rules        : http://localhost:9090/alerts"
	@echo "Grafana dashboard  : http://localhost:3000/d/fundkit-overview"

ci: fmt vet build test test-frontend
	@echo "==> local approximation of the CI pipeline is green"

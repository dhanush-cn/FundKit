# FundKit developer tasks
# Engineered by Dhanush C N (github.com/dhanush-cn)

SERVICES := api-gateway order-service portfolio-service notification-service

# ---------------------------------------------------------------- migrations
#
# Schema changes are versioned .sql files applied by golang-migrate, never by
# AutoMigrate at boot. order-service asserts the version on startup and refuses
# to serve against a schema older than the one its code was written for.
MIGRATIONS_DIR   := order-service/migrations
MIGRATIONS_TABLE := order_service_schema_migrations

# Override for any environment: make migrate-up DB_URL=postgres://...
# Defaults to the compose stack as seen from the host (published on 5433).
DB_URL ?= postgres://fundkit:password@localhost:5433/fundkit_db?sslmode=disable

# The bookkeeping table is namespaced because api-gateway shares this database.
# Two services tracking separate histories in one default schema_migrations
# table would each read the other's version and conclude they were behind.
MIGRATE_DB_URL := $(DB_URL)&x-migrations-table=$(MIGRATIONS_TABLE)

# Use a locally installed migrate binary when there is one, otherwise fall back
# to the official image so a fresh clone needs nothing but Docker. On Docker
# Desktop --network=host does not reach the host's ports; pass a DB_URL with
# host.docker.internal there, or run these against the compose network.
ifeq ($(shell command -v migrate 2>/dev/null),)
  MIGRATE := docker run --rm --network=host -v "$(CURDIR)/$(MIGRATIONS_DIR)":/migrations migrate/migrate:v4.17.1 -path=/migrations
else
  MIGRATE := migrate -path=$(MIGRATIONS_DIR)
endif

.PHONY: help up down logs build test test-integration test-frontend load-test load-test-smoke ci vet fmt tidy metrics monitoring migrate-up migrate-down migrate-version migrate-force migrate-create migrate-configmap

help:
	@echo "up     - start the full stack with docker compose"
	@echo "down   - stop the stack"
	@echo "logs   - follow logs for all services"
	@echo "build  - compile every Go service"
	@echo "test   - run every Go unit test suite"
	@echo "test-integration - run the Postgres/Redis/Kafka integration suites"
	@echo "test-frontend    - type check, lint and test the dashboard"
	@echo "load-test        - k6 load and idempotency test against POST /orders"
	@echo "load-test-smoke  - the same assertions at 20 VUs for one minute"
	@echo "ci     - the local approximation of the CI pipeline"
	@echo "vet    - run go vet across all modules"
	@echo "fmt    - gofmt every module"
	@echo "tidy   - go mod tidy every module"
	@echo "metrics    - curl the /metrics endpoint of every service"
	@echo "monitoring - print the Prometheus and Grafana URLs"
	@echo ""
	@echo "migrate-up      - apply all pending schema migrations"
	@echo "migrate-down    - roll back the most recent migration"
	@echo "migrate-version - print the current schema version"
	@echo "migrate-force   - clear a dirty state: make migrate-force VERSION=1"
	@echo "migrate-create  - scaffold a new pair: make migrate-create NAME=add_x"
	@echo "migrate-configmap - render the k8s ConfigMap of migrations to stdout"

up:
	docker compose up -d --build

# ---------------------------------------------------------------- migrations
#
# `up` applies every migration that has not run yet, in order, recording each
# in $(MIGRATIONS_TABLE). Running it twice is a no-op — that idempotence is the
# whole reason a migration tool exists rather than a folder of scripts someone
# remembers to run.
migrate-up:
	$(MIGRATE) -database "$(MIGRATE_DB_URL)" up

# One step back, not all the way. `down` with no argument would drop every
# table without asking, so the step count is explicit.
migrate-down:
	$(MIGRATE) -database "$(MIGRATE_DB_URL)" down 1

migrate-version:
	$(MIGRATE) -database "$(MIGRATE_DB_URL)" version

# Clears the dirty flag left by a migration that failed part-way. This does NOT
# fix the database — it only tells golang-migrate which version to believe. Look
# at what actually applied, repair it by hand, and only then force.
migrate-force:
	@test -n "$(VERSION)" || { echo "usage: make migrate-force VERSION=<n>"; exit 1; }
	$(MIGRATE) -database "$(MIGRATE_DB_URL)" force $(VERSION)

# Scaffolds the .up.sql/.down.sql pair with the next sequence number, so the
# numbering never has to be worked out by hand.
migrate-create:
	@test -n "$(NAME)" || { echo "usage: make migrate-create NAME=add_something"; exit 1; }
	migrate create -ext sql -dir $(MIGRATIONS_DIR) -seq $(NAME)

# Renders the ConfigMap the k8s init container mounts, straight from the .sql
# files. Generated rather than checked in so the cluster cannot drift from the
# repository:
#
#   make migrate-configmap | kubectl apply -f -
migrate-configmap:
	@kubectl create configmap fundkit-order-migrations \
	  --from-file=$(MIGRATIONS_DIR) \
	  --dry-run=client -o yaml

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
	@echo "==> applying migrations"
	$(MAKE) migrate-up
	@FUNDKIT_TEST_DB_URL="postgres://fundkit:password@localhost:5433/fundkit_db?sslmode=disable" \
	 FUNDKIT_TEST_REDIS_URL="localhost:6380" \
	 FUNDKIT_TEST_KAFKA_BROKERS="localhost:29092" \
	 sh -c 'for svc in api-gateway order-service; do echo "==> $$svc"; (cd $$svc && go test -tags=integration -count=1 ./...) || exit 1; done'

test-frontend:
	cd frontend && npm ci && npm run typecheck && npm run lint && npm test

# The gateway's per-IP limiter defaults to 5 rps, and a load generator is one
# IP. Raise it for the run or you are benchmarking the rate limiter, not the
# order path. See perf/README.md.
load-test:
	@mkdir -p perf/results
	@command -v k6 >/dev/null || { echo "k6 not installed: https://k6.io/docs/get-started/installation/"; exit 1; }
	k6 run perf/load_test.js

load-test-smoke:
	@mkdir -p perf/results
	@command -v k6 >/dev/null || { echo "k6 not installed: https://k6.io/docs/get-started/installation/"; exit 1; }
	TARGET_VUS=20 RAMP_UP=20s HOLD=30s RAMP_DOWN=10s RACE_RATE=2 k6 run perf/load_test.js

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

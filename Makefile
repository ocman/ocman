.PHONY: dev dev-backend dev-frontend dev-prod dev-prod-watch kill-dev build run clean test test-backend test-frontend test-race test-fuzz test-coverage lint lint-backend lint-frontend lint-platform-branching otel-up otel-down otel-logs otel-reset help

# --- OTel dev defaults ----------------------------------------------------
#
# Dev targets export the local LGTM stack as the default OTLP endpoint so
# `make dev` / `dev-prod*` / `dev-backend` automatically ship traces and
# metrics to it (assuming `make otel-up` is running). The variable is set
# via `?=` so an operator can override per-invocation:
#
#   make dev OTEL_EXPORTER_OTLP_ENDPOINT=                       # disable
#   make dev OTEL_EXPORTER_OTLP_ENDPOINT=http://other:4318      # remote
#
# Empty value disables telemetry (telemetry.Init treats unset/empty as
# no-op). Using OTLP/HTTP because it's simpler and the LGTM image
# accepts both.
export OTEL_EXPORTER_OTLP_ENDPOINT ?= http://localhost:4318
export OTEL_SERVICE_NAME ?= ocman-dev

# --- dev-loop plumbing ----------------------------------------------------
#
# The dev targets run multiple long-lived processes (air, vite, watchers) in
# parallel and must shut them ALL down cleanly when the user hits Ctrl+C or
# the make process otherwise dies. Getting this right is fiddly:
#
#   - `trap 'kill 0' EXIT` alone is not enough: Ctrl+C sends SIGINT to the
#     whole process group, which kills the shell running the trap *before*
#     the EXIT trap gets to fire. Orphaned children then get reparented to
#     init and keep holding their ports.
#   - `tee` in a pipeline eats signals that would otherwise propagate.
#   - Sub-shells spawned by make are not always in the same process group as
#     make itself, so `kill 0` can miss grand-children spawned by npm/vite.
#
# Workaround: trap INT, TERM, *and* EXIT; walk the descendant tree with
# `pkill -P $$` (+ a wider ocman/vite sweep via :8228/:8229 port holders as
# a final safety net). This makes Ctrl+C reliably reap every child.

# Macro: `kill-children` is the shell snippet each dev target installs as a
# trap. It first signals every direct child of this shell, then recursively
# walks their descendants, then reclaims the dev ports as a last resort.
# The redirect swallows "no such process" noise when a child has already
# exited normally by the time the trap runs.
# Portable reclaim: BSD xargs (macOS) has no -r flag, but it's already a
# no-op on empty stdin, so we don't need one. GNU xargs accepts -r but
# also no-ops silently on empty input here. `|| true` keeps the trap
# from masking the real exit status when there's nothing to kill.
define kill-children
	pkill -TERM -P $$$$ 2>/dev/null || true; \
	sleep 0.3; \
	pkill -KILL -P $$$$ 2>/dev/null || true; \
	lsof -tiTCP:8228 -sTCP:LISTEN 2>/dev/null | xargs kill -9 2>/dev/null || true; \
	lsof -tiTCP:8229 -sTCP:LISTEN 2>/dev/null | xargs kill -9 2>/dev/null || true
endef

# Run both backend (air) and frontend (vite) with live reload
dev:
	@mkdir -p tmp
	@echo "Starting ocman dev environment..."
	@echo "  Backend (air):    http://localhost:8229"
	@echo "  Frontend (vite):  http://localhost:8228"
	@echo "  Backend log:      tmp/air.log"
	@echo "  Combined log:     tmp/debug.log"
	@echo ""
	@trap '$(kill-children)' INT TERM EXIT; \
		{ $(MAKE) dev-backend & \
		  $(MAKE) dev-frontend & \
		  wait; } 2>&1 | tee tmp/debug.log

# Run with production frontend build + backend live reload (manual frontend rebuild)
dev-prod:
	@mkdir -p tmp
	@echo "Starting ocman PRODUCTION MODE with live reload..."
	@echo "  Backend (air):    http://localhost:8229"
	@echo "  Frontend (vite):  http://localhost:8228 (serves production build)"
	@echo "  Backend log:      tmp/air.log"
	@echo "  Frontend log:     tmp/vite-preview.log"
	@echo ""
	@echo "Note: Frontend changes require manual 'cd frontend && npm run build'"
	@echo ""
	@cd frontend && npm run build
	@trap '$(kill-children)' INT TERM EXIT; \
		{ air 2>&1 | tee tmp/air.log & \
		  cd frontend && npm run preview 2>&1 | tee ../tmp/vite-preview.log & \
		  wait; }

# Run with production frontend build + auto-rebuild on changes + backend live reload
dev-prod-watch:
	@mkdir -p tmp
	@echo "Starting ocman PRODUCTION MODE with AUTO-RELOAD..."
	@echo "  Backend (air):    http://localhost:8229"
	@echo "  Frontend (vite):  http://localhost:8228 (serves production build, auto-rebuilds)"
	@echo "  Backend log:      tmp/air.log"
	@echo "  Frontend log:     tmp/vite-preview.log"
	@echo "  Watch log:        tmp/frontend-watch.log"
	@echo ""
	@trap '$(kill-children)' INT TERM EXIT; \
		{ air 2>&1 | tee tmp/air.log & \
		  cd frontend && npm run preview 2>&1 | tee ../tmp/vite-preview.log & \
		  ./scripts/watch-frontend-prod.sh 2>&1 | tee tmp/frontend-watch.log & \
		  wait; }

dev-backend:
	@mkdir -p tmp
	@air 2>&1 | tee tmp/air.log

dev-frontend:
	cd frontend && npm run dev

# Emergency nuke: kill anything holding the dev ports. Use when a previous
# `make dev*` died badly and left orphans squatting on 8228 / 8229. Safe to
# run even when nothing is listening — xargs is a no-op on empty stdin on
# both BSD and GNU.
kill-dev:
	@echo "Reclaiming dev ports 8228 and 8229..."
	@lsof -tiTCP:8228 -sTCP:LISTEN 2>/dev/null | xargs kill -9 2>/dev/null || true
	@lsof -tiTCP:8229 -sTCP:LISTEN 2>/dev/null | xargs kill -9 2>/dev/null || true
	@pkill -f 'air -c .air.toml' 2>/dev/null || true
	@pkill -f 'vite preview' 2>/dev/null || true
	@pkill -f 'vite dev' 2>/dev/null || true
	@pkill -f 'watch-frontend-prod.sh' 2>/dev/null || true
	@echo "Done. Run 'make dev' / 'make dev-prod-watch' to restart."

# Production build
build: build-frontend build-backend

build-frontend:
	cd frontend && npm ci && npm run build

build-backend:
	go build -o ocman .

run: build
	./ocman -addr 0.0.0.0:8228

clean:
	rm -rf ocman tmp internal/server/static/assets

# Run both Go and frontend test suites
test: test-backend test-frontend

test-backend:
	go test ./...

test-frontend:
	cd frontend && npm test

# Run Go tests with the race detector. Frontend tests are not race-detector
# relevant so they're skipped here — run `make test` for the full suite.
test-race: ## Run Go tests with -race
	go test -race ./internal/...

# Run every Fuzz* target across internal/ for a short time budget. Fuzzing
# is opt-in: `go test` skips fuzz targets unless -fuzz is passed, so the
# regular test suite stays fast.
test-fuzz: ## Run all Fuzz* targets for 10s each
	@for pkg in $$(go list ./internal/...); do \
		fuzzfns=$$(go test -list 'Fuzz.*' $$pkg 2>/dev/null | grep '^Fuzz' || true); \
		for fn in $$fuzzfns; do \
			echo "==> $$pkg $$fn"; \
			go test -run='^$$' -fuzz=$$fn -fuzztime=10s $$pkg || exit 1; \
		done; \
	done

# Print per-package coverage for internal/. Used to verify the NFR-3
# coverage targets in spec/backend-hardening.
test-coverage: ## Print per-package Go coverage for internal/
	go test -cover ./internal/...

# Run all linters and type checks
lint: lint-backend lint-frontend lint-platform-branching

lint-backend:
	go vet ./...

lint-frontend:
	cd frontend && npx tsc -b && npm run lint

# Guard against reintroducing `session.platform === 'foo'` branching,
# which would undermine the multi-platform architecture.
# Suppress individual lines with a trailing `// ocman:allow-platform-branch`
# pragma — expect to justify it in review.
lint-platform-branching:
	./scripts/check-platform-branching.sh

# --- Local observability stack (Grafana LGTM) ----------------------------
#
# Spins up grafana/otel-lgtm: Grafana on :3000, OTLP/gRPC on :4317,
# OTLP/HTTP on :4318. Pre-wired Loki/Tempo/Mimir datasources, anonymous
# Admin enabled (dev-only). See docker-compose.otel.yml.

otel-up:
	docker compose -f docker-compose.otel.yml up -d
	@echo ""
	@echo "  Grafana:    http://localhost:3000  (anonymous Admin)"
	@echo "  OTLP/gRPC:  localhost:4317"
	@echo "  OTLP/HTTP:  http://localhost:4318"
	@echo ""
	@echo "  Run ocman with:  ./ocman --otel=http://localhost:4318"

otel-down:
	docker compose -f docker-compose.otel.yml down

otel-logs:
	docker compose -f docker-compose.otel.yml logs -f lgtm

# Wipe the persisted telemetry volume too. Use when stale data clutters
# Grafana or you want a clean slate after schema changes.
otel-reset:
	docker compose -f docker-compose.otel.yml down -v

# --- Help ----------------------------------------------------------------
#
# `make help` lists every target with a `## ` doc comment after the
# colon. Targets without a doc comment are intentionally hidden from
# the listing — most of them are dev-loop internals (kill-children,
# build-frontend, etc) that the user shouldn't need to invoke
# directly.
help: ## Show this help
	@awk 'BEGIN {FS = ":.*?## "} /^[a-zA-Z_-]+:.*?## / {printf "  %-18s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

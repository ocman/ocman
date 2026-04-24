.PHONY: dev dev-backend dev-frontend dev-prod dev-prod-watch dev-kill-orphans build run clean test test-backend test-frontend test-e2e lint lint-backend lint-frontend lint-platform-branching

# Free dev ports in case a previous run left orphaned children (e.g. air's
# child ./tmp/ocman re-parented to init after an unclean `make` exit). Safe
# to run when nothing is listening — lsof just returns empty.
dev-kill-orphans:
	@stale=$$(lsof -nP -tiTCP:8229 -sTCP:LISTEN 2>/dev/null); \
		if [ -n "$$stale" ]; then \
			echo "Killing stale process(es) on :8229: $$stale"; \
			kill $$stale 2>/dev/null || true; \
			sleep 1; \
			kill -9 $$(lsof -nP -tiTCP:8229 -sTCP:LISTEN 2>/dev/null) 2>/dev/null || true; \
		fi
	@stale=$$(lsof -nP -tiTCP:8228 -sTCP:LISTEN 2>/dev/null); \
		if [ -n "$$stale" ]; then \
			echo "Killing stale process(es) on :8228: $$stale"; \
			kill $$stale 2>/dev/null || true; \
			sleep 1; \
			kill -9 $$(lsof -nP -tiTCP:8228 -sTCP:LISTEN 2>/dev/null) 2>/dev/null || true; \
		fi

# Run both backend (air) and frontend (vite) with live reload
dev: dev-kill-orphans
	@mkdir -p tmp
	@echo "Starting ocman dev environment..."
	@echo "  Backend (air):    http://localhost:8229"
	@echo "  Frontend (vite):  http://localhost:8228"
	@echo "  Backend log:      tmp/air.log"
	@echo "  Combined log:     tmp/debug.log"
	@echo ""
	@trap 'kill 0' EXIT; \
		{ $(MAKE) dev-backend & \
		  $(MAKE) dev-frontend & \
		  wait; } 2>&1 | tee tmp/debug.log

# Run with production frontend build + backend live reload (manual frontend rebuild)
dev-prod: dev-kill-orphans
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
	@trap 'kill 0' EXIT; \
		{ air 2>&1 | tee tmp/air.log & \
		  cd frontend && npm run preview 2>&1 | tee ../tmp/vite-preview.log & \
		  wait; }

# Run with production frontend build + auto-rebuild on changes + backend live reload
dev-prod-watch: dev-kill-orphans
	@mkdir -p tmp
	@echo "Starting ocman PRODUCTION MODE with AUTO-RELOAD..."
	@echo "  Backend (air):    http://localhost:8229"
	@echo "  Frontend (vite):  http://localhost:8228 (serves production build, auto-rebuilds)"
	@echo "  Backend log:      tmp/air.log"
	@echo "  Frontend log:     tmp/vite-preview.log"
	@echo "  Watch log:        tmp/frontend-watch.log"
	@echo ""
	@trap 'kill 0' EXIT; \
		{ air 2>&1 | tee tmp/air.log & \
		  cd frontend && npm run preview 2>&1 | tee ../tmp/vite-preview.log & \
		  ./scripts/watch-frontend-prod.sh 2>&1 | tee tmp/frontend-watch.log & \
		  wait; }

dev-backend:
	@mkdir -p tmp
	@air 2>&1 | tee tmp/air.log

dev-frontend:
	cd frontend && npm run dev

# Production build
build: build-frontend build-backend

build-frontend:
	cd frontend && npm ci && STRIP_TESTIDS=1 npm run build
	./scripts/check-no-testids.sh

build-backend:
	go build -o ocman .

run: build
	./ocman -addr 0.0.0.0:8228

clean:
	rm -rf ocman tmp internal/server/static/assets

# Run Go, frontend unit, and Playwright e2e test suites
test: test-backend test-frontend test-e2e

test-backend:
	go test ./...

test-frontend:
	cd frontend && npm test

# Run Playwright e2e tests. Playwright's webServer block runs `npm run preview`
# which requires a built frontend, so we build first.
# Set E2E_NO_WEBSERVER=1 and E2E_BASE_URL=http://localhost:8228 to use a running
# dev server instead of vite preview.
test-e2e:
	cd frontend && npm run build && npm run test:e2e

test-e2e-ui:
	cd frontend && npm run test:e2e:ui

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

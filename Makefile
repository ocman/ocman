.PHONY: dev dev-backend dev-frontend dev-prod dev-prod-watch build run clean test test-backend test-frontend lint lint-backend lint-frontend lint-platform-branching

# Run both backend (air) and frontend (vite) with live reload
dev:
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
	@trap 'kill 0' EXIT; \
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

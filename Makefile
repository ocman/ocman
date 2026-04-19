.PHONY: dev dev-backend dev-frontend build run clean test test-backend test-frontend lint lint-backend lint-frontend lint-platform-branching

# Run both backend (air) and frontend (vite) with live reload
dev:
	@mkdir -p tmp
	@echo "Starting ocman dev environment..."
	@echo "  Backend (air):    http://localhost:8080"
	@echo "  Frontend (vite):  http://localhost:8228"
	@echo "  Backend log:      tmp/air.log"
	@echo "  Combined log:     tmp/debug.log"
	@echo ""
	@trap 'kill 0' EXIT; \
		{ $(MAKE) dev-backend & \
		  $(MAKE) dev-frontend & \
		  wait; } 2>&1 | tee tmp/debug.log

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

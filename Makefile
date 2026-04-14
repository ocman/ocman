.PHONY: dev dev-backend dev-frontend build run clean

# Run both backend (air) and frontend (vite) with live reload
dev:
	@echo "Starting ocman dev environment..."
	@echo "  Backend (air):    http://localhost:8080"
	@echo "  Frontend (vite):  http://localhost:8228"
	@echo "  Backend log:      tmp/air.log"
	@echo ""
	@trap 'kill 0' EXIT; \
		$(MAKE) dev-backend & \
		$(MAKE) dev-frontend & \
		wait

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

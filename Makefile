.PHONY: docs docs-build dev dev-backend dev-remote dev-relay dev-frontend dev-prod dev-prod-watch kill-dev build build-desktop installer-mac installer-linux run clean test test-all-fast test-backend test-frontend test-e2e test-e2e-dev install-e2e-browsers test-race test-fuzz test-coverage coverage coverage-check lint lint-backend lint-frontend lint-platform-branching lint-settings-rows otel-up otel-down otel-logs otel-reset caddy-up caddy-down caddy-cert install-hooks help

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

# --- Share relay dev default ----------------------------------------------
#
# Dev targets point ocman at the local relay from `make dev-relay`, the same
# way the OTel endpoint above is wired up: it costs nothing when the relay
# is not running, and Settings → Sharing then shows the value so you can
# confirm it took effect. Override per-invocation:
#
#   make dev OCMAN_RELAY_URL=                          # no relay (local-only shares)
#   make dev OCMAN_RELAY_URL=https://share.example.com # a deployed relay
export OCMAN_RELAY_URL ?= http://localhost:$(RELAY_PORT)

# --- dev targets ----------------------------------------------------------
#
# The dev loop lives in scripts/dev.sh, not here. `make` catches Ctrl+C and
# does not forward it to a recipe shell, so a Makefile-hosted dev loop leaves
# air/vite orphaned on Ctrl+C (worst inside tmux). `exec`ing the script makes
# IT the foreground process, so Ctrl+C reaches it and its trap reaps cleanly.

# Run both backend (air) and frontend (vite) with live reload
dev: kill-dev
	@echo "Starting ocman dev environment (backend :8229, frontend :8228)..."
	@exec ./scripts/dev.sh dev

# Run with production frontend build + backend live reload (manual frontend rebuild)
dev-prod:
	@echo "Starting ocman PRODUCTION MODE (manual frontend rebuild)..."
	@cd frontend && pnpm build
	@exec ./scripts/dev.sh prod

# Run with production frontend build + auto-rebuild on changes + backend live reload
dev-prod-watch:
	@echo "Starting ocman PRODUCTION MODE with auto-rebuild..."
	@exec ./scripts/dev.sh prod-watch

dev-backend:
	@mkdir -p tmp
	@air 2>&1 | tee tmp/air.log

# Run backend only with the remote-access gRPC server enabled
# (multi-remote support). Uses air with .air.remote.toml which adds
# -remote-listen 0.0.0.0:8230 to the backend flags.
dev-remote: kill-dev
	@mkdir -p tmp
	@echo "Starting ocman dev environment WITH REMOTE ACCESS..."
	@echo "  Backend (air):    http://localhost:8229"
	@echo "  Remote  (gRPC):   localhost:8230"
	@echo "  Backend log:      tmp/air.log"
	@echo "  Remote config:    .air.remote.toml"
	@echo ""
	@exec ./scripts/dev.sh remote

dev-frontend:
	@mkdir -p tmp
	@cd frontend && pnpm dev 2>&1 | tee ../tmp/vite-dev.log

# Run the share relay locally (cmd/ocman-relay) on :8231.
#
# Storage lives under tmp/ so `make clean` and .gitignore already cover it —
# a relay store is disposable ciphertext, nothing worth keeping between runs.
# Override any of these to test other configurations, e.g.
#   make dev-relay RELAY_TTL=1m           # exercise expiry sweeps
#   make dev-relay RELAY_STORE=/tmp/other # a store that survives make clean
RELAY_PORT  ?= 8231
RELAY_ADDR  ?= 0.0.0.0:$(RELAY_PORT)
RELAY_STORE ?= tmp/relay-data
RELAY_TTL   ?= 720h

dev-relay: ## Run the share relay locally (:8231, store in tmp/relay-data)
	@mkdir -p tmp
	@echo "Starting ocman share relay..."
	@echo "  Bind:             $(RELAY_ADDR)"
	@echo "  API:              http://localhost:$(RELAY_PORT)/s"
	@echo "  Health:           http://localhost:$(RELAY_PORT)/healthz"
	@echo "  Store:            $(RELAY_STORE)"
	@echo "  TTL:              $(RELAY_TTL)"
	@if [ ! -f internal/webui/static/index.html ]; then \
		echo ""; \
		echo "  NOTE: no frontend build found, so the /v/<id> viewer will 404."; \
		echo "        Run 'cd frontend && pnpm build' to embed it."; \
	fi
	@echo ""
	@exec go run ./cmd/ocman-relay \
		-addr $(RELAY_ADDR) \
		-store $(RELAY_STORE) \
		-ttl $(RELAY_TTL)

# Emergency nuke: kill anything holding the dev ports. Use when a previous
# `make dev*` died badly and left orphans squatting on 8228 / 8229. Safe to
# run even when nothing is listening — xargs is a no-op on empty stdin on
# both BSD and GNU.
kill-dev:
	@echo "Reclaiming dev ports 8228, 8229, 8230, and $(RELAY_PORT)..."
	@lsof -tiTCP:8228 -sTCP:LISTEN 2>/dev/null | xargs kill -9 2>/dev/null || true
	@lsof -tiTCP:8229 -sTCP:LISTEN 2>/dev/null | xargs kill -9 2>/dev/null || true
	@lsof -tiTCP:8230 -sTCP:LISTEN 2>/dev/null | xargs kill -9 2>/dev/null || true
	@lsof -tiTCP:$(RELAY_PORT) -sTCP:LISTEN 2>/dev/null | xargs kill -9 2>/dev/null || true
	@pkill -x air 2>/dev/null || true
	@echo "Done. Run 'make dev' / 'make dev-remote' / 'make dev-prod-watch' to restart."

# Marketing/docs site (Hugo, sources in site/, content mounted from docs/)
# Reachable from other machines by default; override DOCS_HOST so generated
# links point at the address you browse from, e.g.
#   make docs DOCS_HOST=192.168.1.20
DOCS_BIND ?= 0.0.0.0
DOCS_HOST ?= localhost
DOCS_PORT ?= 1313
docs: ## Serve the docs site with live reload and open it (DOCS_BIND/DOCS_HOST/DOCS_PORT)
	cd site && hugo server --bind $(DOCS_BIND) --port $(DOCS_PORT) \
		--baseURL http://$(DOCS_HOST) --openBrowser --navigateToChanged

docs-build: ## Build the static docs site into site/public
	cd site && hugo --minify --gc

# Production build
build: build-frontend build-backend

build-frontend:
	cd frontend && pnpm install --frozen-lockfile && pnpm build

build-backend:
	go build -o ocman .

# Desktop (Wails) build — produces a native .app / binary via `wails build`.
#
# Asset architecture: the desktop binary boots the same Go HTTP server used
# in CLI mode and proxies the Wails WebView to it (see internal/gui/app.go).
# All assets are therefore served from the Go `//go:embed static/*` FS in
# internal/webui/static — NOT from Wails's own asset server. So the frontend
# must be built into internal/webui/static (the default `pnpm build` output),
# not frontend/dist. If you set WAILS_BUILD=1 here the embed picks up only
# the gitignored robots.txt placeholder and the desktop app shows a blank
# page with "robots.txt" as the only visible content.
build-desktop: ## Build the Wails desktop app (outputs to build/bin/)
	cd frontend && pnpm install --frozen-lockfile && pnpm build
	@mkdir -p build
	rsvg-convert -w 1024 -h 1024 frontend/public/favicon.svg -o build/appicon.png
	wails build -skipbindings -s -o ocman-desktop
	@# Flush the macOS icon/LaunchServices cache so the Dock shows the
	@# updated icon immediately without a logout. No-op on non-macOS.
	@if [ "$$(uname)" = "Darwin" ]; then \
		/System/Library/Frameworks/CoreServices.framework/Versions/A/Frameworks/LaunchServices.framework/Versions/A/Support/lsregister \
			-f build/bin/ocman.app 2>/dev/null || true; \
		killall Dock 2>/dev/null || true; \
	fi

# ---- macOS DMG installer --------------------------------------------------
#
# Requires: brew install create-dmg
#
# Produces dist/ocman.dmg — a drag-to-Applications installer with:
#   • Custom background colour (matches the app's dark UI)
#   • Icon position tuned for a single-app window
#   • Symlink to /Applications so the user just drags the .app
#
# Usage:
#   make installer-mac              # build .app then wrap in .dmg
#   make installer-mac BUILD=0      # skip the wails build (already done)
#   make installer-mac CI=1         # skip the Finder-prettifying AppleScript
#                                   #   (auto-set when $CI is non-empty; required
#                                   #    for headless runners — Forgejo Actions,
#                                   #    GitHub Actions, `act`, etc. — where
#                                   #    Finder times out with AppleEvent -1712)
#
BUILD ?= 1
# Auto-detect CI environments. GitHub Actions / Forgejo / GitLab / CircleCI all
# export $CI=true. `act` also exports $CI. Locally CI is empty, so the DMG keeps
# its pretty Finder layout.
CREATE_DMG_CI_FLAGS := $(if $(CI),--skip-jenkins,)

installer-mac: ## Build macOS DMG installer (requires create-dmg)
	@command -v create-dmg >/dev/null 2>&1 || { \
		echo "create-dmg not found. Install with:  brew install create-dmg"; exit 1; }
	@if [ "$(BUILD)" = "1" ]; then $(MAKE) build-desktop; fi
	@mkdir -p dist
	@rm -f dist/ocman.dmg
	@# Use the app icon if present (wails build produces build/appicon.png).
	@# Omit --volicon / --background when the file doesn't exist yet.
	@if [ -f build/appicon.png ]; then \
		create-dmg \
			$(CREATE_DMG_CI_FLAGS) \
			--volname "ocman" \
			--volicon "build/appicon.png" \
			--window-pos 200 120 \
			--window-size 540 380 \
			--icon-size 128 \
			--icon "ocman.app" 140 190 \
			--hide-extension "ocman.app" \
			--app-drop-link 400 190 \
			--no-internet-enable \
			"dist/ocman.dmg" \
			"build/bin/"; \
	else \
		create-dmg \
			$(CREATE_DMG_CI_FLAGS) \
			--volname "ocman" \
			--window-pos 200 120 \
			--window-size 540 380 \
			--icon-size 128 \
			--icon "ocman.app" 140 190 \
			--hide-extension "ocman.app" \
			--app-drop-link 400 190 \
			--no-internet-enable \
			"dist/ocman.dmg" \
			"build/bin/"; \
	fi
	@echo ""
	@echo "  Installer: dist/ocman.dmg"

# ---- Linux AppImage / tar.gz ----------------------------------------------
#
# The Wails Linux build produces a standalone binary (no bundled WebKit —
# the system GTK WebKitGTK is used). We package it as both:
#   dist/ocman-linux-amd64.tar.gz   — plain archive, no runtime deps listed
#   (AppImage support requires appimagetool; see the comment below)
#
# Usage:
#   make installer-linux

installer-linux: ## Build Linux binary archive (cross-compiled from any host)
	@mkdir -p dist
	# Frontend must land in internal/webui/static so go:embed picks it up.
	# See the build-desktop comment for why WAILS_BUILD=1 (frontend/dist) is
	# wrong for this proxy-based architecture.
	cd frontend && pnpm install --frozen-lockfile && pnpm build
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 \
		go build -ldflags="-s -w" -tags desktop,exclude_graphdriver_devicemapper \
		-o dist/ocman-linux-amd64 .
	tar -czf dist/ocman-linux-amd64.tar.gz -C dist ocman-linux-amd64
	@echo ""
	@echo "  Archive: dist/ocman-linux-amd64.tar.gz"
	@echo ""
	@echo "  Note: --gui on Linux requires libwebkit2gtk-4.0 and libgtk-3."
	@echo "  Without them, run without --gui for the browser-based UI."

run: build
	./ocman -addr 127.0.0.1:8228

clean:
	rm -rf ocman tmp internal/webui/static/assets

# Run both Go and frontend test suites
test: test-backend test-frontend

# Run all major suites in parallel and fail fast on the first failing one.
#
# Includes:
#   - backend unit/integration tests
#   - frontend unit tests
#   - Playwright e2e suite (preview mode)
#
# Why a custom orchestration block instead of `make -j`?
# `make -j` returns non-zero when one child fails, but it does not reliably
# terminate already-running siblings early enough to save time. This target
# starts each suite in a background process, mirrors its output to a per-suite
# log, then polls the child PIDs (portable to macOS's older Bash) to detect
# the first failure and kill the rest.
#
# Each individual runner also gets its own fail-fast flag where available:
#   - Go:         `-failfast`
#   - Vitest:     `--bail=1`
#   - Playwright: `--max-failures=1`
#
# So we stop as soon as either:
#   1. a suite sees its first failing test internally, or
#   2. the wrapper sees any suite exit non-zero.
test-all-fast: ## Run backend, frontend, and e2e suites in parallel (fail fast)
	@bash -lc 'set -euo pipefail; \
		mkdir -p tmp; \
		pids=(); names=(); logs=(); \
		start_suite() { \
			local name="$$1"; shift; \
			local log="tmp/test-$${name}.log"; \
			echo "==> $$name"; \
			( set -o pipefail; "$$@" 2>&1 | tee "$$log" ) & \
			pids+=("$$!"); names+=("$$name"); logs+=("$$log"); \
		}; \
		cleanup() { \
			for pid in "$${pids[@]}"; do \
				kill "$$pid" 2>/dev/null || true; \
			done; \
		}; \
		trap cleanup INT TERM EXIT; \
		start_suite backend go test -failfast ./...; \
		start_suite frontend bash -lc "cd frontend && pnpm test -- --bail=1"; \
		start_suite e2e bash -lc "cd frontend && pnpm build && pnpm test:e2e -- --max-failures=1"; \
		remaining="$${#pids[@]}"; \
		status=0; \
		failed_name=""; failed_log=""; \
		while [ "$$remaining" -gt 0 ]; do \
			for i in "$${!pids[@]}"; do \
				pid="$${pids[$$i]}"; \
				if [ -z "$$pid" ]; then continue; fi; \
				if kill -0 "$$pid" 2>/dev/null; then continue; fi; \
				if wait "$$pid"; then \
					pids[$$i]=""; \
					remaining=$$((remaining - 1)); \
				else \
					status=$$?; \
					failed_name="$${names[$$i]}"; \
					failed_log="$${logs[$$i]}"; \
					echo ""; \
					echo "FAIL-FAST: suite '$$failed_name' failed (see $$failed_log)"; \
					echo "FAIL-FAST: stopping remaining suites"; \
					cleanup; \
					wait || true; \
					break 2; \
				fi; \
			done; \
			sleep 0.2; \
		done; \
		if [ "$$status" -ne 0 ]; then \
			exit "$$status"; \
		fi; \
		trap - INT TERM EXIT; \
		echo ""; \
		echo "All suites passed."'

test-backend:
	go test ./...

test-frontend:
	cd frontend && pnpm test

# Run Playwright end-to-end tests. Build the frontend first so the
# Playwright webServer can serve `vite preview` from a fresh dist/.
test-e2e: ## Run Playwright end-to-end tests
	cd frontend && pnpm build && pnpm test:e2e

# Run Playwright against the Vite dev server (StrictMode, HMRless test run).
# Use this when a regression reproduces only in local dev mode.
test-e2e-dev: ## Run Playwright end-to-end tests against Vite dev mode
	cd frontend && E2E_USE_DEV_SERVER=1 pnpm test:e2e

# Install Playwright browser binaries used by e2e tests.
install-e2e-browsers: ## Install Playwright browser binaries
	cd frontend && pnpm exec playwright install

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

# Collect total line coverage into coverage/*.json. Pass SUITE=go or
# SUITE=frontend to collect just one side (default: both). Drives the
# CI coverage ratchet (spec/ci-coverage-ratchet).
coverage: ## Collect coverage into coverage/*.json (SUITE=go|frontend|all)
	./scripts/coverage-collect.sh $(SUITE)

# Compare the collected coverage/*.json against a baseline directory
# and fail if any suite dropped beyond the grace tolerance. CI passes
# BASELINE_DIR pointing at the gh-pages baseline; missing baseline =
# pass.
coverage-check: ## Compare coverage/*.json against $(BASELINE_DIR)
	./scripts/coverage-ratchet.sh "$(BASELINE_DIR)" $(SUITE)

# Run all linters and type checks
lint: lint-backend lint-frontend lint-platform-branching lint-host-helpers lint-settings-rows

lint-backend:
	go vet ./...
	golangci-lint run ./...

lint-frontend:
	cd frontend && pnpm exec tsc -b && pnpm lint

# Guard against reintroducing `session.platform === 'foo'` branching,
# which would undermine the multi-platform architecture.
# Suppress individual lines with a trailing `// ocman:allow-platform-branch`
# pragma — expect to justify it in review.
lint-platform-branching:
	./scripts/check-platform-branching.sh

# Guard against HTTP handlers calling host-local helpers (git/tmux/term)
# directly instead of routing through the hostsvc.Host seam, which would
# silently break for remote projects (architecture.md AD-16/R-A).
# Suppress justified exceptions with a trailing `// ocman:allow-host-helper`.
#
# The self-test runs first: the guard once rotted into a no-op because
# its patterns named renamed identifiers, so it must prove it still
# catches a planted bypass before its verdict is worth anything.
lint-host-helpers:
	./scripts/check_host_helpers_test.sh
	./scripts/check-host-helpers.sh

# Guard against hand-rolled `settings-row` markup that skips the
# <SettingRow>/<SettingToggle>/<SettingNumber> components, which would
# drop the save-status indicator (spinner/checkmark) for a setting.
# Suppress justified exceptions with `{/* ocman:allow-raw-setting */}`.
lint-settings-rows:
	./scripts/check-settings-rows.sh

# Regenerate the gRPC stubs for the multi-remote contract. Requires
# protoc plus the Go plugins:
#   go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
#   go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest
# The generated *.pb.go are committed so CI / make build don't need protoc.
proto:
	PATH="$$(go env GOPATH)/bin:$$PATH" protoc \
		--go_out=. --go_opt=module=github.com/NoUseFreak/ocman \
		--go-grpc_out=. --go-grpc_opt=module=github.com/NoUseFreak/ocman \
		internal/remote/proto/remote.proto

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

# --- Local HTTPS via Caddy + Tailscale -----------------------------------
#
# Exposes ocman at https://driess-macbook-pro.tail5f13e4.ts.net so that
# browser APIs requiring a secure context (microphone, Web Speech API)
# work on iPads connected to your tailnet.
#
# Caddy uses `get_certificate tailscale` — it delegates cert issuance to
# the Tailscale daemon, which gets a Let's Encrypt cert for your ts.net
# hostname. No manual CA installation needed on the iPad; the cert is
# already trusted by all devices.
#
# One-time setup:
#   1. brew install caddy
#   2. make caddy-up            # Caddy fetches the cert automatically on first start
#
# Then open https://driess-macbook-pro.tail5f13e4.ts.net on your iPad
# (both devices must be connected to Tailscale).

caddy-up: ## Start Caddy HTTPS proxy (https://driess-macbook-pro.tail5f13e4.ts.net → :8228)
	@command -v caddy >/dev/null 2>&1 || { \
		echo "caddy not found. Install with:  brew install caddy"; exit 1; }
	@command -v tailscale >/dev/null 2>&1 || { \
		echo "tailscale not found or not running"; exit 1; }
	caddy start --config Caddyfile
	@echo ""
	@echo "  ocman is now available at:"
	@echo "  https://driess-macbook-pro.tail5f13e4.ts.net"

caddy-down: ## Stop the Caddy HTTPS proxy
	caddy stop

caddy-cert: ## Pre-fetch the Tailscale TLS cert (optional; caddy-up does this automatically)
	@command -v tailscale >/dev/null 2>&1 || { \
		echo "tailscale not found or not running"; exit 1; }
	tailscale cert driess-macbook-pro.tail5f13e4.ts.net

install-hooks: ## Install pre-commit + pre-push hooks (requires pre-commit: pip install pre-commit)
	pre-commit install
	pre-commit install --hook-type pre-push

# --- Help ----------------------------------------------------------------
#
# `make help` lists every target with a `## ` doc comment after the
# colon. Targets without a doc comment are intentionally hidden from
# the listing — most of them are dev-loop internals (kill-children,
# build-frontend, etc) that the user shouldn't need to invoke
# directly.
help: ## Show this help
	@awk 'BEGIN {FS = ":.*?## "} /^[a-zA-Z_-]+:.*?## / {printf "  %-18s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

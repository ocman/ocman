# AGENTS.md

## What is ocman

A web dashboard for viewing OpenCode session data. Go backend reads OpenCode's SQLite database (read-only) and serves a React SPA. Can also proxy live data from running OpenCode instances via their HTTP API.

## Repository layout

- `main.go` — entrypoint; parses `-addr` and `-db` flags, opens the DB, starts the server
- `internal/db/` — SQLite queries against OpenCode's `session`, `message`, `part` tables; uses `json_extract` heavily
- `internal/server/server.go` — HTTP server, API handlers, static file serving with SPA fallback, OpenCode port discovery via `lsof`
- `internal/server/frontend/` — React + TypeScript + Vite SPA (port 8228 in dev)
- `internal/server/static/` — Vite build output; embedded into the Go binary via `//go:embed`

## Dev commands

```sh
make dev            # runs backend (air) + frontend (vite) concurrently with live reload
make dev-backend    # air only (Go on :8080)
make dev-frontend   # vite only (React on :8228, proxies /api to :8080)
make build          # production: npm ci + npm run build, then go build -o ocman .
make clean          # removes ocman binary, tmp/, and static/assets/
```

- `mise` provides `air` (Go live-reload). Run `mise install` if air is missing.
- The Vite dev server proxies `/api` requests to `localhost:8080`.

## Build pipeline

1. `cd internal/server/frontend && npm ci && npm run build` — builds frontend into `internal/server/static/`
2. `go build -o ocman .` — embeds `internal/server/static/` via `//go:embed`

Order matters: frontend must be built before `go build` so static assets are embedded.

## Key details

- **Single Go dependency**: `github.com/mattn/go-sqlite3` (CGo required)
- **DB is read-only**: opened with `?mode=ro&_journal_mode=WAL`. The database lives at `~/.local/share/opencode/opencode.db` by default.
- **No tests exist** in either Go or frontend as of now.
- **CI**: `.github/workflows/ci.yml` runs frontend lint + typecheck, `go vet`, and a full `make build`. Runs on push/PR to `main`.
- **No linter/formatter config** for Go. Frontend has ESLint (`eslint.config.js`).
- Frontend lint: `cd internal/server/frontend && npm run lint`
- Frontend typecheck: `cd internal/server/frontend && npx tsc -b`

## Conventions

- All Go packages live under `internal/` — nothing is exported.
- The server discovers running OpenCode instances by parsing `lsof` output for processes named `opencode` listening on TCP ports, then resolving their cwd.
- Session status is inferred from the last message's `role` and `finish` fields, not stored explicitly.

# AGENTS.md

## What is ocman

A web dashboard for viewing OpenCode session data. Go backend reads OpenCode's SQLite database (read-only) and serves a React SPA. Can also proxy live data from running OpenCode instances via their HTTP API.

## Repository layout

- `main.go` — entrypoint; parses `-addr` and `-db` flags, opens both databases, starts the server
- `internal/db/` — read-only SQLite queries against OpenCode's `session`, `message`, `part` tables; uses `json_extract` heavily
- `internal/state/` — writable SQLite database (`~/.local/share/ocman/state.db`) for ocman's own state (archived/seen sessions)
- `internal/server/` — HTTP server, API handlers, static file serving with SPA fallback, OpenCode port discovery via `lsof`, tmux integration, whisper transcription
- `frontend/` — React + TypeScript + Vite SPA (port 8228 in dev)
- `internal/server/static/` — Vite build output; embedded into the Go binary via `//go:embed`. Gitignored except for `.gitkeep`.

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

1. `cd frontend && npm ci && npm run build` — builds frontend into `internal/server/static/`
2. `go build -o ocman .` — embeds `internal/server/static/` via `//go:embed`

Order matters: frontend must be built before `go build` so static assets are embedded.

## Verification

CI runs these checks (`.github/workflows/ci.yml`):

```sh
cd frontend && npm run lint       # ESLint
cd frontend && npx tsc -b         # TypeScript typecheck
go vet ./...                      # Go vet
make build                        # full production build (frontend + Go)
```

No tests exist in either Go or frontend. No Go linter/formatter config.

## Key details

- **CGo required**: `github.com/mattn/go-sqlite3` needs a C compiler.
- **Two databases**: OpenCode's DB is opened read-only (`?mode=ro&_journal_mode=WAL`, default `~/.local/share/opencode/opencode.db`). Ocman's own state DB is writable (`~/.local/share/ocman/state.db`) and auto-creates its schema on startup.
- **OpenCode port discovery** uses `lsof` to find processes named `opencode` listening on TCP, then resolves their cwd. This only works on macOS/Linux. Results are cached with a 3-second TTL.
- **Session status** is inferred at query time from the last message's `role`, `finish`, and `error` fields — not stored.
- **Auto-archive**: the server background-goroutine archives sessions inactive for 7+ days (checked every 24h).

## Conventions

- All Go packages live under `internal/` — nothing is exported.
- API routes use `requireGET`/`requirePOST` wrappers for method enforcement. Some routes (tmux) additionally require `localhost` origin via `requireLocalhost`.
- Frontend state management uses Zustand. Routing uses react-router-dom.

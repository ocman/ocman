# Contributing

## Architecture

```mermaid
graph TD
    subgraph Browser
        SPA[React SPA]
    end

    subgraph ocman Server
        HTTP[HTTP Server]
        Static[Embedded Static Assets]
        Registry[Platform Registry]
        Archive[Auto-Archive Goroutine]
    end

    subgraph Adapters
        OCAd[OpenCode adapter]
    end

    subgraph Storage
        OC_DB[(OpenCode DB<br/>read-only)]
        State_DB[(ocman state.db<br/>read-write)]
    end

    subgraph External
        OC[Running OpenCode instances]
    end

    SPA -->|/api/*| HTTP
    SPA -->|static files| Static
    HTTP --> Registry
    Registry --> OCAd
    OCAd --> OC_DB
    OCAd -->|lsof + HTTP| OC
    HTTP -->|archived/seen| State_DB
    Archive --> State_DB
```

## Project structure

```
main.go                                    entrypoint (-addr, -db, -platforms, -auth-* flags)
frontend/                                  React + TypeScript + Vite SPA
e2e/                                       Playwright end-to-end suite
internal/platforms/                        Platform interface, Registry, common types/errors
internal/platforms/opencode/               OpenCode adapter (DB + HTTP proxy)
internal/platforms/claudecode/             Claude Code adapter (JSONL scanner, hook handler)
internal/db/                               SQLite queries against OpenCode's DB
internal/state/                            ocman's own writable state DB (archived/seen,
                                            auth secret, favorites, cached projects)
internal/server/                           HTTP server, API handlers, static file serving,
                                            OpenCode port discovery, tmux launcher, auth
internal/server/static/                    Vite build output (embedded into Go binary)
spec/                                      Requirements + architecture notes per feature
```

## Development

### Requirements

- Go 1.24+
- Node.js 22+
- [mise](https://mise.jdx.dev/) — provides `air` for live-reload (`mise install`)
- `tmux` on `PATH` for the in-UI launcher

### Quick command reference

| Command                | Port | Live Reload | Use Case |
|------------------------|------|-------------|----------|
| `make dev`             | 8228 | Yes (HMR)   | Regular development |
| `make dev-prod-watch`  | 8228 | Manual refresh | Memory profiling, production testing |
| `make dev-prod`        | 8228 | No          | One-time production test |

### All commands

| Command                | Description |
|------------------------|-------------|
| `make dev`             | Backend (air) + frontend (vite dev) with HMR on :8228 |
| `make dev-prod`        | Backend (air) + frontend (vite preview of production build) on :8228 |
| `make dev-prod-watch`  | Backend (air) + frontend (vite preview, auto-rebuilds on changes) on :8228 |
| `make dev-backend`     | Go backend only (air, port 8229) |
| `make dev-frontend`    | Vite dev server only (port 8228) |
| `make test`            | `go test ./...`, `vitest run`, and the Playwright e2e suite |
| `make lint`            | `go vet`, `tsc -b`, `eslint`, and the platform-branching check |
| `make build`           | Production build (frontend then Go binary) |
| `make clean`           | Remove binary, tmp/, and static/assets/ |

### Build pipeline

1. `cd frontend && npm ci && npm run build` — builds frontend into `internal/server/static/`
2. `go build -o ocman .` — embeds `internal/server/static/` via `//go:embed`

Order matters: the frontend must be built before `go build`.

### Development workflows

**Regular frontend development:**
```bash
make dev
# → Instant HMR updates; best for UI iteration
```

**Memory profiling / production testing:**
```bash
make dev-prod-watch
# → Edit code → auto-rebuild → manually refresh browser
```

> Vite's preview mode doesn't include HMR. The watcher rebuilds automatically but you need to
> refresh the browser manually. This is intentional — production builds are for testing, not
> rapid iteration.

## Tests

The repo has **180+ Go tests** (unit + integration), **81 frontend tests** (vitest), and a
**Playwright end-to-end suite** covering auth, the dashboard, composer, SSE streaming, and
prompts. CI runs all three on every PR; `make test` runs them locally.

## Platform-agnostic frontend

The frontend must not branch on `session.platform === 'opencode'` (or similar string comparisons).
UI capability gating goes through `/api/capabilities` + `useCapabilities()` instead. This is
enforced by `scripts/check-platform-branching.sh`, which `make lint` runs. If you hit a false
positive, the pragma `// ocman:allow-platform-branch` suppresses the check on the next line —
use sparingly (e.g. for generic helpers that legitimately need the platform identity).

# ocman

[![CI](https://github.com/NoUseFreak/ocman/actions/workflows/ci.yml/badge.svg)](https://github.com/NoUseFreak/ocman/actions/workflows/ci.yml)
[![Release](https://github.com/NoUseFreak/ocman/actions/workflows/release.yml/badge.svg)](https://github.com/NoUseFreak/ocman/actions/workflows/release.yml)
[![Go](https://img.shields.io/github/go-mod/go-version/NoUseFreak/ocman)](https://go.dev/)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)

A web dashboard for viewing [OpenCode](https://github.com/anomalyco/opencode)
session data. The Go backend serves a React SPA that lists, searches,
and replays sessions, and proxies live traffic (SSE events, composer,
compact, permission replies) to running OpenCode instances
auto-discovered via `lsof`.

Ocman is wired through a generic `Platform` adapter interface
(`internal/platforms/`) so the frontend stays platform-agnostic —
UI is gated on a `/api/capabilities` endpoint rather than branching
on platform identity.

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

## Requirements

- Go 1.26+ (CGo enabled — required by `go-sqlite3`)
- Node.js 20+
- [mise](https://mise.jdx.dev/) (provides `air` for live-reload; run `mise install`)
- `~/.local/share/opencode/opencode.db` must exist (ocman fails fast
  otherwise).

To **interact** with a running session (send messages, respond to
permissions/questions, abort, compact) you must start OpenCode with
an explicit port so its HTTP API is reachable:

```sh
opencode --port 0   # let OpenCode pick a free port
# or pin a specific port, e.g. opencode --port 4096
```

Ocman discovers listening OpenCode processes via `lsof` and
auto-connects. Without `--port`, sessions are still readable from
the database but the composer and other interactive features stay
disabled — the UI shows a hint telling you to re-launch OpenCode
with `--port 0`.

## Quick start

```sh
# Install tools
mise install

# Development mode (Vite dev server with HMR)
make dev
# Open http://localhost:8228

# OR: Production mode with auto-rebuild (for memory profiling)
make dev-prod-watch
# Open http://localhost:8228
```

**Development mode** (`make dev`): Opens on port **8228** - Vite dev server with instant HMR updates.  
**Production mode** (`make dev-prod-watch`): Opens on port **8228** - Vite preview mode serves production build, auto-rebuilds on changes (**manual browser refresh required**).

## Build

```sh
make build
```

This builds the frontend first (`npm ci && npm run build`), then compiles the Go binary with the static assets embedded. Order matters — the frontend must be built before `go build`.

## Usage

```sh
./ocman                              # default: listens on 127.0.0.1:8228, reads ~/.local/share/opencode/opencode.db
./ocman -addr localhost:9090         # custom listen address
./ocman -db /path/to/opencode.db     # custom OpenCode database path
```

Ocman's own state (archived / seen flags, per-platform) lives in
`~/.local/share/ocman/state.db`, auto-created on first run.

## Project structure

```
main.go                                    entrypoint (-addr, -db flags)
frontend/                                  React + TypeScript + Vite SPA
internal/platforms/                        Platform interface, Registry, common types/errors
internal/platforms/opencode/               OpenCode adapter (DB + HTTP proxy)
internal/db/                               SQLite queries against OpenCode's DB
internal/state/                            ocman's own writable state DB
internal/server/                           HTTP server, API handlers, static file serving,
                                           OpenCode port discovery
internal/server/static/                    Vite build output (embedded into Go binary)
spec/multi-agent-support/                  Requirements + architecture notes
```

## Development

### Quick Command Reference

| Command                | Port | Live Reload | Use Case |
|------------------------|------|-------------|----------|
| `make dev`             | 8228 | ✅ Yes (HMR) | Regular development |
| `make dev-prod-watch`  | 8228 | ⚠️ Manual refresh | Memory profiling, production testing |
| `make dev-prod`        | 8228 | ❌ No | One-time production test |

### All Commands

| Command                | Port | Description                                      |
|------------------------|------|--------------------------------------------------|
| `make dev`             | 8228 | Backend (air) + frontend (vite dev) with HMR |
| `make dev-prod`        | 8228 | Backend (air) + frontend (vite preview of production build) |
| `make dev-prod-watch`  | 8228 | Backend (air) + frontend (vite preview, auto-rebuilds on changes) |
| `make dev-backend`     | 8229 | Go backend only (air, port 8229)                 |
| `make dev-frontend`    | 8228 | Vite dev server only (port 8228)                 |
| `make test`            | `go test ./...` + `vitest run`                   |
| `make lint`            | `go vet`, `tsc -b`, `eslint`, and the platform-branching check |
| `make build`           | Production build                                 |
| `make clean`           | Remove binary, tmp/, and static/assets/          |

### Development Workflows

**Regular Frontend Development:**
```bash
make dev
# → Instant HMR updates
# → Best for UI iteration
```

**Memory Profiling / Production Testing:**
```bash
make dev-prod-watch
# → Edit code → auto-rebuild → manually refresh browser
# → Shows real production memory usage
# → Good for debugging the 1GB memory issue
```

**Why no auto-refresh in prod mode?**  
Vite's preview mode serves the production build but doesn't include HMR. The watcher rebuilds automatically, but you need to manually refresh your browser to see changes. This is by design - production builds are meant for testing, not rapid iteration.

### Tests

The repo has **180+ Go tests** (unit + integration, in-package
`*_test.go` files) and **81 frontend tests** (vitest). CI runs both
on every PR; `make test` runs them locally.

### Platform-agnostic frontend

The frontend must not branch on `session.platform === 'opencode'`
(or similar). UI gating goes through `/api/capabilities` +
`useCapabilities()` instead. This is enforced by
`scripts/check-platform-branching.sh`, which `make lint` runs. If
you hit a false positive, the pragma `// ocman:allow-platform-branch`
suppresses the check on the next line — use it sparingly (e.g. for
generic helpers that legitimately need the platform identity).

## License

This project is licensed under the MIT License. See [LICENSE](LICENSE) for details.

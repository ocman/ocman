# ocman

[![CI](https://github.com/NoUseFreak/ocman/actions/workflows/ci.yml/badge.svg)](https://github.com/NoUseFreak/ocman/actions/workflows/ci.yml)
[![Release](https://github.com/NoUseFreak/ocman/actions/workflows/release.yml/badge.svg)](https://github.com/NoUseFreak/ocman/actions/workflows/release.yml)
[![Go](https://img.shields.io/github/go-mod/go-version/NoUseFreak/ocman)](https://go.dev/)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)

A web dashboard for viewing coding-agent session data. The Go backend
serves a React SPA that lists, searches, and replays sessions from
multiple platforms side-by-side, and — where the platform supports it —
lets you send new prompts back into them.

As of v2, ocman supports:

- **[OpenCode](https://github.com/anomalyco/opencode)** — reads its
  SQLite database read-only and proxies live traffic (SSE events,
  composer, compact, permission replies) to running OpenCode
  instances auto-discovered via `lsof`.
- **[Claude Code](https://code.claude.com)** — reads its per-session
  JSONL transcripts from `~/.claude/projects/`, tracks live session
  state via HTTP hooks that ocman installs into
  `~/.claude/settings.json`, and injects new prompts using
  `claude -p --resume`.

Both platforms are wired through a common `Platform` adapter interface
(`internal/platforms/`). The frontend is platform-agnostic: it gates
UI on a `/api/capabilities` endpoint rather than branching on platform
identity.

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
        Hooks[/api/hooks/claude/]
        Archive[Auto-Archive Goroutine]
    end

    subgraph Adapters
        OCAd[OpenCode adapter]
        CCAd[Claude Code adapter]
    end

    subgraph Storage
        OC_DB[(OpenCode DB<br/>read-only)]
        CC_JSONL[(Claude Code JSONL<br/>read-only)]
        CC_Settings[(~/.claude/settings.json<br/>read-write)]
        State_DB[(ocman state.db<br/>read-write)]
    end

    subgraph External
        OC[Running OpenCode instances]
        Claude[claude CLI]
    end

    SPA -->|/api/*| HTTP
    SPA -->|static files| Static
    HTTP --> Registry
    Registry --> OCAd
    Registry --> CCAd
    OCAd --> OC_DB
    OCAd -->|lsof + HTTP| OC
    CCAd --> CC_JSONL
    CCAd -->|install hooks| CC_Settings
    CCAd -->|claude -p --resume| Claude
    Hooks -->|live state| CCAd
    Claude -->|POST hook events| Hooks
    HTTP -->|archived/seen| State_DB
    Archive --> State_DB
```

## Requirements

- Go 1.26+ (CGo enabled — required by `go-sqlite3`)
- Node.js 20+
- [mise](https://mise.jdx.dev/) (provides `air` for live-reload; run `mise install`)

Optional per-platform:

- OpenCode: `~/.local/share/opencode/opencode.db` must exist (ocman
  fails fast otherwise). Running OpenCode instances are discovered
  automatically; no extra configuration needed.
- Claude Code: ocman auto-detects `~/.claude/projects/`. On startup
  ocman installs a managed block of hooks into `~/.claude/settings.json`
  — see [Claude Code integration](#claude-code-integration) below.
  Without Claude Code installed, the Claude Code adapter stays
  registered but silent.

## Quick start

```sh
# Install tools
mise install

# Run with live reload (backend on :8080, frontend on :8228)
make dev
```

Open <http://localhost:8228> during development (Vite proxies `/api` to the Go backend).

## Build

```sh
make build
```

This builds the frontend first (`npm ci && npm run build`), then compiles the Go binary with the static assets embedded. Order matters — the frontend must be built before `go build`.

## Usage

```sh
./ocman                              # default: listens on 0.0.0.0:8080, reads ~/.local/share/opencode/opencode.db
./ocman -addr localhost:9090         # custom listen address
./ocman -db /path/to/opencode.db     # custom OpenCode database path
```

Ocman's own state (archived / seen flags, per-platform) lives in
`~/.local/share/ocman/state.db`, auto-created on first run.

## Claude Code integration

Ocman installs a small block of HTTP hooks into
`~/.claude/settings.json` on startup so it can track live session
state (busy/idle/pending-permission). The block is identified by a
sentinel (`_owner: "ocman"`) and is installed idempotently — repeat
runs won't duplicate entries, and removing ocman is a matter of
deleting the sentinel block.

- **What the hooks do**: each hook POSTs a small JSON payload to
  `http://127.0.0.1:<addr>/api/hooks/claude` on localhost. The
  receiver accepts POSTs from loopback only.
- **Hook events installed**: `UserPromptSubmit`, `PreToolUse`,
  `PostToolUse`, `Stop`, `SubagentStop`, `Notification`,
  `SessionStart`. The full set is the minimum needed to reason about
  live state; adding more requires no user action beyond restarting
  ocman.
- **Preserved keys**: ocman rewrites only its own entries. Any other
  keys in `settings.json` (e.g. `effortLevel`, `enabledPlugins`, your
  own hooks) are left untouched.
- **Live-state retention**: in-memory only. Nothing is persisted
  across ocman restarts; the next hook event naturally rebuilds it.
  Busy state has a 2 min TTL so a missed `Stop` event doesn't leave
  a session stuck.
- **Composer guard**: the composer (`claude -p --resume`) refuses to
  send while the target session is reported `busy`, returning
  HTTP 409. This prevents the conversation tree from forking when
  ocman and a live `claude` TUI both write to the same session — see
  `spec/multi-agent-support/phase7/findings.md` for the full
  experiment.

If you prefer not to have ocman modify `settings.json`, don't run
ocman. There is no per-run flag to disable the installer at this
time; skipping the install silently would defeat the point of the
integration.

## Project structure

```
main.go                                    entrypoint (-addr, -db flags)
frontend/                                  React + TypeScript + Vite SPA
internal/platforms/                        Platform interface, Registry, common types/errors
internal/platforms/opencode/               OpenCode adapter (DB + HTTP proxy)
internal/platforms/claudecode/             Claude Code adapter (JSONL + hooks + composer)
internal/db/                               SQLite queries against OpenCode's DB
internal/state/                            ocman's own writable state DB
internal/server/                           HTTP server, API handlers, static file serving,
                                           OpenCode port discovery, Claude Code hook receiver
internal/server/static/                    Vite build output (embedded into Go binary)
spec/multi-agent-support/                  Requirements + architecture + Phase 7 findings
```

## Development

| Command              | Description                                      |
|----------------------|--------------------------------------------------|
| `make dev`           | Backend (air) + frontend (vite) with live reload |
| `make dev-backend`   | Go backend only (air, port 8080)                 |
| `make dev-frontend`  | Vite dev server only (port 8228)                 |
| `make test`          | `go test ./...` + `vitest run`                   |
| `make lint`          | `go vet`, `tsc -b`, `eslint`, and the platform-branching check |
| `make build`         | Production build                                 |
| `make clean`         | Remove binary, tmp/, and static/assets/          |

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

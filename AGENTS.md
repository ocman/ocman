# AGENTS.md

## What is ocman

A web dashboard for viewing coding-agent session data across multiple
platforms. As of v2, ocman supports:

- **OpenCode** — reads OpenCode's SQLite database (read-only) and
  proxies live data from running OpenCode instances via their HTTP
  API.
- **Claude Code** — reads Claude Code's per-session JSONL transcripts
  from `~/.claude/projects/` and installs HTTP hooks into
  `~/.claude/settings.json` to track live session state. Can inject
  new prompts into any session via `claude -p --resume`.

Both platforms are wired through a common `Platform` adapter interface
(`internal/platforms/`). Adding a third platform (e.g. Codex) is a
new adapter + registry entry; see
`spec/multi-agent-support/architecture.md` for the design.

Ocman also supports **on-demand OpenCode worktree sessions** via the
`/wt` command in the command palette and the per-project Worktrees
view (`/project/<dir>/worktrees`). The feature shells out to
`git worktree add` under `<repo-parent>/.worktrees/<repo>/<slug>/`,
then launches `opencode --port 0` in tmux rooted at that worktree so
parallel sessions stop interfering with each other's files, rebuilds,
and staging area.

## Repository layout

- `main.go` — entrypoint; parses `-addr`, `-db`, and `-platforms`
  flags, opens databases, registers platform adapters, starts the
  server.
- `internal/platforms/` — `Platform` interface, `Registry`, common
  types/errors.
- `internal/platforms/opencode/` — OpenCode adapter wrapping the DB
  + HTTP proxy client.
- `internal/platforms/claudecode/` — Claude Code adapter: JSONL
  scanner, parser, mtime-keyed cache, in-memory live-state cache,
  hook settings installer, `claude -p` composer.
- `internal/db/` — read-only SQLite queries against OpenCode's
  `session`, `message`, `part` tables; uses `json_extract` heavily.
- `internal/state/` — writable SQLite database
  (`~/.local/share/ocman/state.db`) for ocman's own state (archived
  / seen sessions). Primary key is `(platform, session_id)` so it
  can scope state per platform.
- `internal/server/` — HTTP server, API handlers, static file serving
  with SPA fallback, OpenCode port discovery via `lsof`, tmux
  integration, whisper transcription, Claude Code hook handler +
  boot-time hook installation.
- `frontend/` — React + TypeScript + Vite SPA (port 8228 in dev).
- `internal/server/static/` — Vite build output; embedded into the Go
  binary via `//go:embed`. Gitignored except for `robots.txt`, which
  is kept as a permanent placeholder so `go:embed static/*` always has
  at least one file to embed (avoiding churn from build-hashed assets
  like `index.html`).

## Dev commands

```sh
make dev              # backend (air :8229) + frontend (vite dev :8228) with HMR
make dev-prod         # backend (air :8229) + frontend (vite preview :8228, manual rebuild)
make dev-prod-watch   # backend (air :8229) + frontend (vite preview :8228, auto-rebuild)
make dev-backend      # air only (Go on :8229)
make dev-frontend     # vite only (React on :8228, proxies /api to :8229)
make test             # runs `go test ./...` + `vitest run`
make lint             # runs go vet, tsc -b, eslint, and the platform-branching check
make build            # production: npm ci + npm run build, then go build -o ocman .
make clean            # removes ocman binary, tmp/, and static/assets/
make otel-up          # start Grafana LGTM stack (Loki/Tempo/Mimir + OTLP) at :3000/:4317/:4318
make otel-down        # stop the LGTM stack
make otel-logs        # tail LGTM container logs
make otel-reset       # stop + wipe persisted telemetry data
```

- `mise` provides `air` (Go live-reload). Run `mise install` if air
  is missing.
- Both dev and dev-prod modes use port **8228** for frontend, **8229** for backend.
- The frontend (Vite dev or preview) proxies `/api` requests to `localhost:8229`.
- Air rebuilds Go on source changes but does **not** re-embed the
  frontend bundle. After editing frontend code, either (a) use the
  Vite dev server on :8228 instead of the embedded build, or (b)
  run `cd frontend && npm run build` and touch a `.go` file to
  trigger Air.

## Build pipeline

1. `cd frontend && npm ci && npm run build` — builds frontend into
   `internal/server/static/`.
2. `go build -o ocman .` — embeds `internal/server/static/` via
   `//go:embed`.

Order matters: frontend must be built before `go build` so static
assets are embedded.

## Verification

CI runs these checks (`.github/workflows/ci.yml`):

```sh
cd frontend && npm run lint       # ESLint
cd frontend && npx tsc -b         # TypeScript typecheck
cd frontend && npm test           # vitest (81 tests)
go test ./...                     # Go unit + integration tests (180+ tests)
go vet ./...                      # Go vet
./scripts/check-platform-branching.sh
                                  # catches `platform === 'opencode'` style
                                  # branching in the frontend; the rule is
                                  # enforced by AD-12a.
make build                        # full production build (frontend + Go)
```

All of the above are wrapped by `make test` and `make lint` for local
use. The repo has no Go formatter/linter config beyond `go vet`; keep
diffs minimal and match the surrounding code.

## Key details

- **`-platforms` flag**: comma-separated list of platforms to enable
  (default `"opencode"`). Valid values: `opencode`, `claude-code`.
  Only the listed adapters are registered; the OpenCode database is
  not required when `opencode` is omitted from the list. Example:
  `-platforms opencode,claude-code`.
- **CGo required**: `github.com/mattn/go-sqlite3` needs a C compiler.
- **Two databases**: OpenCode's DB is opened read-only
  (`?mode=ro&_journal_mode=WAL`, default `~/.local/share/opencode/opencode.db`).
  Ocman's own state DB is writable (`~/.local/share/ocman/state.db`),
  auto-creates its schema, and runs a versioned migration on startup.
- **OpenCode port discovery** uses `lsof` to find processes named
  `opencode` listening on TCP, then resolves their cwd. macOS/Linux
  only. Cached with a 3-second TTL.
- **Claude Code live state** is driven by HTTP hooks fired from the
  `claude` CLI. Ocman installs a managed block of hooks into
  `~/.claude/settings.json` on startup (sentinel `_owner: "ocman"`
  marks them for idempotent re-install / removal); hooks POST to
  `/api/hooks/claude` on loopback. Live state is kept in-memory only
  (`liveCache`), with a 2 min TTL on `busy` to recover from missed
  `Stop` events.
- **Claude Code composer** spawns `claude -p --resume <id> <message>`
  detached from the request context (so the subprocess outlives the
  HTTP handler). Refuses to send while the target session is
  reported `busy` (see AD-13 / R1); returns HTTP 409 with a
  "try again" body.
- **Session status** for OpenCode is inferred at query time from the
  last message's `role`, `finish`, and `error` fields. For Claude
  Code it's inferred from the last `type` in the JSONL + overlaid
  with the live cache.
- **Auto-archive**: background goroutine archives sessions inactive
  for 7+ days (checked every 24 h). Runs against all registered
  platforms.
- **OpenTelemetry (optional)**: pass `--otel=<endpoint>` (or set
  `OTEL_EXPORTER_OTLP_ENDPOINT`) to ship traces and metrics to an OTLP
  collector. Empty / unset = no-op (zero overhead — the SDK no-op
  providers stay in place). The URL scheme picks the transport:
  `http(s)://...` → OTLP/HTTP, `grpc(s)://...` or bare `host:port` →
  OTLP/gRPC. Everything else is configured via standard `OTEL_*` env
  vars (`OTEL_SERVICE_NAME`, `OTEL_RESOURCE_ATTRIBUTES`,
  `OTEL_TRACES_SAMPLER`, `OTEL_EXPORTER_OTLP_HEADERS`, etc.).
  Instrumentation: `otelhttp` on the inbound mux and outbound HTTP
  clients, `otelsql` on both SQLite handles, custom spans/metrics
  around the auto-archive loop, projects-index refresh, SSE event
  streams, and the `srvtiming` phase boundaries (every existing
  `srvtiming.Record` call also emits a span event). A logrus hook
  decorates log lines with `trace_id`/`span_id` while a span is
  active. For local dev: `make otel-up` starts the bundled
  `grafana/otel-lgtm` stack (Grafana + Loki + Tempo + Mimir +
  collector) on `:3000` / `:4317` / `:4318`; the `make dev*` targets
  export `OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4318` and
  `OTEL_SERVICE_NAME=ocman-dev` automatically (override per
  invocation, set to empty string to disable). The `ocman Overview`
  Grafana dashboard is provisioned automatically from
  `observability/grafana/dashboards/ocman.json` and lives in the
  `ocman` folder once the stack starts. See `internal/telemetry` and
  `observability/`.
- **Optional password auth**: by default ocman binds `127.0.0.1:8228`
  and is unauthenticated. Set `OCMAN_AUTH_PASSWORD` (env, preferred),
  `-auth-password-file`, or `-auth-password` (precedence in that
  order) to require a password. When auth is configured it applies to
  **every** client, including localhost — pass `-auth-trust-localhost`
  (or set `OCMAN_AUTH_TRUST_LOCALHOST=1`) to restore the old "local
  user is trusted" escape hatch for dev loops. The password is
  bcrypt-hashed at startup; cookies are HMAC-signed (stateless) with
  a key persisted in `state.db`'s `auth_secret` table so sessions
  survive restarts. Login attempts are rate-limited (5/min per IP);
  trusted-localhost clients skip the limiter. See
  `internal/server/auth.go`.

## Conventions

- All Go packages live under `internal/` — nothing is exported.
- **Platform-agnostic frontend.** The UI must not branch on
  `session.platform === '...'`; capability gating goes through
  `/api/capabilities` + `useCapabilities()`. Enforced by
  `scripts/check-platform-branching.sh`, which `make lint` runs.
  The pragma `// ocman:allow-platform-branch` can suppress false
  positives when the comparison is part of a generic helper.
- **Terminology**: *platform* = the tool that produced the session
  (OpenCode / Claude Code). *Agent* = a composer-level role within a
  session (OpenCode's `build` / `plan` / user-defined subagent).
  Claude Code has no per-session agent concept.
- API routes use `requireGET` / `requirePOST` wrappers for method
  enforcement. Some routes (tmux, hook receiver) additionally require
  `localhost` origin via `requireLocalhost`.
- Frontend state management uses Zustand. Routing uses
  react-router-dom.
- Tests live alongside code as `*_test.go` / `*.test.ts(x)`. Prefer
  table-driven tests in Go. The server package has a shared
  `fakePlatform` for integration tests.
- **E2e test locators.** Playwright e2e tests (`frontend/e2e/`) must
  prefer stable locators over CSS class selectors. Priority order:
  1. **ARIA roles / labels** — `getByRole`, `getByLabel`,
     `getByText`. Preferred when the element has a natural accessible
     name; doubles as an accessibility check.
  2. **`data-testid`** — `getByTestId('loading-spinner')`. Use for
     structural / state elements that lack a meaningful accessible
     name (loading states, layout containers, backdrops).
  3. **CSS class selectors** — `.oc-foo` — last resort only. Fragile
     across refactors and styling changes.
  When fixing a broken e2e test, replace the CSS-class locator with
  an ARIA or `data-testid` locator rather than patching the class
  name. New components should add `data-testid` attributes for any
  element that e2e tests need to target.

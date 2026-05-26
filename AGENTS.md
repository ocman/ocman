# AGENTS.md

## What is ocman

A web dashboard for viewing coding-agent session data. Ocman supports:

- **OpenCode** — reads OpenCode's SQLite database (read-only) and
  proxies live data from running OpenCode instances via their HTTP
  API.

Platforms are wired through a common `Platform` adapter interface
(`internal/platforms/`). Adding a new platform (e.g. Codex) is a
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
- `internal/db/` — read-only SQLite queries against OpenCode's
  `session`, `message`, `part` tables; uses `json_extract` heavily.
- `internal/state/` — writable SQLite database
  (`~/.local/share/ocman/state.db`) for ocman's own state (archived
  / seen sessions). Primary key is `(platform, session_id)` so it
  can scope state per platform.
- `internal/server/` — HTTP server, API handlers, static file serving
  with SPA fallback, OpenCode port discovery via `lsof`, tmux
  integration, whisper transcription.
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
make build            # production: pnpm install + pnpm build, then go build -o ocman .
make clean            # removes ocman binary, tmp/, and static/assets/
make otel-up          # start Grafana LGTM stack (Loki/Tempo/Mimir + OTLP) at :3000/:4317/:4318
make otel-down        # stop the LGTM stack
make otel-logs        # tail LGTM container logs
make otel-reset       # stop + wipe persisted telemetry data
```

- `mise` provides `air` (Go live-reload), `node`, and `pnpm`. Run
  `mise install` if any of them are missing. **Use `pnpm`** for all
  Node-side commands — `npm` is no longer the supported package
  manager. The version is pinned via the `packageManager` field in
  `frontend/package.json` and via `mise.toml`.
- Both dev and dev-prod modes use port **8228** for frontend, **8229** for backend.
- The frontend (Vite dev or preview) proxies `/api` requests to `localhost:8229`.
- Air rebuilds Go on source changes but does **not** re-embed the
  frontend bundle. After editing frontend code, either (a) use the
  Vite dev server on :8228 instead of the embedded build, or (b)
  run `cd frontend && pnpm build` and touch a `.go` file to
  trigger Air.

## Repository hosting

This repository is self-hosted on **Forgejo** (not GitHub). Use the
`tea` CLI for all issue and pull-request operations — `gh` is not
configured for this remote.

```sh
# Issues
tea issues ls                          # list open issues
tea issues create -t "title" -d "body" # open a new issue
tea issues close <id>                  # close an issue

# Pull requests
tea pulls ls                           # list open PRs
tea pulls create -t "title" -d "body"  # open a PR (branch must be pushed first)
tea pulls merge <id>                   # merge a PR

# Always pass --repo dries/ocman when running outside the repo root,
# or when the remote is not auto-detected.
```

## Build pipeline

1. `cd frontend && pnpm install --frozen-lockfile && pnpm build` —
   builds frontend into `internal/server/static/`.
2. `go build -o ocman .` — embeds `internal/server/static/` via
   `//go:embed`.

Order matters: frontend must be built before `go build` so static
assets are embedded.

## Verification

CI runs these checks (`.github/workflows/ci.yml`):

```sh
cd frontend && pnpm lint          # ESLint
cd frontend && pnpm exec tsc -b   # TypeScript typecheck
cd frontend && pnpm test          # vitest (81 tests)
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
  (default `"opencode"`). Currently the only valid value is `opencode`.
  Only the listed adapters are registered; the OpenCode database is
  not required when `opencode` is omitted from the list.
- **Pure-Go SQLite**: uses `modernc.org/sqlite` (no CGo, no C compiler required).
- **Two databases**: OpenCode's DB is opened read-only
  (`?mode=ro&_journal_mode=WAL`, default `~/.local/share/opencode/opencode.db`).
  Ocman's own state DB is writable (`~/.local/share/ocman/state.db`),
  auto-creates its schema, and runs a versioned migration on startup.
- **OpenCode port discovery** uses `lsof` to find processes named
  `opencode` listening on TCP, then resolves their cwd. macOS/Linux
  only. Cached with a 3-second TTL.
- **Session status** for OpenCode is inferred at query time from the
  last message's `role`, `finish`, and `error` fields.
- **Auto-archive**: background goroutine archives sessions inactive
  for 3+ days (checked every 24 h). Runs against all registered
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
  (OpenCode). *Agent* = a composer-level role within a session
  (OpenCode's `build` / `plan` / user-defined subagent).
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

<!-- gitnexus:start -->
# GitNexus — Code Intelligence

This project is indexed by GitNexus as **ocman** (10134 symbols, 21261 relationships, 300 execution flows). Use the GitNexus MCP tools to understand code, assess impact, and navigate safely.

> If any GitNexus tool warns the index is stale, run `npx gitnexus analyze` in terminal first.

## Always Do

- **MUST run impact analysis before editing any symbol.** Before modifying a function, class, or method, run `gitnexus_impact({target: "symbolName", direction: "upstream"})` and report the blast radius (direct callers, affected processes, risk level) to the user.
- **MUST run `gitnexus_detect_changes()` before committing** to verify your changes only affect expected symbols and execution flows.
- **MUST warn the user** if impact analysis returns HIGH or CRITICAL risk before proceeding with edits.
- When exploring unfamiliar code, use `gitnexus_query({query: "concept"})` to find execution flows instead of grepping. It returns process-grouped results ranked by relevance.
- When you need full context on a specific symbol — callers, callees, which execution flows it participates in — use `gitnexus_context({name: "symbolName"})`.

## Never Do

- NEVER edit a function, class, or method without first running `gitnexus_impact` on it.
- NEVER ignore HIGH or CRITICAL risk warnings from impact analysis.
- NEVER rename symbols with find-and-replace — use `gitnexus_rename` which understands the call graph.
- NEVER commit changes without running `gitnexus_detect_changes()` to check affected scope.

## Resources

| Resource | Use for |
|----------|---------|
| `gitnexus://repo/ocman/context` | Codebase overview, check index freshness |
| `gitnexus://repo/ocman/clusters` | All functional areas |
| `gitnexus://repo/ocman/processes` | All execution flows |
| `gitnexus://repo/ocman/process/{name}` | Step-by-step execution trace |

## CLI

| Task | Read this skill file |
|------|---------------------|
| Understand architecture / "How does X work?" | `.claude/skills/gitnexus/gitnexus-exploring/SKILL.md` |
| Blast radius / "What breaks if I change X?" | `.claude/skills/gitnexus/gitnexus-impact-analysis/SKILL.md` |
| Trace bugs / "Why is X failing?" | `.claude/skills/gitnexus/gitnexus-debugging/SKILL.md` |
| Rename / extract / split / refactor | `.claude/skills/gitnexus/gitnexus-refactoring/SKILL.md` |
| Tools, resources, schema reference | `.claude/skills/gitnexus/gitnexus-guide/SKILL.md` |
| Index, status, clean, wiki CLI commands | `.claude/skills/gitnexus/gitnexus-cli/SKILL.md` |

<!-- gitnexus:end -->

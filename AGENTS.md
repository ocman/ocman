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
`git worktree add` under `<repo-parent>/.worktrees/<repo>/<slug>/`, then
runs the session in-app: ocman keeps **one** `opencode` instance per
project (rooted at the main checkout, launched idempotently via
`EnsureProjectOpencode`) and creates the worktree (and same-directory)
sessions on that instance with a per-session working directory. There is
no per-worktree tmux window; a single project instance serves every
worktree, so parallel sessions still get isolated files, rebuilds, and
staging area without spawning an opencode/tmux process each. When
`EnsureProjectOpencode` launches the instance it seeds the pane with a
scoped `external_directory` `OPENCODE_PERMISSION` rule for the project's
`.worktrees/<repo>` root so worktree paths are pre-approved.
**Limitation:** an opencode instance that was *already running* before
the /wt launch was not seeded with that rule; the runtime autoapprove
pipeline covers the gap for those pre-existing instances.

A worktree/child session launched from a parent also **inherits the
parent's accumulated "Allow always" permissions AND its live permission
posture** at split time (#101): ocman records every user-clicked "Allow
always" reply (plus judge-safe approvals) in `auto_approved_permission`,
and `permissions.BuildInheritedRulesWithLive` replays them into the
child's per-session ruleset via `LaunchRequest.PermissionRules` /
`SetPermissionRules`. Crucially it *also* reads the parent's current
ruleset (`Platform.PermissionRules`) and merges it last (so a live rule
wins on conflict) — this is what propagates a **YOLO / custom
permission mode**, which is written straight to the session ruleset and
never recorded as an approval. Without this a YOLO parent's children
would launch under default permissions and get stuck on prompts.
Controlled by the default-on `worktree.inherit_permissions` setting
(GET/POST `/api/settings/worktree-inherit-permissions`); snapshot at
split time, soft-fail (never blocks a launch).

Pending permission/question prompts from **subagent sessions** — even
deeply nested ones, and ones launched *outside* ocman's control (native
OpenCode Task subagents) — bubble up to the nearest visible top-level
session row so the user can see and answer them.
`db.GetSessionParentIDs` resolves each prompted session to its
**top-level ancestor** (recursive walk over `session.parent_id`), not
just its immediate parent, so a prompt on a grandchild subagent still
surfaces on the row the user is watching.

Ocman also embeds an **MCP (Model Context Protocol) server** at
`http://localhost:8229/mcp` (Go backend) / `http://localhost:8228/mcp`
(via Vite dev proxy). This lets AI coding agents (and users via
the agent) split work from an active session into new parallel sessions
or isolated git worktrees. See the **MCP server** section below for
setup and available tools.

Ocman also surfaces **PRs and Issues** from the active project's
upstream forge (GitHub or Forgejo) in a sidebar pane next to Session
Info / Session Changes / Working Tree. The pane only appears when at
least one supported remote is detected (a `github.com` remote or a
Forgejo host present in `~/.config/tea/config.yml`). Auth uses env-var
tokens (`GITHUB_TOKEN`, `FORGEJO_TOKEN` / `GITEA_TOKEN`) with a fallback
to the `gh auth token` / `tea login` configuration. Clicking a row
expands it to show the body (markdown-rendered) and a split-button:
default action launches a new OpenCode session in the project
directory; the menu offers "new worktree" instead, which checks out
the PR's source branch into a fresh worktree (or fetches the PR head
ref into `ocman/pr-<n>` for cross-fork PRs after explicit
confirmation). The prompt sent to the new session is rendered from a
user-customizable template under Settings → "PR & Issue templates",
persisted in the `setting` table of `state.db` (migration v12). See
`spec/pr-issue-sidebar/` for the full spec.

Ocman also supports **multi-remote**: one "hub" ocman attaches to other
ocman instances over a long-lived gRPC channel and manages every
machine's sessions from one unified, host-agnostic UI. A remote opts in
by starting with `-remote-listen <addr>` (off by default → NFR-6); each
instance has a stable random instance ID + remote-access token persisted
in `state.db` (migration v14), revealed from its own Settings → Remotes
page. The hub dials each saved remote (token auth, optional TLS via
`-remote-tls-cert`/`-remote-tls-key` or a `grpcs://` address), registering
one `remotePlatform` adapter (compound platform id `r-<remoteID>:opencode`,
AD-2) and one `remoteHost` per connected remote. Two adapter seams keep
this transparent: session-scoped work goes through `platforms.Platform` +
`Registry`, directory-scoped work (git/worktree/tmux/projects) through the
new `hostsvc.Host` + `hostsvc.Router` (`ForRemote`/`ForDir`) — handlers
resolve an owner and delegate, so the HTTP layer is unchanged. Host-local
actions (tmux, worktrees) execute on the owning host. The browser still
talks REST/SSE to the hub only; the hub re-emits remote gRPC event streams
as SSE. New-session creation is machine-aware via
`POST /api/sessions/resolve-targets` + a frontend machine picker. The
frontend stays host-agnostic (host badge + capability flags, no
remote-identity branching; `scripts/check-host-helpers.sh` enforces that
handlers don't bypass the `Host` seam). User-facing docs:
`docs/multi-remote.md`. Full design: `spec/multi-remote-support/`.

## Repository layout

- `main.go` — entrypoint; parses `-addr`, `-db`, and `-platforms`
  flags, opens databases, registers platform adapters, starts the
  server.
- `internal/platforms/` — `Platform` interface, `Registry`, common
  types/errors.
- `internal/platforms/opencode/` — OpenCode adapter wrapping the DB
  + HTTP proxy client.
- `internal/sessionsvc/` — session mutation service (validation,
  adapter selection, side-effect hooks). REST handlers, MCP tools,
  and the remote gRPC server all delegate session mutations to it
  (one shared mutation path).
- `internal/queuesvc/` — follow-up message queue (#58). Composer
  sends made while a session is mid-turn are enqueued in `state.db`
  (shared across every client, survives a client moving machines) and
  drained one-per-turn on the `session.idle` edge. Enqueue is
  unconditional; flush is the sole send gate (serialized per session)
  so there's no check-then-act race. The idle-edge flush trusts the edge
  and does **not** re-check the (lagging) inferred status, so a genuine
  turn-end never leaves the head stranded. A periodic `Sweep`
  (`runQueueSweep`, 15 s) is the backstop: it drains one message from each
  idle session with a standing backlog, self-healing rows that never got
  an idle edge. Wired in `internal/server/queue.go`.
- `internal/db/` — read-only SQLite queries against OpenCode's
  `session`, `message`, `part` tables; uses `json_extract` heavily.
- `internal/state/` — writable SQLite database
  (`~/.local/share/ocman/state.db`) for ocman's own state (archived
  / seen sessions). Primary key is `(platform, session_id)` so it
  can scope state per platform.
- `internal/mcp/` — MCP server implementation. `PromptComposer`
  enriches caller-provided intent with session context; `SessionLauncher`
  creates child sessions via the Platform interface; tool handlers
  implement the MCP tools. Mounted at `/mcp` by the server package.
- `internal/server/` — HTTP server, API handlers, static file serving
  with SPA fallback, OpenCode port discovery via `lsof`.
- `internal/tmux/` — tmux process control: session/window listing,
  name derivation/validation, opencode launchers (runner seams for
  tests). HTTP handlers stay in `internal/server`.
- `internal/term/` — in-app browser terminals: window naming/hashing,
  window management in the dedicated `ocman-term` tmux session, and
  the PTY bridge. WebSocket/REST layer stays in `internal/server`.
- `internal/whisper/` — self-contained voice transcription via a local
  whisper-cpp binary (+ ffmpeg conversion).
- `internal/autoapprove/` — the LLM-judged permission auto-approve
  pipeline (judge, per-permission state machine, safe-command cache,
  SSE tee/sinks, headless watcher). Wired into the server through an
   `autoapprove.Deps` seam by `internal/server/autoapprove_engine.go`.
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
- **OpenTelemetry (optional)**: `--otel=<endpoint>` /
  `OTEL_EXPORTER_OTLP_ENDPOINT` ships traces + metrics to an OTLP
  collector; empty = no-op (zero overhead). Implementation in
  `internal/telemetry`: `otelhttp` on the mux + outbound clients,
  `otelsql` on both SQLite handles, custom spans/metrics around the
  auto-archive loop, projects-index refresh, SSE streams, and
  `srvtiming` boundaries; a logrus hook stamps `trace_id`/`span_id`.
  `make otel-up` runs the bundled Grafana LGTM stack and the `make dev*`
  targets auto-export the dev endpoint. User-facing config (URL scheme →
  transport, `OTEL_*` vars, dashboard) is in `docs/configuration.md`;
  dashboard provisioning in `observability/`.
- **Tmux session name character limitations**: `tmux.SessionNameForPath`
  derives a session name from the worktree directory. tmux itself
  replaces dots with underscores when displaying session names, so a
  path like `/home/u/src/github.com/foo` becomes the session name
  `~/src/github_com/foo` in `tmux list-sessions` output. Two
  character sets are enforced in `internal/tmux/sessions.go`:
  - `tmux.ValidName` (`[a-zA-Z0-9._/~:-]+`) — used for user-supplied
    target identifiers such as `session:window` pairs.
  - `tmux.ValidComponent` (`[a-zA-Z0-9._/~-]+`) — used for names
    *derived from filesystem paths* (session names, window names).
    The colon is **excluded** because tmux uses `:` as the
    session/window separator in target identifiers; an embedded `:`
    would silently mis-target the wrong pane.
  If a derived session name contains any character outside
  `tmux.ValidComponent`, `tmux.LaunchOpencodeWith` /
  `tmux.LaunchOpencodeEnvWith` return an error. In practice this is
  rare: only atypical *project* directory names (e.g. those containing
  `:` or spaces) can trigger it.
- **Optional password auth** (`internal/server/auth.go`): off by
  default (binds `127.0.0.1:8228`, unauthenticated). When configured it
  applies to every client including localhost; password is
  bcrypt-hashed, cookies are HMAC-signed (stateless) with a key in
  `state.db`'s `auth_secret` table, logins rate-limited 5/min/IP. The
  precedence (`OCMAN_AUTH_PASSWORD` > `-auth-password-file` >
  `-auth-password`), the `-auth-trust-localhost` escape hatch, and full
  setup are documented in `docs/configuration.md`.

## MCP server

Ocman embeds a localhost-only MCP server (`internal/mcp/`, mounted at
`/mcp` by the server package) exposing session-split + parent/child
message tools (`new_session`,
`get_current_session_id`, `get_session_status`, `list_child_sessions`,
`cancel_session`, `send_message_to_child`, `send_message_to_parent`).

Implementation notes:

- `PromptComposer` enriches caller intent with parent-session context
  (last 10 messages, git branch, `git diff --stat`); `SessionLauncher`
  creates the child via the `Platform` interface.
- Child session records live in `state.db`'s `child_sessions` table
  (migration v9); a background watcher polls every 5 s and injects a
  result summary back into the parent on completion.
- `new_session` seeds the child with the parent's accumulated
  "Allow always" permissions when `worktree.inherit_permissions` is on
  (#101), and reports `permissionsInherited` / `permissionsInheritedCount`
  (and `permissionsInheritError` on a soft failure) in its result.
- Agent splitting *policy* lives in
  `.opencode/skills/ocman-session-splitting/SKILL.md` so MCP tool
  descriptions stay short and action-focused.

User-facing setup, the full tool table, and the splitting workflow are
documented in `docs/mcp.md`.

## Architecture doc

`docs/architecture.md` holds the Mermaid architecture diagrams (system
context, backend composition, session/event data flow, frontend
composition). When a change alters what those diagrams show — a new or
merged `internal/` package, a new external dependency (database, forge,
protocol), a new seam like `platforms.Platform`/`hostsvc.Host`, or a
change to the browser↔backend data flow — update the affected diagram
and its bullet list in the same PR. Keep diagrams at ~10 blocks; push
detail into the prose below each one.

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
  enforcement. Privileged host-control routes additionally use
  `requireLocalhost`, which checks both the loopback peer and browser origin.
- Frontend state management uses Zustand. Routing uses
  react-router-dom.
- Tests live alongside code as `*_test.go` / `*.test.ts(x)`. Prefer
  table-driven tests in Go. The server package has a shared
  `fakePlatform` for integration tests.
- **Test coverage must not drop.** CI runs a coverage ratchet
  (`make coverage` / `make coverage-check`, see
  `spec/ci-coverage-ratchet/`) that fails any PR lowering Go or
  frontend total line coverage beyond a 0.1% slack. Treat this as a
  hard requirement, not a suggestion:
  - **New code ships with tests.** Every new function, branch, loop,
    parser, or money/security path gets a test in the same PR. Bug
    fixes get a regression test. Don't rely on existing tests to cover
    new behaviour.
  - **Prove bugs red/green.** When fixing a bug, first write a test
    that reproduces it and *fails* on the unpatched code (red), then
    apply the fix so it *passes* (green). State in the PR that you saw
    it fail before the fix. A fix without a failing-first test is
    incomplete.
  - **Verify before committing.** Run `make coverage-check
    BASELINE_DIR=<baseline>` (or at minimum `go test -cover ./...`
    and `cd frontend && pnpm test -- --coverage`) and confirm the
    delta is ≥ 0 for the side you touched. If coverage drops, add
    tests until it doesn't — do not lower the baseline.
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

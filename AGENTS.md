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
project (rooted at the main checkout, ensured via
`EnsureProjectOpencode`) and creates the worktree (and same-directory)
sessions on that instance with a per-session working directory. There is
no per-worktree tmux window; a single project instance serves every
worktree, so parallel sessions still get isolated files, rebuilds, and
staging area without spawning an opencode/tmux process each.

The managed instance lifecycle is **runtime-neutral** (#376,
`internal/ocruntime`): an `ocruntime.Runtime` interface
(`Launch`/`Probe`/`Stop`) abstracts how the instance is hosted, with a
native-tmux implementation today and a container runtime planned (epic
#375). `EnsureProjectOpencode` (on `hostsvc.Host`, owner-routed via
`Router.ForDir`) is the only path that launches opencode for a project.
It **allocates a free loopback port itself**
(`ocruntime.AllocateLoopbackPort`, bind→close) and launches
`opencode --port N` — the managed path no longer relies on `lsof` port
discovery. Health is an **API probe**: `GET {endpoint}/config` returning
200, not mere listener presence. Concurrent ensure calls for one repo
root collapse via `singleflight` (at-most-one launch); the call probes
the current instance and reuses it when healthy, else relaunches.
`EnsureProjectOpencodeResult` exposes the full `Endpoint` URL (plus a
`Port()` accessor), the resolved `RepoRoot`, the opaque
`ocruntime.Instance` (whose `ID` is the tmux session name, kept for
observability), and `Launched`. Each managed instance is persisted in
`state.db`'s `managed_opencode` table (migration v35) keyed by canonical
repo root, so it survives an ocman restart; on recovery a persisted row
is **always re-probed** before being trusted — a dead row is discarded
and the project relaunches. `RestartProjectOpencode` (same owner
routing, works local + remote over the gRPC seam) stops the tracked
instance and relaunches under the same `singleflight` key.

Permission approval provenance is durable: AI and user approvals, including
one-time user approvals, are recorded for command footnotes with the original
permission metadata. Only user `always` decisions and judge-safe approvals feed
child permission inheritance; user `once` decisions remain audit-only.

When `EnsureProjectOpencode` launches the instance it seeds the pane with
a scoped `external_directory` `OPENCODE_PERMISSION` rule for the
project's `.worktrees/<repo>` root so worktree paths are pre-approved.
**Limitation:** an opencode instance that was *already running* before
the /wt launch was not seeded with that rule; the runtime autoapprove
pipeline covers the gap for those pre-existing instances.

A worktree session launched from a parent also **inherits the parent's
accumulated "Allow always" permissions AND its live permission posture**
at split time (#101): ocman records every user-clicked "Allow always"
reply (plus judge-safe approvals) in `auto_approved_permission`, and
`permissions.BuildInheritedRulesWithLive` replays them into the child's
per-session ruleset via `LaunchRequest.PermissionRules` /
`SetPermissionRules`. Crucially it *also* reads the parent's current
ruleset (`Platform.PermissionRules`) and merges it last (so a live rule
wins on conflict) — this is what propagates a **YOLO / custom permission
mode**, which is written straight to the session ruleset and never
recorded as an approval. Without this a YOLO parent's children would
launch under default permissions and get stuck on prompts. Controlled by
the default-on `worktree.inherit_permissions` setting (GET/POST
`/api/settings/worktree-inherit-permissions`); snapshot at split time,
soft-fail (never blocks a launch).

Pending permission/question prompts from **subagent sessions** — even
deeply nested ones, and ones launched *outside* ocman's control (native
OpenCode Task subagents) — bubble up to the nearest visible top-level
session row so the user can see and answer them.
`db.GetSessionParentIDs` resolves each prompted session to its
**top-level ancestor** (recursive walk over `session.parent_id`), not
just its immediate parent, so a prompt on a grandchild subagent still
surfaces on the row the user is watching.

Ocman also embeds an **MCP (Model Context Protocol) server** on its own
loopback-only listener, `http://127.0.0.1:8227/mcp` (`-mcp-addr`), plus
the same endpoint on the web UI's port (`:8229`, or `:8228` via the Vite
dev proxy). This exposes workflow control tools and `embed_file` for
displaying generated assets in the UI. See the **MCP server** section
below for setup and available tools.

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

PR/Issue backend operations are owner-routed. Every project forge endpoint
accepts `remoteId` (`local` by default); explicit remotes fail closed when
disconnected. Repository detection and cross-fork fetches run through the
owner's `hostsvc.Host`, while the hub uses its forge clients for PR/Issue
metadata and launches sessions on the owner's compound platform.
The frontend carries that owner through upstream lists, checks, forge-user
identity, git branch highlighting, and optimistic session seeding. Successful
forge-user identities survive PR/Issue tab remounts; failures remain retryable.

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
new `hostsvc.Host` + `hostsvc.Router` (`LookupRemote`/`ForDir`) — handlers
resolve an owner and delegate, so the HTTP layer is unchanged. An
explicitly client-supplied `remoteId` is always resolved through
`Server.resolveOwner`, which fails closed with 503 when that remote is not
registered; only *inferred* ownership (`ForDir`) may degrade to the hub.
An instance ID must be unique across connected remotes: the compound
platform id and the router key are both derived from it, so a second
remote claiming a live ID would rebind the first's sessions and host
actions to the wrong machine. `publishAdapters` refuses it, publishes
nothing, and marks that connection `duplicate-identity` so Settings shows
why it never came up (the usual cause is a cloned `state.db`).
Host-local
actions (tmux, worktrees) execute on the owning host. The browser still
talks REST/SSE to the hub only; the hub re-emits remote gRPC event streams
as SSE. New-session creation is machine-aware via
`POST /api/sessions/resolve-targets` + a frontend machine picker. The
frontend stays host-agnostic (host badge + capability flags, no
remote-identity branching; `scripts/check-host-helpers.sh` enforces that
handlers don't bypass the `Host` seam). User-facing docs:
`docs/features/multi-remote.md`. Full design: `spec/multi-remote-support/`.

## Repository layout

- `main.go` — entrypoint; parses the CLI flags (run `ocman -h`, or read
  the `flag.` block in `main.go` for the authoritative list), opens
  databases, registers platform adapters, starts the server. Note
  `-gui` / `-gui-addr`: with `-gui` the process opens a native Wails
  desktop window (`internal/gui`) around the same HTTP server instead
  of only serving HTTP.
- `internal/platforms/` — `Platform` interface, `Registry`, common
  types/errors.
- `internal/platforms/opencode/` — OpenCode adapter wrapping the DB
  + HTTP proxy client.
- `internal/sessionsvc/` — session mutation service (validation,
  adapter selection, side-effect hooks). REST handlers and the remote
  gRPC server delegate session mutations to it
  (one shared mutation path).
- `internal/queuesvc/` — follow-up message queue (#58). Queueing is an
  **explicit user gesture**: plain **Enter** in the composer sends
  immediately (mid-turn included — OpenCode interleaves the prompt into
  the running turn), while **Ctrl/Cmd+Enter** holds it in `state.db`
  (shared across every client, survives a client moving machines) to be
  drained one-per-turn on the `session.idle` edge. The `queue` flag on
  `POST /api/session/{id}/message` selects the path; when it is false the
  handler calls `Server.sendNow` and never touches the queue. The flag is
  never derived from inferred status, which lags the SSE stream. Held
  messages are drained only by flush (serialized by a drainable worker), so
  there's no check-then-act race. The idle-edge flush trusts the edge and does
  **not** re-check the (lagging) inferred status, so a genuine turn-end
  never leaves the head stranded. A periodic one-minute `Sweep`
  (`runQueueSweep`) is only a recovery backstop: it drains one message from
  each idle session with a standing backlog after a missed edge or crash.
  Scheduled prompts enqueue the same way. Wired in `internal/server/queue.go`.
- `internal/db/` — read-only SQLite queries against OpenCode's
  `session`, `message`, `part` tables; uses `json_extract` heavily.
- `internal/state/` — writable SQLite database
  (`~/.local/share/ocman/state.db`) for ocman's own state (archived
  / seen sessions). Primary key is `(platform, session_id)` so it
  can scope state per platform.
- `internal/mcp/` — MCP server implementation. Tool handlers implement
  workflow control and `embed_file`. Mounted at `/mcp` by the server
  package.
- `internal/server/` — HTTP server, API handlers, static file serving
  with SPA fallback, OpenCode port discovery via `lsof`.
- `internal/tmux/` — tmux process control: session/window listing,
  name derivation/validation, opencode launchers (runner seams for
  tests). HTTP handlers stay in `internal/server`.
- `internal/ocruntime/` — runtime-neutral managed-opencode lifecycle
  (#376). A `Runtime` interface (`Launch`/`Probe`/`Stop`) abstracts how a
  project's opencode instance is hosted; the native-tmux implementation
  runs `opencode --port N` on an ocman-allocated loopback port and probes
  `GET {endpoint}/config` for health. Plug point for the container
  runtime (epic #375). Driven by `hostsvc/local`'s
  `EnsureProjectOpencode` / `RestartProjectOpencode`, which persist each
  instance in `state.db`'s `managed_opencode` table (v35, probe-on-
  recovery). Launch capability is surfaced as the `HostCaps.OpencodeLaunch`
  (`opencodeLaunch`) flag, distinct from `Tmux`; the frontend gates
  managed-launch UI on it.
- `internal/term/` — in-app browser terminals: window naming/hashing,
  window management in the dedicated `ocman-term` tmux session, and
  the PTY bridge. WebSocket/REST layer stays in `internal/server`.
- `internal/whisper/` — self-contained voice transcription via a local
  whisper-cpp binary (+ ffmpeg conversion).
- `internal/autoapprove/` — the LLM-judged permission auto-approve
  pipeline (judge, per-permission state machine, safe-command cache,
  SSE tee/sinks, headless watcher). Wired into the server through an
   `autoapprove.Deps` seam by `internal/server/autoapprove_engine.go`.
- `internal/hostsvc/` — the directory/host-scoped adapter seam (`Host`,
  `Router`), the directory analogue of `platforms.Platform`.
  `internal/hostsvc/local/` is the only package that reaches for
  git/tmux/whisper directly; handlers resolve an owner and delegate.
- `internal/remote/` — multi-remote gRPC client/server plus the
  generated `proto` stubs (regenerate with `make proto`).
- `internal/gitexec/` — hardened `git` subprocess construction (strips
  `GIT_DIR`/`GIT_INDEX_FILE`, disables terminal prompts and optional
  locks). Every git invocation must go through it.
- `internal/git/` — repository queries built on `gitexec` (branches,
  status, worktrees, diffs).
- `internal/forge/` — GitHub/Forgejo PR + issue clients behind one
  interface; feeds the PRs & Issues sidebar.
- `internal/workflows/` — DAG workflow engine (definitions, scheduling,
  node runs/attempts) on top of `internal/state`. See
  `docs/features/workflows.md`.
- `internal/permissions/` — builds the inherited permission ruleset for
  a worktree session (#101).
- `internal/pricing/` — LiteLLM model-pricing fetch/cache + cost
  calculation for the usage metrics views.
- `internal/ocapi/` — host-local auth shared by every ocman client that
  talks to OpenCode's HTTP API.
- `internal/opencodeskills/` — installs ocman-owned embedded skills into
  OpenCode's global skill directory.
- `internal/opencodeconfig/` — reads/writes the `mcp.ocman` entry in
  OpenCode's global config (`~/.config/opencode/opencode.json`, honouring
  `$XDG_CONFIG_HOME`/`OPENCODE_CONFIG`). Backs the original up to
  `opencode.<timestamp>-backup.json`, writes atomically, and refuses any
  config it can't round-trip losslessly (`.jsonc`, or comments in a
  `.json`) so hand-written files are never mangled. Drives the
  `McpConfigPrompt` toast via `GET /api/mcp/config` +
  `POST /api/mcp/config/install`.
- `internal/srvtiming/` — per-request phase timing rendered into the
  `Server-Timing` response header; no-op outside an HTTP request.
- `internal/toolpath/` — `Ensure()` merges the login shell's PATH into
  the process PATH at startup, so a launchd/login-item start still finds
  homebrew and mise/asdf shims (`tmux`, `opencode`, `git`).
- `internal/share/` — share snapshot format + relay client.
  `ResolveRelayURL` picks the relay endpoint (`-relay-url` flag >
  `OCMAN_RELAY_URL` > `DefaultRelayURL` baked into the build) and fails
  startup on an invalid value rather than silently disabling sharing.
- `internal/telemetry/` — OpenTelemetry wiring (see Key details).
- `internal/gui/` — Wails desktop shell used by `-gui`; wraps the same
  HTTP server in a native WebView window.
- `frontend/` — React + TypeScript + Vite SPA (port 8228 in dev).
- `internal/webui/static/` — Vite build output; embedded into the Go
  binary via `//go:embed`. Gitignored except for `robots.txt`, which
  is kept as a permanent placeholder so `go:embed static/*` always has
   at least one file to embed (avoiding churn from build-hashed assets
   like `index.html`).
- `site/` — Hugo (Hextra theme) marketing/docs site. Holds only the
  landing page, templates and config; **all documentation content lives
  in `docs/`**, mounted read-only via `hugo.toml`. `docs/` mirrors the
  site's five chapters — `introduction/`, `features/`,
  `configuration/`, `faq/`, `other/` — with a `_index.md` per chapter
  and `title:`/`weight:` front matter driving nav order. Serve it with
  `make docs`, build with `make docs-build`.
- `deploy/` — deployment and local-proxy config: `Caddyfile` (HTTPS via
  `tailscale cert`, host from `OCMAN_CADDY_HOST`), `Dockerfile.relay`
  (build context is the repo root), `docker-compose.otel.yml` (the
  `make otel-*` LGTM stack; volume paths point at `../observability/`).

## Dev commands

```sh
make dev              # backend (air :8229) + frontend (vite dev :8228) with HMR
make dev-prod         # backend (air :8229) + frontend (vite preview :8228, manual rebuild)
make dev-prod-watch   # backend (air :8229) + frontend (vite preview :8228, auto-rebuild)
make dev-backend      # air only (Go on :8229)
make dev-frontend     # vite only (React on :8228, proxies /api to :8229)
make dev-remote       # backend with the remote-access gRPC server on :8230
make kill-dev         # kill orphans squatting on 8228/8229/8230

make test             # go test ./... + vitest run
make test-backend     # go test ./...
make test-frontend    # vitest run
make test-race        # go test -race ./internal/...
make test-fuzz        # run every Fuzz* target for 10s
make test-e2e         # build frontend, then Playwright
make test-all-fast    # backend + frontend + e2e in parallel, fail fast
make coverage         # collect coverage/*.json (SUITE=go|frontend|all)
make coverage-check   # ratchet coverage/*.json against $(BASELINE_DIR)

make lint             # go vet, golangci-lint, tsc -b, eslint + the three guards
make build            # production: pnpm install + pnpm build, then go build -o ocman .
make build-desktop    # Wails desktop app (the -gui path) into build/bin/
make proto            # regenerate internal/remote/proto stubs (needs protoc)
make install-hooks    # pre-commit + pre-push hooks
make clean            # removes ocman binary, tmp/, and static/assets/

make docs             # Hugo docs/marketing site with live reload (:1313, DOCS_HOST/DOCS_BIND/DOCS_PORT)
make docs-build       # static site into site/public

make otel-up          # start Grafana LGTM stack (Loki/Tempo/Mimir + OTLP) at :3000/:4317/:4318
make otel-down        # stop the LGTM stack
make otel-logs        # tail LGTM container logs
make otel-reset       # stop + wipe persisted telemetry data
```

`make help` lists every target carrying a `## ` doc comment; the
Makefile is the authoritative list.

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
   builds frontend into `internal/webui/static/`.
2. `go build -o ocman .` — embeds `internal/webui/static/` via
   `//go:embed`.

Order matters: frontend must be built before `go build` so static
assets are embedded.

## Verification

CI is defined in `.github/workflows/ci.yml`. **It runs on Forgejo
Actions**, which reads `.github/workflows/` as a fallback — there is
deliberately no `.forgejo/` or `.gitea/` directory, and the workflow is
Forgejo-aware (see the pnpm-install comment near the top and the
`FORGEJO_API_URL: ${{ github.api_url }}` env). Do not "fix" it by
moving files out of `.github/`.

Jobs and the checks they run:

```sh
# Frontend job
cd frontend && pnpm lint            # ESLint
cd frontend && pnpm exec tsc -b     # TypeScript typecheck
./scripts/check-platform-branching.sh  # AD-12a: no `platform === '...'`
./scripts/check-settings-rows.sh       # no hand-rolled settings-row markup
make coverage SUITE=frontend        # vitest with coverage
make coverage-check SUITE=frontend  # coverage ratchet vs the gh-pages baseline

# Backend job
go vet ./...
golangci-lint run                   # config in .golangci.yml (pinned version)
go test .                           # root package
make coverage SUITE=go              # internal/... with coverage
make coverage-check SUITE=go        # coverage ratchet

# Other jobs
pnpm test:e2e                       # Playwright e2e (chromium)
make build                          # full production build (frontend + Go)
```

Locally, `make test` and `make lint` cover the same ground.
`make lint` runs `go vet`, `golangci-lint run`, `tsc -b`, `pnpm lint`,
and the three guard scripts (platform-branching, host-helpers,
settings-rows). Install `golangci-lint` locally — CI enforces it and
`.golangci.yml` enables linters beyond the standard set. Keep diffs
minimal and match the surrounding code.

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
  only. Cached with a 10-second TTL.
- **Session reads go through one snapshot**
  (`internal/platforms/opencode/models_cache.go`). The full aggregate
  query in `db.GetSessions` is expensive (~4.3 s on a 12 GB OpenCode
  DB), so it runs as rarely as correctness allows:
  - There is **one global snapshot**, not one per directory. A
    directory-scoped listing filters that slice in Go; `since` is
    filtered on read for the same reason (a rolling `since` would
    otherwise leak one cache entry per poll). Filtering always copies,
    so read-time overlays can never mutate the snapshot.
  - A **TTL-expired** snapshot is served immediately and revalidated in
    the background; a slow scan must not become endpoint latency. An
    upstream session creation refreshes that exact row before broadcasting
    `ocman.session.changed`, so externally-created sessions appear immediately
    without a full scan when the snapshot is warm. **Explicit invalidation**
    (`InvalidateSessionsCache`) remains the synchronous fallback when the
    snapshot is cold or that row refresh fails.
  - The refresher recomputes **only sessions the event stream marked
    dirty**, via `db.GetSessionSummary`. That single-session read
    reuses the list query's projection and scan so it cannot drift;
    if you change one, you change both.
  - Events are a hint, never the source of truth. An event with no
    resolvable session id marks the whole snapshot dirty, and a full
    reconciliation runs every 5 minutes regardless, so deletions and
    missed events are always corrected.
  - The cache is package-global and assumes **one local OpenCode
    database** per process (see "Two databases" above).
- **Session status** is a closed, typed set — `db.SessionStatus`
  (`busy`, `waiting`, `done`, `error`, `interrupted`), mirrored by the
  exported TS `SessionStatus` union. It is **settled from the agent's own
  turn lifecycle**, not guessed from stored messages (#488). One function
  decides it, `db.SettleSessionStatus(turn, live, inferred)`:
  - The live signal comes from OpenCode itself —
    `GET /session/status` (`{sessionID: {type: "busy"|"retry"|"idle"}}`),
    seeded per instance when the autoapprove watcher connects and kept
    current from `session.status` events on `/global/event`. It lives in
    `internal/platforms/opencode/live_status.go`, keyed by instance port,
    and is dropped wholesale when a port disappears. Nothing is
    persisted: OpenCode owns this state, so a restart re-seeds instead of
    trusting a stale copy.
  - `db.InferSessionStatus` (last message's `role`/`finish`/`error`) is
    demoted to answering one question: *which* terminal state a settled
    session is in. It is never read as "running".
  - No live view + an unfinished turn = `interrupted`: the process that
    owned the turn is gone, so it can never finish.
  Consequently there is no `STATUS_GRACE_MS` debounce and no sticky-busy
  merge in the sidebar — the value no longer lags, so compensating for
  lag would only add staleness. The queue's idle-edge flush still trusts
  the edge, but only for ordering independence, not because the status
  can't be trusted.
- **Auto-archive**: background goroutine archives sessions inactive
  for 7+ days (checked every 24 h). Runs against all registered
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
  transport, `OTEL_*` vars, dashboard) is in `docs/configuration/_index.md`;
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
  setup are documented in `docs/configuration/_index.md`.

## MCP server

Ocman embeds a localhost-only MCP server (`internal/mcp/`, mounted at
`/mcp` by the server package) exposing workflow control tools and
`embed_file`. The authoritative tool list is the table in
[`docs/features/mcp.md`](docs/features/mcp.md#tools) — don't duplicate it here.

Implementation notes:

- Registration is self-service: `McpConfigPrompt` (mounted at the app
  root) polls `GET /api/mcp/config` once per load and offers an Install
  button that POSTs `/api/mcp/config/install`, writing the entry through
  `internal/opencodeconfig` (backup first, refuses non-round-trippable
  configs). OpenCode must be restarted to pick it up.
- Two mounts, one handler (`Server.mcpHandler`, built once): the main mux
  at `/mcp` under `requireLocalhost` (password auth applies), and a
  dedicated loopback-bound listener (`-mcp-addr`, default
  `127.0.0.1:8227`, `startMCPListener`) under `requireLoopbackPeer`,
  which treats the loopback peer as the credential. The second listener
  exists because native MCP clients can't send an auth cookie; binding it
  separately keeps it unreachable through a reverse proxy pointed at
  `-addr`. Non-loopback `-mcp-addr` values are refused (fails closed).

User-facing setup and the full tool table are documented in
`docs/features/mcp.md`.

## Architecture doc

`docs/other/architecture.md` holds the Mermaid architecture diagrams (system
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

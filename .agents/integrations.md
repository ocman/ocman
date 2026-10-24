# Data stores, integrations and security

## Databases and analytics

SQLite uses pure-Go `modernc.org/sqlite`, without CGo. Read
[development.md](development.md) before running code that opens writable stores.

- OpenCode's database defaults to `~/.local/share/opencode/opencode.db`, opened
  read-only with `?mode=ro&_journal_mode=WAL`. The only writer is user-started
  `internal/ocmaint` maintenance. It stops managed instances, blocks launches and
  refuses while another process holds the file. It moves `summary.diffs` older
  than thirty days into `<db>.ocman-diffs` for restoration. Jobs must finish before state.db closes
  and must never leave launches blocked. Maintenance mutation routes are local.
- Ocman's writable state defaults to `~/.local/share/ocman/state.db` and migrates
  on startup. Session state is keyed by `(platform, session_id)`.
- Disposable `analytics-cache.db` beside state.db holds slim session/message rows
  and minimal tool timings. It uses OpenCode table names, falls back to the
  source DB until built, and rebuilds on schema or `-db` changes, not migrations.
  After `AddAnalyticsSource` adds other platforms, never fall back to OpenCode.
  Without OpenCode, `db.OpenAnalyticsStore` is the analytics DB itself.
- Analytics routes, subscription usage and joined workers belong to
  `internal/analytics`; DB maintenance belongs to `ocmaint`. Core keeps mirror
  sync, session snapshots, client demand leases, system stats and HTTP/SSE/index
  instruments. Subscription usage reads local OAuth tokens with a one-minute
  cache; never return tokens to the browser.

Optional OpenTelemetry uses `--otel` or `OTEL_EXPORTER_OTLP_ENDPOINT`; empty is a
no-op. `internal/telemetry` instruments HTTP clients/server, SQLite, archive/index
work, SSE and timing phases. Preserve logrus trace/span IDs and batched OTLP logs
alongside console output. Configuration lives in `docs/configuration/_index.md`,
dashboards in `observability/`. Dev targets export to the local LGTM stack.

## MCP and authentication

The authoritative tool table is `docs/features/mcp.md#tools`. Do not duplicate a
tool inventory in instructions. Feature modules register through `Deps.Tools`.

- Use one `Server.mcpHandler` at both mounts. The web listener's `/mcp` uses
  `requireLocalhost` and password auth. The dedicated default
  `127.0.0.1:8227/mcp` uses `requireLoopbackPeer`, treating the peer as credential
  for native clients without browser cookies. Refuse non-loopback `-mcp-addr`.
- `McpConfigPrompt` polls config once per load and offers installation through
  `internal/opencodeconfig`. Back up first and write atomically. Refuse JSONC or
  commented JSON that cannot round-trip losslessly. Respect XDG and explicit
  config paths. Restart OpenCode to pick up changes.
- Owner-local webhook MCP actions reuse fixed in-process HTTP routes after MCP
  auth. Keep relay registration, predicates and local-routine checks in that
  handler. Omit secrets. Subscription changes do not alter routine schedules or
  enabled state.
- Optional password auth applies even to localhost unless explicitly trusted.
  Hash passwords with bcrypt, sign stateless cookies using state.db's HMAC key,
  and limit login attempts to five/minute/IP. Precedence is
  `OCMAN_AUTH_PASSWORD`, password file, then password flag. See the configuration
  docs for `-auth-trust-localhost` and setup.

## Routines, webhooks and artifacts

- Routines create or reuse managed sessions and remain running until settlement.
  `done` succeeds, `error` fails and a session lost on restart is `interrupted`.
  Missed timeout schedules expire on startup rather than running late. Successful
  runs may soft-delete definitions. `/routines` only inserts a prompt for review.
  Webhook inboxes capture deliveries; routines subscribe with header/JSON-pointer
  filters. See `docs/features/routines.md`.
- Artifacts are immutable project/session files or links. Metadata is in state.db;
  content-addressed SHA-256 blobs live under `<state dir>/artifacts/blobs/`, capped
  at 50 MB per file. Artifacts are local to one instance, never remote-routed.
  Live browsing uses `ocman.artifact.created`. See `docs/features/artifacts.md`.
- Sharing uploads write-once encrypted manifests and chunks. Check relay limits
  before upload. Revoke and artifact deletion remove relay copies. HTML previews
  are inert under sandbox CSP. Opt-in interactive previews grant only
  `allow-scripts` in both CSP and iframe, never `allow-same-origin`; shared views
  stay inert. E2e headers live in `frontend/e2e/artifact-html/headers.json`.
- Relay URL precedence is flag, `OCMAN_RELAY_URL`, then build default. Fail startup
  on invalid URLs, do not silently disable sharing. Sharing listens on idle to
  update session snapshots; encryption/protocol stay in `internal/share`.

## Files, terminals and previews

- `filesystem` owns read-only browsing and signed inert file proxies. Session
  attachments belong to the owner-streamed composer cache; artifact blobs are
  separate. File-signing HMAC keys live in state settings. The hub's directory
  picker/search is localhost-only. Git file reads require an explicit owner and
  fail with 503 for disconnected remotes.
- Browser terminals run on the owner through Host. WebSocket upgrades must fail
  closed for disconnected explicit owners, validate windows against the directory
  and use the privileged-request check for Origin. Each viewer links one target
  into a temporary destroy-on-detach session so shell exit cannot switch it to
  another project. Poll away exited windows and close an empty dock. The terminal
  server is `tmux -L ocman-term`, separate from managed agents. Keep terminal HTTP
  routes localhost-only.
- Tmux listing/launch routes use the owner Host. Client listing and switching
  affect only this machine's TTY. Keep these routes localhost-only.
- Link previews own their routes, grants and lazily reset auth/service state in
  `previews`. Read mutable app access, remote connections and hub forge
  credentials at call time. Keep forge clients server-owned. Fetch bounds,
  caching, rate-limit handling and PR freshness stay in `linkpreview`; credentials
  stay in `previewauth`.

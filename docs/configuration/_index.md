---
title: Configuration
weight: 3
---

## Running ocman

```sh
./ocman                                      # default: listens on 127.0.0.1:8228
./ocman -addr localhost:9090                 # custom listen address
./ocman -db /path/to/opencode.db             # custom OpenCode database path
./ocman -platforms opencode,claude-code      # enable multiple platforms
```

Ocman's own state (archived/seen flags, auth secret, favorites, cached projects) lives in
`~/.local/share/ocman/state.db`, created on first run and migrated automatically. Startup
enforces owner-only permissions, `0700` for the directory and `0600` for the database and
SQLite sidecars, and fails rather than continuing if existing paths cannot be secured.

Before upgrading an existing state database, ocman saves a consistent SQLite backup
beside it, named `state.db.backup-v<old>-to-v<new>-<unique>.db`. The backup includes
committed WAL data and has owner-only permissions (`0600`). One backup covers the
entire migration batch, which runs in a single transaction. Startup logs the backup
path and refuses to migrate if the backup cannot be created or synced to disk.
Fresh databases, in-memory databases, and databases already at the current version
do not need a backup. Backups are retained, including after a failed migration;
remove unneeded copies manually. They cover the state database only, not external
artifact files or OpenCode's database. Ocman never restores a backup automatically.

The HTTP server limits request-header read time and idle keep-alive connections, and bounds how
long a request *body* may take to arrive (30 s for normal API calls, 5 min for uploads). There is
deliberately no global read/write timeout, so SSE streams and in-app terminals keep working.
Ocman size-limits responses it reads from upstream services (OpenCode, GitHub/Forgejo). Browser
responses deny framing and MIME sniffing and carry a Content Security Policy. That policy
permits inline styles, external/data/blob images, blob workers, and WebSocket connections
because the current SPA, attachments, service worker, and terminal need them.

Privileged localhost routes reject cross-origin browser requests. Origin-less local CLI and
MCP clients still work, but once password auth is configured they must present a valid auth
cookie like everyone else. Behind a reverse proxy a loopback peer address is not a credential.
Use `-auth-trust-localhost` to restore the unauthenticated local path. MCP clients cannot send
a cookie, so they use the separate loopback-only listener on `-mcp-addr` (`127.0.0.1:8227` by
default). A proxy pointed at `-addr` cannot reach it, and ocman refuses to bind it to a
non-loopback address. Browser access through a local reverse proxy requires password
authentication and an `OCMAN_PUBLIC_BASE_URL` matching the external origin.

Ocman also checks every request against a Host allowlist before routing, which blocks DNS
rebinding. Allowed hosts are loopback names (`localhost`, `*.localhost`, `127.0.0.1`, `::1`),
bare IP literals, and the host of `OCMAN_PUBLIC_BASE_URL`. Anything else gets
`421 Misdirected Request`. If you reach ocman through a hostname (a tunnel, a Tailscale
MagicDNS name, a reverse proxy), set `OCMAN_PUBLIC_BASE_URL` to that external origin.

## Security model / trust boundary

An unauthenticated loopback deployment trusts every process on the machine. That
includes local agents and other software that can call ocman's privileged session,
worktree, tmux, OpenCode, and MCP actions. Loopback means local reachability, not
user identity. Treat the machine and every process on it as part of the trust
boundary, or configure password authentication.

Auto-approval is not a trust boundary: its judge evaluates session content and
permission metadata that an agent can influence. The judge is deliberately scoped
to the supplied request and cannot read files, run commands, or use tools; it also
uses a per-session safe-command cache and a hard denylist. These are guardrails,
not a replacement for authentication or human review, and the judge denies when
it is uncertain.

When worktree permission inheritance is enabled, child sessions can receive the
parent's approved permissions, custom permission rules, or YOLO permission
posture. Review a parent's permissions before starting child sessions, especially
when the parent or its content is not fully trusted.

Do not treat a same-host reverse proxy as a credential. Requests forwarded by a
local proxy may appear to come from loopback even when the client is remote or
untrusted. Require password authentication in this setup and set
`OCMAN_PUBLIC_BASE_URL` (or `-public-base-url`) to the public origin; proxy
locality must not be used to justify `-auth-trust-localhost`.

## Flags

| Flag | Default | Description |
|------|---------|-------------|
| `-addr` | `127.0.0.1:8228` | Listen address. |
| `-db` | `~/.local/share/opencode/opencode.db` | Path to OpenCode's SQLite DB. Opened read-only. |
| `-log-file` | _(platform default)_ | Also write the log to this file (plain text, dir `0700`, file `0600`). Default `~/Library/Logs/ocman/ocman.log` on macOS, otherwise `$XDG_STATE_HOME/ocman/ocman.log` (`~/.local/state/ocman/ocman.log`). A file over 10 MB at startup moves to `ocman.log.1`. `-` or `off` logs to stderr only. Check this file when the desktop app fails to start. |
| `-gui-boot-timeout` | `10s` | With `-gui`, how long to wait for the backend to answer before showing an error dialog instead of opening the window. In `-gui` mode every startup failure shows a native alert (macOS `osascript`, Linux `zenity` when installed) naming the log file. |
| `-mcp-addr` | `127.0.0.1:8227` | Loopback listen address for the MCP endpoint. Local clients reach it without auth, so non-loopback addresses are refused. Empty disables it. |
| `-platforms` | `opencode` | Comma-separated list of platforms to enable (`opencode`, `claude-code`). |
| `-public-base-url` | _(unset)_ | Externally reachable base URL, e.g. `https://ocman.example.ts.net`. Used for the links ocman hands to readers elsewhere (share links, plugin attention notices) and for cookie/origin decisions. Falls back to the listen address, which only works locally. Also `OCMAN_PUBLIC_BASE_URL`. |
| `-auth-password` | _(unset)_ | Password to require. Prefer `OCMAN_AUTH_PASSWORD` or `-auth-password-file`. |
| `-auth-password-file` | _(unset)_ | Read auth password from file (trailing whitespace trimmed). |
| `-auth-session-ttl` | `720h` (30 days) | Auth cookie lifetime. |
| `-auth-trust-localhost` | `false` | Exempt loopback clients from auth. Also `OCMAN_AUTH_TRUST_LOCALHOST=1`. |
| `-opencode-server-password-file` | _(unset)_ | Read the managed OpenCode API password from a file (trailing whitespace trimmed). |
| `-opencode-server-generate-password` | `false` | Generate an ephemeral managed OpenCode API password at startup. |
| `-remote-listen` | _(unset, off)_ | Bind address for the remote-access gRPC server (multi-remote), e.g. `0.0.0.0:8230`. Empty disables it. |
| `-remote-tls-cert` | _(unset)_ | TLS certificate file for the remote-access gRPC server (enables TLS with `-remote-tls-key`). |
| `-remote-tls-key` | _(unset)_ | TLS key file for the remote-access gRPC server. |
| `-remote-trusted-overlay` | `false` | Explicitly allow plaintext remote gRPC on a trusted overlay network. |
| `-insecure-no-auth` | `false` | Allow a non-loopback `-addr` / `-gui-addr` with no password configured. Also `OCMAN_INSECURE_NO_AUTH=1`. |

## Environment variables

| Variable | Description |
|----------|-------------|
| `OCMAN_AUTH_PASSWORD` | Auth password. Empty string is treated as unset. |
| `OCMAN_PUBLIC_BASE_URL` | Externally reachable base URL (same as `-public-base-url`, which takes precedence). Set it whenever something other than your own browser follows ocman's links — a shared session, or a [conversation plugin](../features/plugins/) posting attention notices into a chat thread. |
| `OCMAN_AUTH_TRUST_LOCALHOST` | Truthy value enables the loopback auth bypass. |
| `OCMAN_INSECURE_NO_AUTH` | Truthy value allows a non-loopback listen address with no password configured. |
| `OPENCODE_SERVER_PASSWORD` | Password for managed OpenCode servers and all ocman-to-OpenCode HTTP/SSE traffic. |
| `OCMAN_SLACK_PREVIEW_CLIENT_ID`, `OCMAN_SLACK_PREVIEW_CLIENT_SECRET` | Enable Slack message previews. Register a dedicated Slack app (not the conversation plugin's) with redirect URL `<public base>/api/previews/oauth/callback` and user token scopes `channels:history`, `channels:read`, `users:read`. Sign in once under Settings → Link previews; private channels and DMs are always shown as denied. |
| `OCMAN_GITHUB_PREVIEW_CLIENT_ID`, `OCMAN_GITHUB_PREVIEW_CLIENT_SECRET` | Optional sign-in app for GitHub previews; a personal token pasted in Settings, or your `gh` login, is simpler. Register a GitHub App with callback URL `<public base>/api/previews/oauth/callback`, expiring user tokens, and read-only Metadata, Pull requests, Issues and Contents permissions. Public links preview without it. |
| `OCMAN_FORGEJO_PREVIEW_APPS` | `host=client_id:client_secret[,host=…]`: one Forgejo OAuth2 application per exact host (https only), with redirect URI `<public base>/api/previews/oauth/callback`. Forgejo OAuth tokens have no granular scopes, so a grant can read and change everything the account can on that host; Settings says so before sign-in. A personal token pasted in Settings is simpler. Listed hosts join the tea-login hosts as previewable. |
| `OCMAN_NOTION_PREVIEW_CLIENT_ID`, `OCMAN_NOTION_PREVIEW_CLIENT_SECRET` | Enable Notion page previews. Create a Notion *public* connection with redirect URI `<public base>/api/previews/oauth/callback` and only the **Read content** capability. Pick the pages to share at sign-in; only those pages preview. An internal integration token pasted in Settings works too. A link rule routed to `notion` looks identifiers such as `ABC-42` up by page title; several matches list the candidates, none leaves the rule's own link. |
| `OCMAN_LINEAR_PREVIEW_CLIENT_ID`, `OCMAN_LINEAR_PREVIEW_CLIENT_SECRET` | Enable Linear issue previews. Create a Linear OAuth2 application with callback URL `<public base>/api/previews/oauth/callback`; ocman requests only the `read` scope with PKCE, so the secret is optional. A personal API key pasted in Settings works without an app. `linear.app/<workspace>/issue/ENG-1` links preview only with a token or sign-in for that workspace, and a link rule routed to `linear` resolves identifiers such as `ENG-1`. |
| `OCMAN_JIRA_PREVIEW_CLIENT_ID`, `OCMAN_JIRA_PREVIEW_CLIENT_SECRET` | Enable Jira Cloud issue previews. Create an Atlassian OAuth 2.0 (3LO) app with callback URL `<public base>/api/previews/oauth/callback` and the `read:jira-work` (Jira API) and `read:me` (User identity API) scopes; ocman also requests `offline_access` for refresh. Sign in once and pick sites at consent. `<site>.atlassian.net/browse/KEY-1` links preview only for a site the grant can reach, always through `api.atlassian.com`; a link rule routed to `jira` looks identifiers up on every connected site and lists the candidates when several match. |
| `OCMAN_GITLAB_PREVIEW_APPS` | `host=client_id[:client_secret][,…]`: one GitLab OAuth application per exact host — `gitlab.com` and/or allowlisted self-managed hosts (https only) — with redirect URI `<public base>/api/previews/oauth/callback` and only the `read_api` scope. Ocman uses authorization code + PKCE, so the secret is optional for a non-confidential app, and refreshes the two-hour access tokens. Merge request, issue and commit links on a listed host preview; public projects on `gitlab.com` preview without setup, others need a token or sign-in for that host (a personal token pasted in Settings needs no app). Unlisted hosts are never contacted, redirects are not followed, and API calls refuse loopback and link-local addresses (and private ones for `gitlab.com`), so a self-managed host on `localhost` is not supported. |
| `OCMAN_ALLOWED_HOSTS` | Vite dev/preview only: comma-separated extra hostnames allowed by the dev server (e.g. `foo.tailnet.ts.net,bar.lan`). |

Link previews mostly need no variables: paste personal tokens under **Settings → Link previews**. The `*_PREVIEW_*` sign-in apps can also be added there; a saved app overrides the environment's for the same provider and host. See [link previews](../features/link-previews/#setup).

### Forge link previews and the machine token

GitHub and Forgejo links (`/owner/repo/pull|pulls|issues|commit/…` on github.com, the https hosts from your `tea` logins, and hosts with a saved token or app) render as preview cards. Resolution, in order:

1. A token or sign-in saved under **Settings → Link previews**.
2. Otherwise the machine's forge token (`GITHUB_TOKEN`/`GH_TOKEN`, `gh auth token`, `FORGEJO_TOKEN`/`GITEA_TOKEN`, `tea` login), for any request with app access, private repositories included.
3. A request without app access previews public repositories only.

## Authentication

By default ocman binds `127.0.0.1:8228` and serves unauthenticated. Any other listen address
(`0.0.0.0:8228`, a bare `:8228`, a LAN IP) requires a password. Ocman refuses to start without
one, because session routes can send messages, run commands, and launch agents. Override that
with `-insecure-no-auth` (or `OCMAN_INSECURE_NO_AUTH=1`) only on a network you control.

To require a password, say when exposing ocman over a tunnel, Tailscale, or any non-loopback
listener, set one of the following (highest precedence first):

1. `OCMAN_AUTH_PASSWORD` env var (preferred)
2. `-auth-password-file /path/to/file`
3. `-auth-password '<plaintext>'` (visible in `ps`; use only for testing)

Once auth is configured it applies to every client, localhost included. For local dev loops,
pass `-auth-trust-localhost` (or `OCMAN_AUTH_TRUST_LOCALHOST=1`) to restore the loopback
bypass.

Ocman bcrypt-hashes the password at startup. Auth cookies are HMAC-signed and stateless, using
a key persisted in `state.db` so logins survive restarts. Login attempts are rate-limited to
5/min per IP, and trusted-localhost clients skip the limiter.

Auth cookies are marked `Secure` when the request arrives over TLS, or when
`OCMAN_PUBLIC_BASE_URL` / `-public-base-url` is an `https://` URL. Behind a TLS-terminating
reverse proxy the request itself looks like plain HTTP, so set the public base URL to the
external `https://` origin and the cookie never travels in cleartext. Ocman deliberately does
not trust the client-supplied `X-Forwarded-Proto` header for this decision.

## OpenCode: enabling interactive features

Sessions launched from ocman (command palette, Worktrees view, PR/Issue sidebar) are interactive
out of the box: ocman manages one OpenCode instance per project on a port it allocates itself.

For OpenCode v1 instances you start yourself, use an explicit port so ocman can discover them
(OpenCode v2 works differently; see [OpenCode v2](#opencode-v2)):

```sh
opencode --port 0   # let OpenCode pick a free port
# or pin a specific port, e.g. opencode --port 4096
```

Ocman finds listening OpenCode processes with `lsof` and connects automatically. Without
`--port`, externally launched sessions are still readable from the database but interactive
features stay disabled.

### OpenCode v2

Ocman supports OpenCode v1 and v2. Each ocman checks `opencode --version` at startup, so one ocman
can run v1 locally while a remote runs v2. Nothing needs configuring. After upgrading or downgrading
OpenCode, restart ocman (it logs a warning when it notices the change).

On a v2 machine, OpenCode runs one server for every project. Ocman follows that:

- **One server per machine.** Ocman starts `opencode serve` on a loopback port it picks and
  keeps it running, relaunching it within 30 seconds if it stops. It runs in a tmux session
  with its own working directory under `~/.local/share/ocman/opencode-v2` (one per database, so
  a different `-db` never reuses a server writing another file). Ocman does not
  use OpenCode's own background service (`opencode service`) or v2 servers you start
  yourself. To use ocman's server from the TUI, run `opencode --server http://127.0.0.1:<port>`.
- **Always authenticated.** v2 requires a password. When none is configured (see below), ocman
  generates one, stores it encrypted in `state.db`, and reuses it across restarts. This keeps
  the server running across an ocman restart.
- **Native follow-up queue.** Ctrl/Cmd+Enter hands the message to v2's own session inbox, which
  delivers it when the turn ends. The composer lists these messages with ocman's own queued
  messages. You can delete them but not reorder them. If ocman's own queue already holds
  messages for the session, a new one joins that queue instead, so the order stays the same.
- **History.** v2 stores conversations in new tables (`session_v2`, `session_message`). Ocman
  reads them through read-only views with the v1 names, so the session list, transcripts,
  search and analytics work unchanged.
- **MCP.** Installing ocman's MCP entry on a v2 machine also sets `"codemode": false`, so the
  agent calls ocman's tools directly instead of through v2's Code Mode `execute` tool.
- **Settings → Maintenance** is not available on v2; it only knows the v1 storage layout.

If you point ocman at a non-default database with `-db`, ocman exports it as `OPENCODE_DB` for the
v2 server it launches, so both use the same file. Without `-db`, an `OPENCODE_DB` you set yourself is
used for both.

### OpenCode server authentication

OpenCode authentication is off by default, so instances started natively without it keep
working. To protect ocman-managed instances, configure one source in this precedence order:

1. `OPENCODE_SERVER_PASSWORD` environment variable
2. `-opencode-server-password-file /path/to/file`
3. `-opencode-server-generate-password`

Ocman injects the selected password as `OPENCODE_SERVER_PASSWORD` when it launches OpenCode and
uses OpenCode's default `opencode` HTTP Basic Auth username for every API and SSE request. The
password stays in backend memory and the managed process environment; it is not stored in
`state.db`, returned to the browser, or included in runtime diagnostics.

To rotate a supplied password, update the environment variable or file and restart ocman, then
restart each managed OpenCode instance from its session action. You can also let the next
managed launch replace an instance whose credential no longer matches. Generated passwords are
deliberately short-lived: every ocman restart creates a new value, and recovered managed
instances are stopped and relaunched on first use. Unmanaged authenticated instances must use
the same configured password. If they don't, ocman reports an authentication failure rather
than an unreachable instance.

## Multi-remote support (optional)

Attach other ocman instances over the network and manage every machine's
sessions from one hub. On a machine you want to manage remotely, start
its gRPC server:

```sh
ocman -remote-listen 0.0.0.0:8230 \
  -remote-tls-cert cert.pem -remote-tls-key key.pem   # secure default
ocman -remote-listen 0.0.0.0:8230 -remote-trusted-overlay # Tailscale/WireGuard only
```

> **Warning:** `-remote-trusted-overlay` sends the bearer token and all session
> traffic without TLS. Use it only when Tailscale, WireGuard, or an equivalent
> encrypted overlay protects the complete route. A private LAN alone is not
> sufficient.

Then attach it from the hub's **Settings → Remotes** page using the
remote's address and access token. This is off by default, so a fresh install
with no remotes is unchanged. See [multi-remote](../features/multi-remote.md)
for the full step-by-step guide, security notes, and troubleshooting.

## OpenTelemetry (optional)

Pass `--otel=<endpoint>` (or set `OTEL_EXPORTER_OTLP_ENDPOINT`) to ship traces and metrics to
an OTLP collector. Empty or unset disables telemetry with zero overhead.

The URL scheme selects the transport:
- `http(s)://...` → OTLP/HTTP
- `grpc(s)://...` or bare `host:port` → OTLP/gRPC

All other configuration uses standard `OTEL_*` env vars (`OTEL_SERVICE_NAME`,
`OTEL_RESOURCE_ATTRIBUTES`, `OTEL_TRACES_SAMPLER`, `OTEL_EXPORTER_OTLP_HEADERS`, etc.).

For local dev, `make otel-up` starts a bundled Grafana LGTM stack on `:3000`, `:4317` and
`:4318`. The `make dev*` targets export `OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4318`
and `OTEL_SERVICE_NAME=ocman-dev` for you. See `observability/` for dashboard provisioning.

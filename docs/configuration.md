# Configuration

## Running ocman

```sh
./ocman                                      # default: listens on 127.0.0.1:8228
./ocman -addr localhost:9090                 # custom listen address
./ocman -db /path/to/opencode.db             # custom OpenCode database path
./ocman -platforms opencode,claude-code      # enable multiple platforms
```

Ocman's own state (archived/seen flags, auth secret, favorites, cached projects) lives in
`~/.local/share/ocman/state.db`, auto-created on first run with automatic schema migration.
Startup enforces owner-only permissions (`0700` for the directory and `0600` for the database
and SQLite sidecars) and fails rather than continuing if existing paths cannot be secured.

The HTTP server limits request-header read time and idle keep-alive connections. Browser
responses deny framing and MIME sniffing and use a Content Security Policy. The policy
intentionally permits inline styles, external/data/blob images, blob workers, and WebSocket
connections because the current SPA, attachments, service worker, and terminal require them.
Privileged localhost routes reject cross-origin browser requests. Origin-less local CLI and
MCP clients remain supported. Browser access through a local reverse proxy requires password
authentication and an `OCMAN_PUBLIC_BASE_URL` matching the external origin.

## Flags

| Flag | Default | Description |
|------|---------|-------------|
| `-addr` | `127.0.0.1:8228` | Listen address. |
| `-db` | `~/.local/share/opencode/opencode.db` | Path to OpenCode's SQLite DB. Opened read-only. |
| `-platforms` | `opencode` | Comma-separated list of platforms to enable (`opencode`, `claude-code`). |
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

## Environment variables

| Variable | Description |
|----------|-------------|
| `OCMAN_AUTH_PASSWORD` | Auth password. Empty string is treated as unset. |
| `OCMAN_AUTH_TRUST_LOCALHOST` | Truthy value enables the loopback auth bypass. |
| `OPENCODE_SERVER_PASSWORD` | Password for managed OpenCode servers and all ocman-to-OpenCode HTTP/SSE traffic. |
| `OCMAN_ALLOWED_HOSTS` | Vite dev/preview only: comma-separated extra hostnames allowed by the dev server (e.g. `foo.tailnet.ts.net,bar.lan`). |

## Authentication

By default ocman binds `127.0.0.1:8228` and serves unauthenticated. To require a password —
for example when exposing over a tunnel, Tailscale, or any non-loopback listener — set one of
the following (precedence in order listed):

1. `OCMAN_AUTH_PASSWORD` env var (preferred)
2. `-auth-password-file /path/to/file`
3. `-auth-password '<plaintext>'` (visible in `ps`; use only for testing)

Once auth is configured it applies to **every** client, including localhost. For local dev
loops, pass `-auth-trust-localhost` (or `OCMAN_AUTH_TRUST_LOCALHOST=1`) to restore the
loopback bypass.

The password is bcrypt-hashed at startup. Auth cookies are HMAC-signed (stateless) with a key
persisted in `state.db` so sessions survive restarts. Login attempts are rate-limited to 5/min
per IP; trusted-localhost clients skip the limiter.

## OpenCode: enabling interactive features

Sessions launched from ocman (command palette, Worktrees view, PR/Issue sidebar) are interactive
out of the box: ocman manages one OpenCode instance per project on a port it allocates itself.

For OpenCode instances you start yourself, use an explicit port so ocman can discover them:

```sh
opencode --port 0   # let OpenCode pick a free port
# or pin a specific port, e.g. opencode --port 4096
```

Ocman discovers listening OpenCode processes via `lsof` and auto-connects. Without `--port`,
externally launched sessions are still readable from the database but interactive features stay
disabled.

### OpenCode server authentication

OpenCode authentication is disabled by default, preserving compatibility with native instances
started without authentication. To protect ocman-managed instances, configure one source in this
precedence order:

1. `OPENCODE_SERVER_PASSWORD` environment variable
2. `-opencode-server-password-file /path/to/file`
3. `-opencode-server-generate-password`

Ocman injects the selected password as `OPENCODE_SERVER_PASSWORD` when it launches OpenCode and
uses OpenCode's default `opencode` HTTP Basic Auth username for every API and SSE request. The
password stays in backend memory and the managed process environment; it is not stored in
`state.db`, returned to the browser, or included in runtime diagnostics.

To rotate a supplied password, update the environment variable or file and restart ocman, then
restart each managed OpenCode instance from its session action (or let the next managed ensure
replace an instance whose credential no longer matches). Generated passwords are intentionally
ephemeral: every ocman restart creates a new value, and recovered managed instances are stopped
and relaunched on first use. Unmanaged authenticated instances must use the same configured
password; otherwise ocman reports an authentication failure rather than an unreachable instance.

## Multi-remote support (optional)

Attach other ocman instances over the network and manage every machine's
sessions from one hub. On a machine you want to manage remotely, start
its gRPC server:

```sh
ocman -remote-listen 0.0.0.0:8230 \
  -remote-tls-cert cert.pem -remote-tls-key key.pem   # secure default
ocman -remote-listen 0.0.0.0:8230 -remote-trusted-overlay # Tailscale/WireGuard only
```

Then attach it from the hub's **Settings → Remotes** page using the
remote's address and access token. Off by default — a fresh install with
no remotes is unchanged. See [multi-remote.md](multi-remote.md) for the
full step-by-step guide, security notes, and troubleshooting.

## OpenTelemetry (optional)

Pass `--otel=<endpoint>` (or set `OTEL_EXPORTER_OTLP_ENDPOINT`) to ship traces and metrics to
an OTLP collector. Empty / unset disables telemetry with zero overhead.

The URL scheme selects the transport:
- `http(s)://...` → OTLP/HTTP
- `grpc(s)://...` or bare `host:port` → OTLP/gRPC

All other configuration uses standard `OTEL_*` env vars (`OTEL_SERVICE_NAME`,
`OTEL_RESOURCE_ATTRIBUTES`, `OTEL_TRACES_SAMPLER`, `OTEL_EXPORTER_OTLP_HEADERS`, etc.).

For local dev, `make otel-up` starts a bundled Grafana LGTM stack on `:3000` / `:4317` / `:4318`.
The `make dev*` targets automatically export `OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4318`
and `OTEL_SERVICE_NAME=ocman-dev`. See `observability/` for dashboard provisioning.

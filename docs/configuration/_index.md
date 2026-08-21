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

## Flags

| Flag | Default | Description |
|------|---------|-------------|
| `-addr` | `127.0.0.1:8228` | Listen address. |
| `-db` | `~/.local/share/opencode/opencode.db` | Path to OpenCode's SQLite DB. Opened read-only. |
| `-mcp-addr` | `127.0.0.1:8227` | Loopback listen address for the MCP endpoint. Local clients reach it without auth, so non-loopback addresses are refused. Empty disables it. |
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
| `-insecure-no-auth` | `false` | Allow a non-loopback `-addr` / `-gui-addr` with no password configured. Also `OCMAN_INSECURE_NO_AUTH=1`. |

## Environment variables

| Variable | Description |
|----------|-------------|
| `OCMAN_AUTH_PASSWORD` | Auth password. Empty string is treated as unset. |
| `OCMAN_AUTH_TRUST_LOCALHOST` | Truthy value enables the loopback auth bypass. |
| `OCMAN_INSECURE_NO_AUTH` | Truthy value allows a non-loopback listen address with no password configured. |
| `OPENCODE_SERVER_PASSWORD` | Password for managed OpenCode servers and all ocman-to-OpenCode HTTP/SSE traffic. |
| `OCMAN_ALLOWED_HOSTS` | Vite dev/preview only: comma-separated extra hostnames allowed by the dev server (e.g. `foo.tailnet.ts.net,bar.lan`). |

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

For OpenCode instances you start yourself, use an explicit port so ocman can discover them:

```sh
opencode --port 0   # let OpenCode pick a free port
# or pin a specific port, e.g. opencode --port 4096
```

Ocman finds listening OpenCode processes with `lsof` and connects automatically. Without
`--port`, externally launched sessions are still readable from the database but interactive
features stay disabled.

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

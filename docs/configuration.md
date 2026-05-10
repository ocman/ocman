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

## Environment variables

| Variable | Description |
|----------|-------------|
| `OCMAN_AUTH_PASSWORD` | Auth password. Empty string is treated as unset. |
| `OCMAN_AUTH_TRUST_LOCALHOST` | Truthy value enables the loopback auth bypass. |
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

To use the composer, permission replies, abort, and compact, start OpenCode with an explicit port:

```sh
opencode --port 0   # let OpenCode pick a free port
# or pin a specific port, e.g. opencode --port 4096
```

Ocman discovers listening OpenCode processes via `lsof` and auto-connects. Without `--port`,
sessions are still readable from the database but interactive features stay disabled.

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

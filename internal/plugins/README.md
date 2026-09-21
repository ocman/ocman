# Plugin protocol v1

For installation, Settings controls, diagnostics, removal, and development, see
the [plugin guide](../../docs/features/plugins.md).

`protocol.go` defines the external DTOs. They have no dependencies on ocman's
state, platforms, or capability implementations. This package encodes,
negotiates, and validates the protocol, and discovers native plugin executables.

The optional public [Go SDK](../../sdk/plugin/README.md) reuses these canonical
types and validators. It includes lifecycle/action helpers, reusable executable
conformance tests and a deterministic example plugin. NDJSON remains usable
directly from any language.

## Modes and handshake

An executable receives one argument, `describe` or `serve`. In either mode its
first stdout frame is `hello`, containing the mode, a description, and the exact
one-time token supplied by the host. The launcher supplies a fresh cryptographically
random 32-byte token as lowercase hex in `OCMAN_PLUGIN_TOKEN` for each invocation.
Never persist or log the token. Token validation binds the response to that launch;
it does not sandbox the trusted executable.

`describe` emits exactly one hello and exits. In `serve`, the plugin emits its
hello offer, then waits for a host hello acknowledgment containing `mode: "serve"`
and `accepted: {protocol, capabilities}`. The acknowledgment has no token or
description. Only then may calls or events begin. Stdout contains only NDJSON;
diagnostics belong on stderr.
The description declares a reverse-domain plugin ID, display name, plugin release
version, process protocol version, capability versions, maximum concurrent calls,
requested grants, settings, and execution scope. `hub` executes on the hub,
`owner` on the owning machine, and `global` resolves to the hub. Scope is metadata,
not a grant or routing decision.

Process versions and each named capability's versions use `{major, minor}`.
Different process majors fail the handshake. Matching majors select the lower
minor. Each capability is negotiated independently the same way; unknown names
and incompatible capability majors are omitted. Names are unique in an offer.
For example, capability `action` with major `1` is `action.v1`. A call carries the
selected capability version explicitly. A plugin must support older minors of its
advertised major; an added required field or changed meaning needs a new major.
The host acknowledges the exact selected versions, preserving the offer's
capability order. Events use these versions too. Capability-specific schemas are separate.

## Framing and messages

Each UTF-8 JSON object occupies one line, terminated by LF or CRLF. The hard
limit is 1 MiB of JSON, excluding the terminator; nesting is limited to 64 levels.
Blank lines, missing final newlines, duplicate JSON keys at any depth, wrong
known-field types, case aliases, null known fields, mixed bodies, unknown message
types, and malformed JSON are protocol errors. Unknown additive fields are
accepted, including within bodies. Opaque capability payloads may contain JSON
null. Decoders stop permanently on the first error; the process supervisor must
terminate the plugin. Encoders validate before writing and stop permanently after
a transport failure. Owners must serialize access to codecs and stream state.

Every frame has `type` and exactly one matching body:

```json
{"type":"call","call":{"id":"1","operationId":"op-123","capability":"action","version":{"major":1,"minor":0},"method":"invoke","deadlineUnixMs":1800000000000,"params":{}}}
{"type":"chunk","chunk":{"id":"1","sequence":1,"data":{"progress":50}}}
{"type":"result","result":{"id":"1","value":null}}
```

| Type | Direction | Body |
| --- | --- | --- |
| `hello` | Plugin → host | Offer: `mode`, `token`, `description` |
| `hello` | Host → plugin | Serve acknowledgment: `mode`, `accepted` |
| `call` | Host → plugin | `id`, `operationId`, `capability`, `version`, `method`, `deadlineUnixMs`, `params` |
| `chunk` | Plugin → host | `id`, `sequence`, `data` |
| `result` | Plugin → host | `id`, exactly one of `value` or `error` |
| `event` | Plugin → host | `capability`, `name`, `data` |
| `cancel` | Host → plugin | `id` |
| `shutdown` | Host → plugin | Empty object |

Request IDs are positive uint64 values encoded as decimal **strings**, so clients
do not lose precision. The host increases IDs monotonically across all calls in
one process connection, including after completion. This prevents reuse without
retaining an unbounded history. Operation IDs are separate opaque strings, up to
128 UTF-8 bytes, that callers preserve across retries. Deduplication is a broker
responsibility. Deadlines are positive UTC Unix milliseconds; the supervisor
enforces wall-clock expiry and chooses timeout policy.

A call produces zero or more chunks, with contiguous per-call sequences starting
at one, then exactly one result. Calls may interleave up to the advertised
concurrency, bounded by 256. Only negotiated capabilities may be called or emit
events. Unknown request IDs, duplicate results, chunks after results, and messages
in the wrong direction fail the stream. An error also permanently closes the
validator. The selected versions can be read with `Stream.Negotiation` after the
plugin offer. Its readiness flag becomes true after the host acknowledgment, or
immediately after the single describe response.

Cancellation is advisory and repeatable for an active call. Chunks already in
flight and a racing successful result remain valid after cancellation. The plugin
normally finishes with `{"error":{"category":"cancelled"}}`; only a result
releases the concurrency slot. Shutdown is terminal and abandons active calls.
The supervisor settles their callers locally, then waits for exit or kills the
process after its grace period. No outstanding work is automatically replayed.

Safe error categories are `invalid_argument`, `permission_denied`, `not_found`,
`conflict`, `unavailable`, `deadline_exceeded`, `cancelled`, and `internal`. Errors
carry no plugin-provided text or diagnostic data. The host maps categories to
safe user-facing messages. Action data and grant enforcement belong to capability
brokers. Process lifecycle and restart policy are described below.

## Discovery

`Server.StartOnListener` calls `RescanPlugins`; explicit rescans use the same
method. Describe executes trusted native code before enablement; discovery is not
a sandbox. There is no filesystem watcher. The directory defaults to
`~/.local/share/ocman/plugins`; `OCMAN_PLUGIN_DIR` overrides it. Only direct regular
executable children with a nonempty `ocman-plugin-` suffix are candidates. Hidden
files, symlinks, directories, nested files, and non-executables are ignored.

Each candidate runs with only `PATH=/usr/bin:/bin`, `LANG=C.UTF-8`, and a fresh
`OCMAN_PLUGIN_TOKEN`. Its working directory is `/`, stdin reads EOF, and describe
must exit within three seconds. Stdout is bounded to one 1 MiB frame plus its
terminator; stderr is discarded with a 64 KiB limit. Either overflow kills the
process group. Extra frames, malformed output, nonzero exits, invalid tokens, and
incompatible process versions reject the candidate without exposing its output.

The host computes SHA-256 before and after describe and rejects observed file
replacement or modification. A stored executable path cannot change its plugin
ID, even across restarts or removal. Duplicate IDs mark every matching candidate
as conflicted and revoke enablement. Consumers must never serve a discovery entry
with an error. Missing or invalid previously registered plugins are marked removed,
retaining their identity, configuration, and data. A directory-level scan failure
leaves existing registrations untouched. New and changed plugins require approval.
Release versions are nonempty display strings; process and capability versions
use the independently negotiated numeric version pairs above.

`requestedGrants` is a list of unique names. Settings use the following small,
host-rendered schema rather than arbitrary JSON Schema:

```json
{"key":"endpoint","type":"string","label":"Endpoint","required":true,"default":"https://example.org"}
```

Keys and grant names match `[a-zA-Z][a-zA-Z0-9_.-]{0,127}`. Each setting requires a
unique key, a label, and a type: `string`, `boolean`, `number`, or `integer`.
`required` and `secret` default to false. `enum` optionally restricts string values
to a unique nonempty set. Defaults must match the declared type and enum. Secrets
must be strings and cannot declare defaults. These declarations request access;
discovery itself does not grant access or start a serve process.

## Host persistence

`internal/state` owns plugin registration in schema v91. `DiscoverPlugin`
records the description, absolute executable path, SHA-256 checksum and capability
instances. Scope comes from the description and must match each instance. New
plugins are disabled. Changed code, path, description or instances revoke approval.
`SetPluginEnabled` records explicit enablement and grants in one statement;
`SetPluginGrants` replaces the grant set, including immediate revocation.

`SetPluginConfiguration` replaces non-secret values and patches secrets. Callers
split fields using the settings schema. Omitted secrets retain their values;
empty strings clear them. `MarkPluginConfigurationWorking` checkpoints both kinds
of settings after readiness. `RollbackPluginConfiguration` restores both pointers
atomically. Lifecycle callers must serialize configuration activation and readiness
per plugin, so they checkpoint the configuration they actually tested.

Public registration reads return only secret-presence booleans, including for the
last working configuration. `WithPluginSecrets` is a host-local launch helper;
never expose it through management or remote reads. Its callback errors are
replaced with a safe error rather than wrapped.

Files live beside the configured `state.db`, under `plugin-data/<sha256(id)>/`
for plugin-owned files and `plugin-secrets/<sha256(id)>/` for host-owned secret
snapshots. Directories are `0700`, secrets are `0600`. Snapshot writes are synced
before SQL commits their references. Old snapshots are retained for rollback and
diagnostic redaction. `RedactPluginDiagnostics` covers current and historical
values, including JSON/URL escaping, and suppresses diagnostics if history cannot
be read safely. Arbitrary encodings from trusted native code cannot be sanitized
reliably; API failures use fixed errors. In-memory `OpenFromSQL` handles have no
file store and reject secret writes.

Disable and `RemovePlugin` retain configuration, grants, snapshots and private data.
Removal marks a tombstone included in catalog reads. Rediscovery clears that marker
without enabling the plugin. Only `DeletePluginPermanently` deletes retained state;
it rejects enabled plugins and permits retrying file cleanup after an I/O failure.

## Serve supervision

After startup discovery, the server starts one `Process` for each enabled local
registration. `RescanPlugins` and `SyncPluginProcesses` reconcile the same map;
unchanged registrations keep their process, while disable, removal and conflicts
stop it. Management callers must reconcile after changing enablement. The server
waits for process shutdown on exit, including early startup failures.

Each launch rechecks the approved executable checksum, runs `<binary> serve` in
the registration's private `0700` data directory, and passes only the same three
environment variables as discovery. No repository path, inherited environment,
ocman credential or secret-store path is passed. The serve offer must match the
approved description. Readiness requires the token-bound offer and a successfully
written host acknowledgment within three seconds. The host's supported capability
list is explicit; capability brokers supply it rather than trusting the offer.

Serve launches also inherit fd 3, an unlinked `0600` file containing one JSON
object followed by a newline. Its keys are the declared settings, with defaults
applied and secret values included only here. Read and close it before emitting
the serve hello, and exit without a hello if initialization fails. Configuration
never appears in arguments or environment variables. Plugins without settings may
ignore fd 3. SDK users read it before calling `plugin.Run`; describe mode has no
configuration descriptor.

`Process.Call` assigns monotonic request IDs and preserves the caller's operation
ID. Calls beyond advertised concurrency fail with `ErrBusy`. Each call requires
an absolute deadline; either that deadline or context cancellation settles its
caller and sends a cancel frame. The slot stays occupied until a result arrives.
A plugin ignoring cancellation for one second is terminated. Crashes, malformed
stdout, unknown IDs and invalid stream ordering settle outstanding callers with
fixed local errors. Calls are never replayed automatically.

Replies contain streamed chunks followed by one terminal result or local error.
Limits are 1 MiB per frame, 8 MiB total output per call including unknown fields,
16 buffered chunks per call, and 16 buffered events. Slow consumers or output
overflows terminate the process instead of accumulating memory. Pipe workers keep
blocked stdin and incomplete stdout from blocking deadlines or shutdown.

Stderr retains at most a 64 KiB prefix in local process memory across automatic
restarts. The localhost-protected management API exposes redacted text from the
current and most recently stopped process. Launch tokens and current/historical
secrets, including partial trailing credentials, are redacted. A full capture is
suppressed rather than exposing a truncated secret. Each redacted capture is capped
at 64 KiB. Raw stderr never enters telemetry, health, SQL, or HTTP errors. Text
redaction is not a sandbox for trusted plugins that deliberately encode secrets.

Failures restart after 100 ms, doubling to a five-second cap, with at most five
restarts per supervised lifetime. A successful handshake does not reset the failure
budget. Exhaustion leaves the registration terminally unhealthy, including across
server restarts, until an explicit restart, retry, or enable. Health updates are
durable and cannot overwrite a concurrent
disable or discovery conflict. Shutdown settles callers, sends a shutdown frame,
allows one second for exit, then kills the process group and reaps the child.

## Management HTTP API

All routes use ocman's configured authentication. Every POST and the stderr GET
also require a loopback peer and a safe browser origin. Mutations serialize with
rescan and action admission. The endpoints below accept an `ownerId` query parameter,
defaulting to `local`. Remote owners use authenticated `PluginOperation` RPCs and
the same owner-local lifecycle implementation; unavailable owners fail closed.
Secrets submitted for remote configuration are forwarded without hub persistence;
reads return only presence flags. The server exposes these endpoints:

| Method | Path | Result / request |
| --- | --- | --- |
| GET | `/api/plugins` | Catalog, including disabled, conflicted and removed registrations |
| GET | `/api/plugins/discovery` | Up to 128 rejected filenames and safe host errors from the latest scan, owner-local |
| POST | `/api/plugins/rescan` | Rescan executables and return the catalog |
| GET | `/api/plugins/{id}/health` | Status, restart count and safe last error |
| GET | `/api/plugins/{id}/grants` | Requested and approved grants |
| POST | `/api/plugins/{id}/enable` | `{"approval":"<catalog fingerprint>","grants":["context.owner"]}`; approve the reviewed registration and exactly all requested grants |
| POST | `/api/plugins/{id}/disable` | `{}`; stop execution, retain configuration, mappings and data |
| POST | `/api/plugins/{id}/grants` | `{"grants":[]}`; replace approvals with a declared subset, stop in-flight work and restart |
| GET | `/api/plugins/{id}/configuration` | Public `values` and boolean secret-presence indicators |
| POST | `/api/plugins/{id}/configuration/validate` | Validate `values` and `secrets` without writing or launching |
| POST | `/api/plugins/{id}/configuration` | Validate and save `values` and `secrets`; restart enabled plugins transactionally |
| POST | `/api/plugins/{id}/restart` | `{}`; restart an enabled, non-conflicted plugin and await readiness |
| POST | `/api/plugins/{id}/retry` | `{}`; same as restart, including resetting terminal unhealthy state |
| GET | `/api/plugins/{id}/stderr` | `{"stderr":"..."}`; bounded, redacted, local diagnostics |
| POST | `/api/plugins/{id}/remove-data` | `{}`; forget a disabled registration, configuration and private data; retryable |

Mutation bodies are JSON objects capped at 1 MiB. Configuration replaces public
values, applies declared defaults, and patches secrets. Omitted secrets remain
configured; an empty string clears an optional secret. Unknown settings, wrong
types, missing required values, enum violations, and secrets in public `values`
are rejected. Secrets are never returned, including in the last-working snapshot.
The catalog's `approval` fingerprint binds the executable path, checksum, description,
and capability instances. Enable rejects missing or stale fingerprints with 409.

For an enabled, ready plugin, configuration activation checkpoints its working
values, writes the candidate, restarts and waits for readiness. Failure restores
both public and secret snapshots and restarts the old configuration. Rollback
continues after HTTP cancellation. Startup restores enabled registrations whose
public values or secret snapshot differ from their working checkpoint before any
serve process launches. Disabled plugins can be configured without
launching; enable performs readiness before marking their configuration working.
Updating an enabled unhealthy plugin returns conflict; disable it to repair its
configuration, then enable again. Conflicted/removed plugins cannot be launched.

Mutations return the updated registration, except validation and removal return an
acknowledgment. Errors use fixed host text: 400 for invalid configuration/grants,
404 for unknown registrations, 409 for lifecycle conflicts, 503 for readiness
failure, and 500 for storage failure. Configuration failure may return 503 even
when rollback successfully restored the old process; read health for its state.

## action.v1

The host negotiates `action` major 1, minor 0. Descriptions may declare
up to 128 actions alongside that capability:

```json
{"id":"report","label":"Create report","placement":"session","requiredGrants":["context.owner","context.session"],"surfaces":["command-palette"],"confirmation":"Create a report for this session?"}
```

`placement` is `global`, `project`, or `session`. The only v1 surface is
`command-palette`. Labels and optional confirmation text are plain text, at most
128 bytes. Required grants must also appear in the plugin's `requestedGrants`.
An action receives only fields covered by both its own required grants and current
user approval, even if the plugin holds other grants:

| Grant | Context field | Allowed value |
| --- | --- | --- |
| `context.owner` | `ownerId` | Opaque owner identity |
| `context.project` | `projectId` | Opaque project identity, never its directory |
| `context.session` | `sessionId` | Opaque session identity |
| `context.route` | `route` | Core route name, without parameters or query strings |
| `context.selection` | `selection` | Up to 100 `{kind, id}` project/session references |

Identifiers use `[A-Za-z0-9][A-Za-z0-9_.:-]{0,127}`. Selection never contains
selected text, files, messages, or transcript content. Route names are `sessions`,
`projects`, `project`, `session`, `settings`, `inbox`, `routines`, `factory`, and
`analytics`. Context identities are caller-supplied references, not proof of core
authorization. They confer no ability to resolve services, databases, credentials,
transcripts, or filesystem paths. Native executables remain trusted code outside
this broker; this protocol is not an OS sandbox.

### HTTP invocation

- `GET /api/plugins/actions?placement=global&surface=command-palette` lists enabled,
  granted actions. Optional `ownerId`, `projectId`, `sessionId`, and `route` describe
  the current context. Project/session placements require the corresponding ID.
  Responses contain `pluginId`, `ownerId`, execution `scope`, and `action` only.
- `POST /api/plugins/actions/invoke` accepts `pluginId`, `actionId`, `operationId`,
  `placement`, `surface`, `context`, optional installation `ownerId`, and optional
  `confirmationToken`. Unknown fields, unsafe context, and bodies over 32 KiB are
  rejected. Omitted installation owner defaults to `context.ownerId`, which defaults
  to `local`. Global placement always resolves to the hub. Remote operations use
  authenticated `PluginOperation` RPCs and the owner's broker; disconnected explicit
  owners fail closed.
- For a confirmation-required action, the host returns HTTP 409 with
  `{"confirmation":{"text":"...","token":"...","expiresAt":1800000000000}}`.
  Render the prompt and resubmit the **same** request and operation ID with that
  token only after user confirmation. Tokens expire in five minutes and are bound
  to the action declaration and complete invocation. Confirmation never goes to
  the plugin. A completed operation can be read again without reconfirming.
- All endpoints use ocman's normal authentication and origin protection, including
  when password auth is disabled. No cacheable response contains plugin results.

The broker checks current enablement/grants while admitting the call, serialized
with revocation. The wire call has capability `action`, version `{major:1,minor:0}`,
method `invoke`, and params `{"actionId":"report","context":{"sessionId":"ses-1"}}`.
There are no arbitrary action parameters. The host sets a maximum 30-second
deadline, shortened by request cancellation. action.v1 is unary: chunks are rejected.

Concurrent requests with the same plugin/operation ID share one call and terminal
response. Reusing an ID with changed input or declaration returns `conflict`.
Failures and timeouts are retained too: callers must not automatically choose a
new ID to retry a possibly side-effecting action. Before dispatch, schema v92
records a durable plugin/operation receipt without context or output. A duplicate
after host restart returns `conflict` rather than repeating an uncertain effect.
Receipts survive plugin disable, removal, and permanent deletion. Terminal results
and artifacts remain in memory only. The bounded cache holds 4096 operation IDs
and 32 MiB of output for the host lifetime; exhaustion returns `unavailable` without
evicting IDs and replaying work. Results and downloads recheck grants, so revocation
also denies cached results. Context already sent to a plugin cannot be recalled.

### Results

The plugin returns `{"results":[...]}` as its terminal result value, with 1–16
items and at most 1 MiB total JSON. Every item is one of these closed variants;
unknown fields, mixed variants, and arbitrary HTML are rejected:

```json
{"kind":"notice","text":"Report ready"}
{"kind":"link","label":"View report","url":"https://example.org/report"}
{"kind":"artifact","label":"report.txt","data":"aGk="}
{"kind":"navigation","target":"settings"}
{"kind":"refresh","target":"sessions"}
```

Render labels and notice text as text, never HTML or markdown. Links permit HTTP(S)
only, without embedded credentials. Artifacts contain base64 bytes and a plain
filename without path separators. The host replaces `data` with a random `handle`;
`GET /api/plugins/actions/artifact?handle=...&ownerId=...` downloads the bytes from
the installation owner after current authorization checks, as an attachment with
`application/octet-stream` and
`X-Content-Type-Options: nosniff`. Handles never reference filesystem paths.
Navigation permits only the static core destinations `sessions`, `projects`,
`settings`, `inbox`, and `routines`. Refresh hints target `actions`, `projects`, or
`sessions`. These results request host behavior; no plugin HTML or JavaScript runs.

Errors contain only `{"error":{"category":"..."}}`. HTTP mapping: invalid
argument 400, denied 403, missing 404, conflict 409, unavailable 503, deadline 504,
cancelled 408, and internal 500. Plugin diagnostics and transport errors are never
returned. A listed plugin can become unavailable before invocation; the caller
must handle the error without bypassing the broker.

## conversation.v1

The host negotiates `conversation` major 1, minor 0, independently of `action`.
It carries exactly two provider-neutral moves; provider protocols, credentials,
and payload shapes stay inside the plugin.

A description declaring this capability must also request the
`conversation.session` grant and declare a required, non-secret `project`
setting. `Description.Validate` rejects a declaration missing either, so the
approved configuration is always the single project the plugin can reach. There
is no way to widen the grant to a second project.

### Inbound: plugin-initiated normalized message

```json
{"type":"event","event":{"capability":"conversation","name":"message","data":{
  "accountId":"T0WORKSPACE","threadId":"C123:1700000000.000100","eventId":"Ev0A1B2C3",
  "text":"ship it","project":"/srv/repo"}}}
```

| Field | Rule |
| --- | --- |
| `accountId` | `[A-Za-z0-9][A-Za-z0-9_.:@/-]{0,127}`, the provider workspace, opaque to the host |
| `threadId` | Same charset, the conversation, opaque to the host |
| `eventId` | Same charset, stable across redeliveries of this one event |
| `text` | 1–65536 bytes, valid UTF-8, no control characters except tab/CR/LF |
| `project` | Optional claim, at most 1024 bytes, compared only |

Unknown fields are rejected. A `project` that does not clean-compare equal to
the configured project is denied, as is an empty configured project; the
directory the host actually uses always comes from configuration, never from the
event. Events for an unnegotiated capability are rejected by the stream, and an
undrained event flood fails the process, so the host consumes them continuously.

The host creates or resumes one managed session per
`(pluginId, accountId, threadId)` in the approved project and delivers `text` as
a prompt. That mapping is durable (`state.db`'s `plugin_conversation`), so a
restart of host or plugin continues the same session, and it is claimed by an
atomic insert-if-absent, so concurrent first messages for one conversation can
only ever produce one mapped session.

`eventId` must be the provider's identifier for the *event*, not for the
delivery attempt: the host reserves `conv-in:<accountId>:<eventId>` in the same
durable receipt table as actions and drops a repeat outright. Reservation
happens before any work, which makes a redelivery at-most-once — a crash in that
window loses one prompt rather than posting a duplicate prompt and a duplicate
reply into a thread everyone can see.

Delivery goes through the follow-up queue rather than a direct send. An idle
session answers immediately; a message arriving mid-turn is held and drained on
the next `session.idle` edge, one per turn, in arrival order. External messages
never interleave into a running turn the way a composer Enter deliberately does,
because nobody in the thread can see that a turn is in flight. A mapped session
that can no longer be sent to is set aside by the queue after repeated
failures, with a `queue.updated` broadcast.

### Outbound: host-initiated completed reply

The call has capability `conversation`, version `{major:1,minor:0}`, method
`reply`, and a 30-second deadline. conversation.v1 is unary: chunks are rejected.

```json
{"accountId":"T0WORKSPACE","threadId":"C123:1700000000.000100","text":"Shipped."}
```

The account is echoed back so a plugin serving several workspaces posts into the
right one. The thread is resolved from the session through the durable mapping,
so a turn completing after a restart still reaches its thread.

The text is the newest assistant message's text parts only, capped at 64 KiB;
reasoning, tool, and file parts are excluded. The host strips control characters
other than tab, CR, and LF rather than dropping a reply the wire would reject.
Return `{}` as the terminal value.

A completed turn is not called out directly. It is appended to the durable
outbox (`state.db`'s `plugin_conversation_outbox`, migration v97) keyed by
`<sessionId>:<messageID>` — so a repeated idle edge appends nothing — and
delivered by a pump whose ordering group is one conversation. The wire
`operationId` is `conv-out:<deliveryId>`, the row's AUTOINCREMENT id, which is
both immutable and the delivery's sequence number, and is *stable across
retries*.

The outbox is the one place the reply path differs from actions: it does **not**
reserve an operation receipt, because a receipt would make the second attempt of
an undelivered reply a permanent `conflict`. Instead the row itself is the
receipt — acknowledged by marking it done, never by deleting it — so the durable
dedup survives while the retry stays possible. Acknowledgment follows the call,
which makes delivery **at-least-once**: a crash between the post and the ack
replays the same `operationId`, and suppressing that repeat is the provider
adapter's job. An uncertain post should not be repeated blindly.

Failures retry with bounded exponential backoff (5s doubling to a 5-minute
ceiling); provider cooldowns such as Slack's `Retry-After` are absorbed inside
the plugin. After 6 attempts the delivery becomes a dead letter: it stops
retrying, keeps its conversation from advancing (rather than reordering around
it) and waits for an explicit retry or discard through the localhost-only
`conversations/retry` and `conversations/discard` plugin operations, with
`conversations` reporting the backlog. Per-plugin count and byte caps
(`PluginConversationOutboxMaxRows`, `PluginConversationOutboxMaxBytes`) pause
*inbound* admission before the receipt is reserved, so pressure stops new work
visibly instead of dropping replies already owed, and the provider's redelivery
is still accepted later.

Both directions are admitted by `ConversationBroker` under the same critical
section as revocation, reading enablement, grants, and the configured project
from one row. A disabled plugin returns `unavailable`; a missing grant,
undeclared capability, or unapproved project returns `permission_denied`.
Session-starting work runs outside that section because it launches processes.
Permission prompts remain in ocman and are never exposed to the provider.

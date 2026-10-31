# Plugin protocol v1

Page 1.4 adds `PageTableCapability`. Collection columns and detail properties
can use `summary` values shaped as `{label, description?, count?}`, or `severity`
values from `critical`, `high`, `medium`, `low`, `info`, `unknown`. Summary labels
are nonempty strings; label and description are bounded to 4096 bytes. Counts
are integers from zero to one million and represent additional items. Hosts
reject these displays on older negotiated minors. The frontend renders these
as inert text, secondary text, count badges and host-colored severity pills.
Plugin catalogs include the plugin display name. Navigation groups declared
root pages by plugin and owner, rendering their labels as host-managed tabs.
Page 1.4 collections can set `detailPresentation: "drawer"` to request the host's
right drawer. Empty retains route navigation; other values are rejected. The
drawer uses the existing owner/project/item read contract, not a new permission
or executable UI. Configuration rows with `projectId` still navigate to editors.

Page forms also support `projects`, a newline-separated string rendered with
the host's owner-local multi-project picker, and `select`. A `select` field must
set `optionsFrom` to a sibling `list` field whose first field is `text`. The host
uses that text field's values as the choices and updates them as rows change.
Plugins still validate saved paths and selected values. `PageDocument.configured`
is an optional boolean indicating whether a project has a saved configuration.
The workspace homepage uses it to omit unconfigured projects; older documents
fall back to nonempty values or available results. These field types require a
host SDK with project-picker support.

For installation, Settings controls, diagnostics, removal, and development, see
the [plugin guide](../../docs/features/plugins.md).

## Package and module

This package is `github.com/ocman/ocman/sdk/protocol`, part of the public Go
module `github.com/ocman/ocman/sdk` (nested in `sdk/`, versioned with
`sdk/vX.Y.Z` tags). It imports only the standard library. ocman's host code
(`internal/pluginsmod`, `internal/server`, `internal/state`, `internal/remote`)
imports it directly, so this is the single definition of the protocol.

`protocol.go` defines the external DTOs. They have no dependencies on ocman's
state, platforms, or capability implementations. This package encodes,
negotiates, and validates the protocol, discovers native plugin executables,
supervises serve processes and implements the capability brokers. The host and
the conformance suite share these brokers.

The optional public [Go SDK](../plugin/README.md) aliases these canonical
types and validators. It includes lifecycle/action helpers, reusable executable
conformance tests and a [deterministic example plugin](../examples/ocman-plugin-fixture/main.go).
See the SDK README for `go get` and the versioning policy. NDJSON remains usable
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
must exit within fifteen seconds. Stdout is bounded to one 1 MiB frame plus its
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
must be strings and cannot declare defaults. `list` (default false) makes a string
setting hold a JSON array of strings, rendered as add/remove rows and delivered on
fd 3 as an array; it cannot be combined with `secret` or `enum`. `multiline`
(default false) renders a string setting as a textarea and cannot be `secret`.
Other declarations of either flag are rejected at describe and at configuration
save. These declarations request access;
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
reliably; API failures use fixed errors. In-memory test handles have no file store
and reject secret writes.

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
written host acknowledgment within fifteen seconds. The host's supported capability
list is explicit; capability brokers supply it rather than trusting the offer.

Serve launches also inherit fd 3, an unlinked `0600` file containing one JSON
object followed by a newline. Its keys are the declared settings, with defaults
applied and secret values included only here. Read and close it before emitting
the serve hello, and exit without a hello if initialization fails. Configuration
never appears in arguments or environment variables. Plugins without settings may
ignore fd 3. SDK users read it before calling `plugin.Run`; describe mode has no
configuration descriptor.

A description may set `wantsCatalog: true` to also receive a read-only
launch-time catalog snapshot under the reserved `catalog` member, delivered
exactly like secrets on fd 3. The opt-in is only meaningful together with the
single required, non-secret `project` setting (the same key `conversation.v1`
uses); `Description.Validate` rejects the pair otherwise, including a declared
setting keyed `catalog`, and constrains nothing without the opt-in. At serve
launch the host reads the directory catalog from the local platform instance
for that directory and appends `{agents, models, defaultAgent, defaultModel}`.
An unresolved project, an unavailable local adapter, an unreachable instance
or an oversized snapshot omits the member entirely; the catalog never blocks
startup. SDK users read settings and catalog together with
`plugin.ReadConfiguration`, whose `Catalog` is nil when the member is omitted.

`Process.Call` assigns monotonic request IDs and preserves the caller's operation
ID. Calls beyond advertised concurrency fail with `ErrBusy`. Each call requires
an absolute deadline; either that deadline or context cancellation settles its
caller and sends a cancel frame. The slot stays occupied until a result arrives.
A plugin ignoring cancellation for three seconds is terminated. Crashes, malformed
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
allows three seconds for exit, then kills the process group and reaps the child.

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
| `context.project.path` | `directory` | Project directory, resolved host-side from `projectId` at dispatch on the executing owner to OpenCode's one project root (sibling worktrees share it); only for `project` placement, omitted when unresolvable |
| `context.session` | `sessionId` | Opaque session identity |
| `context.route` | `route` | Core route name, without parameters or query strings |
| `context.selection` | `selection` | Up to 100 `{kind, id}` project/session references |

Identifiers use `[A-Za-z0-9][A-Za-z0-9_.:-]{0,127}`. Selection never contains
selected text, files, messages, or transcript content. Route names are `sessions`,
`projects`, `project`, `session`, `settings`, `inbox`, `routines`, `factory`, and
`analytics`. Context identities are caller-supplied references, not proof of core
authorization. They confer no ability to resolve services, databases, credentials,
transcripts, or filesystem paths; the host, never the caller, resolves
`directory` and only for an approved `context.project.path`. Native executables remain trusted code outside
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
remain in memory only; artifact bytes are also written (0600) under
`<plugin-data>/artifacts/`, so a handle downloads after a host restart until the
plugin's data is deleted. The bounded cache holds 4096 operation IDs
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

A description declaring this capability must use execution scope `owner`, request
the `conversation.session` grant, and declare a required, non-secret `project`
setting. `Description.Validate` rejects a declaration missing any of the three,
so the approved configuration is always the single project the plugin can reach.
There is no way to widen the grant to a second project.

`owner` scope is the connector-ownership rule: a connector holds one owner's
provider credentials and drives that owner's one project, so it belongs to the
machine it is installed on. `hub` or `global` would let a second machine claim
the same provider account, and for a long-lived socket that means two processes
competing for one event stream. Session creation follows the same rule: the host
uses its own local host service, never an owner inferred from the project
inventory, so a conversation can neither run on a machine that approved nothing
nor fall back to the hub when an inferred owner is disconnected.

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
The delivery pump asks the same question (`ConversationAllowed`) *before* it
schedules a row, so disabling an installation or revoking its grant stops
delivery at once without spending the reply's retry budget: the row stays
pending and unattempted, keeping its mapping and its text, and the next pump
tick after re-enablement delivers exactly the work that was already owed.
Session-starting work runs outside that section because it launches processes.
Permission prompts remain in ocman and are never exposed to the provider.

## session.v1

### Project association, minor 6

Session 1.6 adds optional `associatedProjectId` to workspace runs. It is an
opaque project ID from the executing owner's project catalog, not a path or Git
URL. Declare `AssociatedSessionCapability` and request `session.associate` as well
as the workspace's existing grants. The owner resolves the ID before admission;
unknown projects, missing grants and unavailable lookup fail closed.

```json
{"workspaceId":"scan-a","associatedProjectId":"project-123","readOnly":true,"subpath":"source-2059222746","archived":true,"prompt":"Review the prepared source"}
```

The association changes dashboard and sidebar grouping only. Execution stays in
the private workspace subpath with exactly the same read-only or tool-denied
rules. `session.associate` does not authorize project execution or grant access
to the associated project's files. The host persists
the directory-to-project association, including after source cleanup or restart,
and joins it with the project's ordinary Git-remote group across machines.
New session targets use the associated checkout, never the temporary copy.

A workspace directory can belong to only one project. Reusing it with another
association returns `conflict`; use a fresh subpath for each project's source.
Omit the field for multi-project or project-independent runs. `projectId` still
selects an execution directory and remains incompatible with `workspaceId`.
The Go SDK refuses to downgrade association requests to older negotiated minors.
Existing unassociated sessions are not inferred from file names or source text;
use the dashboard's project merge action to group historical entries explicitly.

### Follow-up turns, minor 5

`session` 1.5 adds the plugin-to-host `send` method, gated by the separately
approved `session.send` grant alongside the existing session grants. Its closed
parameters are `{"after":"sess-out:123","prompt":"Clarify the supplied inventory"}`.
`after` is the opaque operation ID passed to the plugin's completed handler, not
a session ID invented by the caller. The result is `{"sessionId":"..."}` for
the existing session. A new completed call later carries the next turn and its
own stable operation ID and per-turn usage.

The host verifies ownership, current grants, idle state and the latest completed
turn. It keeps the original directory, model, agent and permission rules. One
durable admission per completed turn prevents duplicate sends across retries,
restarts and concurrent requests. Changed prompts or operation IDs conflict;
uncertain enqueue failures remain fenced. Older hosts refuse this operation.
Plugins must retain distinct completion receipts and account for every turn.
For example, document indexing and dependency inventory workflows can request
clarification without losing their inspected context. The host does not parse
the result or enforce a domain-specific response format.

### Read-only prepared source, minor 4

`session` 1.4 adds `readOnly: true` for a private workspace session. The plugin
must request and obtain both `session.plugin` and the new `session.source` grant,
in addition to `session.run`. It prepares source below
`<plugin-data>/model-workspaces/<sha256(workspaceId)>/<subpath>` and supplies that
nonempty relative `subpath`. No registered project or arbitrary host directory
is selected. Missing directories, traversal, symlink escapes, symlink entries,
special files, empty source and more than 10,000 files are refused.

```json
{"workspaceId":"docs-a","readOnly":true,"subpath":"prepared-documents",
 "archived":true,"prompt":"Read the supplied inventory and index these documents."}
```

The host pins deny-all rules and permits only the `read` tool for the exact
regular files present at admission. Reads must use the absolute paths in the
resolved source directory. Permission rules include exact absolute and source-root
relative names because providers may normalize absolute inputs to relative
resources before checking them. Neither form allows a directory wildcard.
Plugins should prepare an inventory file so the
model can discover the corpus without unrestricted search or shell tools.
Edits, writes, commands, network, MCP and delegation remain denied. Files added
later are not automatically readable. `noTools`, `permissionProfile` and `agent`
cannot be combined with this mode. Existing workspace turns remain tool-denied.

This is a tool-permission policy, not an operating-system sandbox. A native
plugin remains trusted code and must keep its prepared files stable for the
session's lifetime. The same interface supports document indexing and manifest
analysis; the host does not interpret either corpus or its response schema.

Older hosts reject the new mode rather than silently creating a tool-disabled
or unrestricted session. The existing creation receipt, archived-session
identity, usage reporting and completion outbox apply unchanged.

The host negotiates `session` major 1, minor 0, independently of `action` and
`conversation`. A session plugin requires `scope: "owner"`, the requested and
approved `session.run` grant, and a required, non-secret scalar string `project`
setting `conversation.v1` uses — one row, one critical section, one approved
project the capability can ever reach.

Inbound, the plugin sends a unary `run` call. Request IDs increase independently
in each direction; a host `completed` call may use the same ID as a plugin
`run` call without collision. The caller's operation ID is preserved in the
session title. After CreateConfigured and Enqueue, ocman returns a correlated
`{sessionId}` result or a safe error category. Runs are never automatically replayed.

`plugin_session_run` durably reserves `(pluginId, operationId)` before creation.
An identical retry under the same approved project returns the stored session
ID or safe error. Changed parameters or project return `conflict`. A concurrent
retry or crash-left pending receipt returns `unavailable`, never another session.
Pending receipts require investigation because creation may already have happened.

```json
{"type":"call","call":{"id":"1","operationId":"rename-1","capability":"session",
 "version":{"major":1,"minor":0},"method":"run","deadlineUnixMs":1791500000000,"params":{
 "prompt":"rename the module","agent":"plan","model":"anthropic/claude-sonnet-4",
 "permissionProfile":"read-only","subpath":"packages/app"}}}
```

Parameters are closed. `prompt` is required; `agent`, `model` and
`permissionProfile` pass through to the session; `subpath` is a relative path
inside the approved project. Absolute paths, any `..` component, and symlink
escapes are denied before session creation; the resolved directory must exist. There is no directory
parameter. `permissionProfile` resolves an ocman permission profile whose rules
are pinned verbatim onto the session at creation; an unknown name fails the run
with `not_found`. The prompt is queued like any follow-up. `archived: true`
creates the session archived; the host stamps every run with origin `plugin`
and the plugin ID, independently of `archived`.

Outbound, ocman calls `completed` with one completed assistant turn of the
session the plugin started, keyed by the session's newest assistant message.
The operation id (`sess-out:<id>`) is the row's immutable sequence, stable
across retries, so the plugin can recognize a repeat; the plugin must
acknowledge with `{}`. Completions ride a durable outbox
(`plugin_session_outbox`) claimed head-per-session so a thread never
reorders around itself, delivered on the `session.idle` edge with periodic
reconciliation, retried with bounded backoff, and settled by a durable receipt
so a repeated edge appends nothing. A dead letter blocks only its own session.
Successful completion values other than an empty JSON object are invalid.
Session completion text is the newest assistant message's text parts concatenated
verbatim, without progress messages, reasoning or tool output. Conversation's
64 KiB cap and text sanitization do not apply. The complete JSON-encoded call
must fit the protocol's 1 MiB frame limit, including escaping and metadata.
Oversized or invalid completion text returns a structured `invalid_argument`
delivery failure before dispatch; it is never shortened or acknowledged as a
successful delivery. The outbox retains the full text and records the failed
attempt under the same operation ID, following its normal retry/dead-letter rules.
A process or host crash in the send/ack window replays the same operation ID;
the consumer must durably deduplicate its effect. After six failures, the
retained dead letter has no supported inspection/retry/discard controls yet.
Unlike conversation replies, session completions therefore lack
operator-recoverable delivery after retry exhaustion. Restart and re-enable
only recover pending rows, not dead letters.

Both directions are admitted by `SessionBroker` under the same critical
section as revocation, reading enablement, grants, and the configured project
from one row. A disabled plugin returns `unavailable`; a missing grant,
undeclared capability, or unapproved project returns `permission_denied`. The
delivery pump asks `SessionAllowed` before scheduling a row, so revocation
stops delivery without spending the retry budget. Per-plugin row and byte caps
pause *inbound* run admission before any work, so backlog pressure refuses new
runs visibly instead of dropping owed completions. The `(platformId, sessionId)` → plugin mapping
(`plugin_session`) is claimed by an atomic insert-if-absent before the first
delivery. Permission prompts remain in ocman and are never exposed to the
plugin.

### session 1.1 project-scoped extension

Hosts also negotiate `session` 1.1. A plugin declaring
`ProjectSessionCapability` and requesting both `session.run` and
`session.project` does not need a global `project` setting. The additional grant
explicitly permits sessions in this owner's registered projects. A run supplies
an opaque `projectId`; the executing owner resolves it through its local project
database, never through the hub, a caller directory, or remote fallback. Unknown
projects, absent resolvers and revoked grants fail closed. Existing session 1.0
plugins retain their one configured project and require no changes.

`noTools: true` pins an all-tools-denied permission rule before queueing the
prompt. It overrides any selected profile, rather than allowing that profile to
widen model-only work. Empty agent/model selections preserve host defaults.

Completed replies optionally contain `usage` with `promptTokens`,
`completionTokens` and `costUsd`. Counts include reported cache and reasoning
tokens. Missing usage means unknown consumption, not free inference. The
metadata is stored atomically with the completion in the durable outbox and
retained unchanged across retries and restarts. Plugins with dollar limits must
not infer a zero charge from absent metadata.

## page.v1

Page 1.3 adds `model` fields through `PageModelCapability`. The host's model
picker reads the owner's platform catalogs without resolving a project or
starting a runtime. Values are strings in `[platform:]provider/model` format,
or empty for the default. Model fields, including nested fields, are rejected
on older negotiated versions. See the contribution guide for declarations.

Page 1.1 adds static form constraints, fixed selects, numeric fields, collections,
item details and opt-in submit validation. Declare `PageContributionCapability`.
The host supports 1.0 unchanged and selects the lower minor. The exact supported
options, request and response examples, limits and persistence requirements are
in the [plugin contribution guide](../../docs/features/plugin-contributions.md).

Owner-scoped plugins can declare up to eight `pages` with fixed `id` and `label`
fields and request `page.project`. Approved enabled declarations appear in main
navigation without invoking the executable. Page read/save calls carry only an
opaque owner-local `projectId`, page ID, optional revision and JSON values. The
executing owner resolves that project before admission, checks enablement and
the page grant under the lifecycle lock, and rechecks authorization before
returning data. No plugin-supplied path selects a project. Remote requests use
the existing authenticated PluginOperation transport and never fall back locally.

Responses contain a revision, typed fields, values and a validated pane tree.
Field types are text, multiline, secret, boolean and nested list. Schemas are
bounded to 128 fields and four nested lists; existing message and JSON-depth
limits apply. Omitting `fields`, or sending null, means a results-only page and
reaches the renderer as an empty schema. The host checks values against the
schema: booleans are JSON booleans, text-like fields are strings, and list rows
are objects, so null or scalar rows are refused rather than rendered. There is
no markup, script, stylesheet, URL or arbitrary command type. Plugins validate
values and own persistence. Stored secrets must never
appear in responses; secret fields accept new input only. Writes are explicit
and not automatically retried. Plugins should compare revisions and write
atomically. Existing project actions provide consent, confirmations and downloads.

## pane.v1

The `pane` capability, major 1 minor 0, contributes read-only project trees to
the session sidebar. It requires `scope: "owner"`, the requested and approved
`pane.project` grant, and 1–16 unique declarations in `Description.panes`:

```json
{"panes":[{"id":"tickets","label":"Beads"}]}
```

IDs use the normal capability identifier grammar and labels are plain text,
at most 128 bytes. Listing declarations never calls the plugin, probes a
workspace, or runs a tool. Disabled, removed, conflicted, incompatible and
ungranted panes are omitted.

| Method | Endpoint | Purpose |
| --- | --- | --- |
| GET | `/api/plugins/panes?ownerId=<owner>` | Enabled and granted pane declarations |
| GET | `/api/plugins/panes/read?ownerId=<owner>&pluginId=<id>&paneId=<pane>&directory=<absolute>` | Read one project tree |

Both endpoints require normal authentication. An explicit owner is required;
disconnected owners fail closed. Remote projection uses the existing closed
`PluginOperation` RPC. The receiver requires the claimed owner to match its
instance ID and never forwards pane operations to another machine. Pane listing
returns `{pluginId, ownerId, pane: {id, label}}`, with owner identity qualified
by the hub.

Discovery projects pane declarations from the owner's existing `catalog`
operation. Older protocol-7 owners therefore contribute no panes without
breaking plugin management or displaying a false owner-disconnection error.

Read authorization is checked against the current registration while admitting
the call under the lifecycle lock, and checked again before returning results.
The `pane.project` grant authorizes sending the selected directory to the
owner-local trusted process; it is not a filesystem sandbox or a write grant.
Unlike actions, reads create no durable operation receipt or result handle.
Each read gets a fresh operation ID and a maximum 30-second deadline, shortened
by request cancellation. The wire call is unary; chunks are rejected:

```json
{"capability":"pane","version":{"major":1,"minor":0},"method":"read",
 "params":{"paneId":"tickets","directory":"/srv/repo"}}
```

The result is a host-rendered tree:

```json
{"available":true,"nodes":[
  {"id":"bd-1","title":"Parent","status":"open","badge":"P1","kind":"epic"},
  {"id":"bd-2","title":"Child","parentId":"bd-1","status":"blocked","badge":"P2"}
],"warning":false}
```

Only these fields are accepted. Results are capped at 1 MiB and 2000 nodes, with
unique nonempty IDs, no cycles, and maximum ancestry depth 64. Titles are at
most 4096 bytes; IDs, parent IDs, badges and kinds are at most 128 bytes. All
strings must be valid UTF-8 without control characters. Status is optional or
one of `open`, `in_progress`, `blocked`, `deferred`, `closed`; absent parents
produce roots. `available: false` permits neither nodes nor warnings.

Ocman renders text and fixed host styling, never plugin HTML, JavaScript, URLs,
or CSS. `warning: true` preserves the client's last successful tree while
showing a retryable refresh warning. Initial errors also offer retry.
An open pane refreshes every 30 seconds when available; unavailable projects
do not poll. Closing the pane unmounts the query and cancels the HTTP/plugin
read through its AbortSignal. Sidebar catalog reads continue to be metadata-only.

The SDK aliases the canonical types and exposes `PaneHandler`, which validates
read input and tree output. `examples/ocman-plugin-beads` implements this contract
with an explicitly configured absolute `bd` executable. See
[Beads setup](../../docs/features/beads.md).

## ui.v1

The `ui` capability, major 1 minor 0, lets a plugin ship in-app React UI. It is
the higher-trust tier beside the declarative `page.v1`/`pane.v1`; see
[ADR 0009](../../docs/adr/0009-let-plugins-ship-in-app-react-ui.md). A
declaration requires `scope: "owner"`, the requested `ui.app` grant and a `ui`
object:

```json
{"ui":{"sdk":"^1.0.0","exposes":{"index":{"sha256":"<64 lowercase hex>","size":5321}}}}
```

`sdk` is the `@ocman/plugin-ui` range the bundle was built against: `X.Y.Z`,
`^X.Y.Z`, `~X.Y.Z` or `>=X.Y.Z`. `exposes` has one to eight entries keyed by
capability identifiers. This host mounts only `index`. Other names are accepted
and reserved for later entry points, so adding one needs no protocol change. Each
bundle is 1 byte to 4 MiB. Because the checksum and size are part of the
description, the approval fingerprint binds them, and a changed bundle revokes
approval. `UIAllowed` additionally requires the `ui` major 1 capability, the
current `ui.app` grant and a range satisfied by `UISDKVersion`, which a test keeps
equal to `sdk/plugin-ui/package.json`.

| Method | Params | Reply |
| --- | --- | --- |
| `call` | `{"method":"items.list","params":{...}}` | One result with any JSON value; chunks are a protocol error |
| `bundle` | `{"expose":"index"}` | Chunks `{"data":"<base64>"}` of at most 512 KiB each, then `{}` |

Method names match `[A-Za-z][A-Za-z0-9_.:-]{0,127}` and are defined by the
plugin; `params` is optional JSON. Unknown methods should return `not_found`.
Bundle chunks stay below the 1 MiB frame limit and a whole bundle below the
8 MiB per-call output limit. The host rejects chunks beyond the declared size and
bytes whose SHA-256 differs from the approved declaration, then caches the
verified bundle per plugin in memory.

| Method | Endpoint | Purpose |
| --- | --- | --- |
| GET | `/api/plugins/ui` | Hub-local enabled plugins holding `ui.app`, with label, SDK ranges, compatibility, actions and the index URL |
| GET | `/api/plugins/ui/bundle?pluginId=&expose=index&sha256=` | The verified bundle as `text/javascript; charset=utf-8`, `nosniff`, `private, no-cache` and an ETag of the checksum |
| POST | `/api/plugins/ui/call` | `{"pluginId","method","params"}`; the plugin's JSON result |

All three use normal authentication and always target the hub's own installation.
They route through `RouteOperation` as the closed `PluginOperation` operations
`ui-bundle` and `ui-call`. An owner refuses both when they arrive over the remote
transport, so a hub never loads UI code from another machine in this version.
Calls are admitted under the lifecycle lock with the current grants, get a fresh
operation ID and a 30-second deadline, and are rechecked before results return.
Cached bundles are rechecked on every request. Like page reads, calls have no
durable receipt and are never retried.

The browser imports the bundle with `import(url)` from ocman's origin. Its
shared imports point at `/plugin-shared/<name>.js` entries emitted by the host
build (see `frontend/vite.config.ts` and `sdk/plugin-vite`), which re-export the
app's own React, router, query-client and `@ocman/plugin-ui` instances. The SDK
exposes `NewUIDescriptor`, `UIHandler` and `conformance.RunUI`; the
[catalog example](../../examples/ocman-plugin-catalog) uses all three.

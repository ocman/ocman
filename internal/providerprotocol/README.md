# Internal backend protocol

A **platform** is the coding tool that owns a session, such as OpenCode or Claude
Code. A **backend** implements that platform through this socket protocol. A
**model provider** supplies the LLM, such as Anthropic or OpenAI.

The Go handshake field is `Hello.PlatformID`. Its legacy JSON key `providerId`
identifies the platform, not a model provider. The `providerprotocol`,
`providersupervisor`, and `platforms/provider` package paths, `ocman provider`
command, and persisted provider names are retained for compatibility.

HTTP/1.1 with JSON bodies over an owner-local unix socket. Requests and responses
use the exported DTOs in `protocol.go`; request field spelling follows those
shared DTOs. No credentials are exchanged. The runtime owns socket permissions.
Negotiate `/hello` before other calls. Only version 4 is accepted; an explicit
`VersionError` signals runtime management to drain before replacing a backend.
Version 2 publishes exact (escaped) prompt patterns, see below, and answers a
reply the agent received but could not record with 503 `reply_unrecorded`;
version 3 marks replayed lifecycle events of acknowledged turns. Version 4
supports adopting external Claude sessions and advisory owner observations;
older supervisors are replaced once idle so new sends cannot reach the old
read-only behavior. A draining
version-1 supervisor sends raw tool arguments, which the adapter escapes on
arrival, and its generic 503 may follow a delivered reply, so the adapter
reports `ReplyOutcomes` false and the auto-approve pipeline leaves its
prompts to the user.

| Method and path | Request / response |
| --- | --- |
| GET /hello | `Hello` |
| GET /sessions?directory=&since= | `[]Session`, since is Unix milliseconds |
| GET /sessions?live=1 | `[]Session` with only id, status and resetsAt: the statuses this process settled |
| GET /sessions?runtime=1&directory= | `[]Session` owned by this supervisor, including read-only native children and pre-transcript sessions; excludes status-only helper observations. Older supervisors ignore `runtime` and return their ordinary session list. |
| GET /sessions/{id}?limit=&offset= | `SessionDetail`, message paging |
| GET /sessions/{id}/lifecycle | `SessionLifecycle`, bounded read |
| POST /sessions | `CreateSessionRequest` / `CreateSessionResponse` |
| POST /sessions/{id}/fork | `ForkSessionRequest` / `CreateSessionResponse` |
| POST /sessions/{id}/rename | `RenameSessionRequest` |
| POST /sessions/{id}/auto-title | `RenameSessionRequest`, only changes an owned session with no custom title; requires `autoTitle` |
| POST /sessions/{id}/compact | `CompactRequest` |
| POST /sessions/{id}/abort | `AbortRequest`, optional `TaskID` stops only that background task and requires `backgroundTaskStop` |
| POST /sessions/{id}/send | `SendMessageRequest` |
| POST /sessions/{id}/hold | `SendMessageRequest`, Delivery="queue" |
| GET /sessions/{id}/held | `[]NativeQueuedMessage`, oldest first |
| POST /sessions/{id}/held/{messageId}/cancel | `CancelNativeQueuedRequest` |
| POST /sessions/{id}/command | `ExecuteCommandRequest` |
| POST /sessions/{id}/shell | `RunShellRequest` |
| GET /permissions?sessionID= | `[]LivePrompt`, reconciliation only |
| POST /permissions/{id}/reply | `RespondPermissionRequest` |
| GET /questions?sessionID= | `[]LivePrompt`, reconciliation only |
| POST /questions/{id}/reply | `RespondQuestionRequest` |
| POST /questions/{id}/reject | `RejectQuestionRequest` |
| GET /sessions/{id}/rules | `[]PermissionRule` |
| POST /sessions/{id}/rules | `SetPermissionRulesRequest` |
| GET /sessions/{id}/native-permission | Optional `nativeAutoPermission`: `NativePermissionState` |
| POST /sessions/{id}/native-permission | Optional `nativeAutoPermission`: `SetPermissionRulesRequest` with only `NativeMode`; rules are unchanged |
| GET /catalog?directory= | `DirectoryCatalog` |
| GET /events?after= | SSE, described below |
| GET /sessions/{id}/snapshot | `Snapshot`, current v1 upserts plus cursor and single-use lease |
| POST /sessions/{id}/ack | `TurnAck`, exact completed `session.idle` sequence; 204 |
| POST /sessions/{id}/history-confirmed | Optional `historyConfirm` capability: `HistoryConfirmation` of durably committed message IDs and part-content digests; reclaims matching settled child views, never acknowledges turns; 204. |
| POST /drain | `DrainState`, stops new turns before reporting whether all sessions are idle |
| POST /drain-idle?token= | `DrainState`, stops admission only when already idle; active turns, admitted requests, limited sessions and pending prompts refuse culling |
| POST /resume?token= | `DrainState`, restores admission only for the matching cleanup pause |
| POST /seal-idle?token= | `DrainState`, commits the matching idle pause to replacement; only `idle:true` authorizes stopping the supervisor, and `/resume` cannot undo it |
| GET /sessions/{id}/ownership | Optional `ownership` capability: `SessionOwnership`, native loaded-thread proof with source key and idle state; never launches a runtime |
| GET /account | `Account` |
| POST /hooks | `HookEvent`, external-session status and advisory ownership; optional `pid` and `managed` identify the originating process; 204 |

Optional Platform capabilities also support GET `/sessions/{id}/changes`, GET
`/sessions/{id}/info`, and POST `/sessions/{id}/move` using the shared Platform
DTOs. Mutations with no response return 204. Errors return JSON `Error`; 404 means
not found, 409 busy, 501 unsupported. Other 4xx errors retain the backend's
message. A prompt reply the agent received but whose event the backend could
not record yet is 503 with code `reply_unrecorded`
(`platforms.ErrReplyUnrecorded`): the decision took effect, its event follows. Unsupported methods must be gated locally using `/hello` capabilities.

Create/fork rejections known to precede allocation include error code
`creation_rejected`, independently of their HTTP status. Adapters preserve it as
`platforms.ErrCreationRejected`. Without that explicit guarantee, even an error
response leaves allocation uncertain. Older supervisors omit the code and remain
conservative; clients must never infer safe replay from a status alone.
Composer gates creation and sending; rename, native queue and account have
additional protocol capability flags. Browsing and lifecycle reads are required.

Busy responses preserve the backend's user-facing message. A 409 with code
`session_owned_elsewhere` is a definite rejection before turn admission. It
permits bounded recovery only after verifying an idle managed owner for the
same native thread and source key. Other busy or transport errors never permit
automatic replay. A backend without ownership probes or idle sealing cannot be
stopped for ownership recovery. Sealing atomically rechecks the cleanup token
and idle state, then clears the resumable token so reconnect cannot undo the
replacement fence. An uncertain stop after sealing leaves the fence intact.

Idle culling uses the optional `/drain-idle` route rather than `/drain`. A random
token scopes each cleanup pause and its rollback; replacement `/drain` clears
the token, so rollback cannot undo replacement. `/hello` exposes an optional
`idleDrainToken` while paused, letting the owner recover interrupted cleanup on
the next Ensure, including after a dashboard restart or lost response. Rollback
uses a bounded context independent of the cancelled cleanup request. Older
supervisors that reject idle draining stay running until replaced normally.

SSE frames carry `id: <positive uint64>` and `data: <Event JSON>`. `after` is
exclusive and defaults to 0. The sequence orders all sessions on this socket,
so one cursor can replay all sessions. A backend must not reuse a sequence
within a supervisor lifetime. The runtime must create a fresh adapter when it
replaces the supervisor. Comment heartbeats keep an idle stream alive.
Frames are limited to 8 MiB. A replay must retain the initializing
`message.part.updated` before a part's deltas, so replay reconstructs the part
rather than appending to the browser's REST snapshot. Event properties are the existing OpenCode v1 shapes
for `message.updated`, `message.part.updated`, `message.part.delta`,
`session.status`, `session.idle`, `permission.asked`, `permission.replied`,
`question.asked`, `question.replied` and `question.rejected`. Status also allows
`limited` with `resetsAt` in Unix milliseconds. Interrupted prompts are removed
by a replied event with a cancelled reason.

Backends may also publish `ocman.session.background` with
`{sessionID, count}`. This is an absolute nonnegative count of tracked background
tasks, not a delta. A leased browser snapshot includes the current count, zero
included; foreground `session.idle` does not clear it. Optional `tasks` entries
carry `id`, `description`, `kind` and `lifecycle`. `lifecycle: "service"` explicitly
declares persistent work; missing or unknown values mean a finite task. Service
classification affects completion-based archive resurfacing, never foreground
status or queue ordering. User overrides are persisted by the owning ocman
instance, separately from the native declaration.

A permission prompt's `patterns` and `always` use OpenCode's wildcard syntax
plus one escape (`ExactPattern`, `PatternValue` in `pattern.go`): a backslash
before `*`, `?` or `\` makes it literal. A backend publishes a call's
argument as an exact pattern, so "Allow always" grants that call only.

A headless reconnect re-delivers still-pending prompts under new sequences;
that re-delivery does not reopen a turn whose idle edge was already published.
A prompt resolved after its turn was acknowledged keeps its ask in the spool
until a later acknowledgment, so a consumer replaying the resolution has the
prompt's metadata. The lifecycle events (`session.status`, `session.idle`) of
that acknowledged turn are still in the spool: the headless stream sends them
with `acknowledged: true`, and the adapter rebuilds state from them without
running turn-end side effects (queue drain, routines, prompt pipeline).

The adapter's `WatchEvents(ctx)` is the headless, reconnecting consumer. Install
the shared lifecycle sink first and `SetEventHandler` for the headless prompt
pipeline. `ProxyEvents` replays v1 payloads to the browser for its session from
sequence zero, covering changes between the REST snapshot and connection.
Prompts already consumed by the headless watcher come from the current pending
cache on connection rather than being replayed into the judge. Both update instance-local caches, with sequence
deduplication for sink delivery. Pending lists are read from those caches;
explicit refresh operations reconcile them with the backend. Session ownership
is learned from listing, details, creation and events without network calls from
`Owns`. Session-scoped catalogs resolve the cached directory, fetching detail
only when it is unknown.

Claude agent tasks publish `session.created` with their actual native parent,
then child `session.status` busy before forwarded messages. Their runtime detail
can seed history ingestion before a transcript exists. Completion publishes a
child idle edge independently of the parent, with error or interrupted status
first for failed or stopped tasks. Non-agent background tasks are not sessions.

Backends advertising `capabilities.replay` implement explicit turn acknowledgment
and snapshot leases. This is an additive extension supported on both internal
versions 1 and 0; a backend without this capability retains the legacy stream
behavior and must not truncate its spool. `/drain` is a version-independent
runtime control, including on supervisors outside the negotiated version range.
It remains available during draining, as do permission/question replies and abort.
Busy, waiting and limited sessions are not idle. These, with `GET /hello`,
the `GET /permissions` / `GET /questions` reconciliation lists and the headless
event stream `GET /events?consumer=headless&after=<n>` (SSE `id:`/`data:`
framing; of its events only `permission.*` and `question.*` are read), are the
version-independent routes: while an out-of-range supervisor drains, the
dashboard reaches it only through them (`provider.NewDraining`). Its prompts,
including ones asked later in the draining turn, are pushed to the prompt
pipeline (rules, judge, Inbox) and to the session's browser stream, never
polled, so they stay answerable and the drain can finish; turns are not
acknowledged. A supervisor launched with
another launch configuration (CLI or Claude config, `BackendRuntime.Identity`)
is drained the same way with its regular adapter. Their shapes must not change
between versions.

Only the headless watcher acknowledges turns, after applying their terminal event
to the lifecycle and prompt handlers. Failed acknowledgments retry on reconnect.
An acknowledgment cannot target an unfinished event and is idempotent. The
supervisor retains whole turns, including part initialization, until completion,
acknowledgment and all attached replay consumers have passed their end. Pending
permissions and questions prevent truncation.

For browsers, `ProxyEvents` takes a fresh snapshot containing v1 upserts for the
session's current messages, parts and status, plus unresolved prompts. It writes
those upserts before attaching to `/events?after=<cursor>&lease=<lease>`. The
snapshot's cursor is captured under the same lock as publishing and the backend's
state update. The single-use lease pins intervening events for 60 seconds; expired
leases return 409 and require a fresh snapshot. Each tab has its own lease. This
repairs a stale REST snapshot even if an earlier turn was already acknowledged
and truncated, without appending historical deltas to that snapshot. Headless
connections use `consumer=headless`; unresolved requests at or before their
cursor are reissued with a new sequence so adapter dedup does not drop them.

Backend turn-start handlers call `Supervisor.BeginTurn` synchronously before
scheduling asynchronous child work or returning success. It records busy under
the same lock as `/drain`, rejecting a start once draining begins. Publish the
terminal idle event only after that child's turn has actually finished.

Rules are decided by ocman, never evaluated in the supervisor. The rules endpoint
is a transport for the authoritative ruleset, not permission evaluation. An
adapter given an ocman-owned `Rules` store (`SetRules`, used for Claude) serves
rulesets locally and answers rule-decided prompts itself before they reach its
pending map, its event handler or a browser stream; such a backend may answer
the rules endpoints with 501.

Claude supervisors advertising `nativeAutoPermission` also register SDK
`PreToolUse` callbacks. They publish ordinary permission events with
`metadata.ocmanPreflight=true`. On these events no matching ocman rule means
continue to native permission evaluation, while explicit ask/deny rules still
apply. An approval returns an empty hook result, never a native allow; a rejection
returns a `PreToolUse` deny. Missing owner rules reject the callback. Unanswered
callbacks stop their child before the CLI hook timeout. Native mode is persisted
as launch state, not as an ocman permission ruleset.

The browser writes `PUT /api/session/{id}/native-permission-mode`; remote owners
receive the distinct `SetNativePermissionMode` Call method. Do not send native
changes through legacy rules mutation routes: an older owner would ignore the
new field and interpret it as a rules reset instead of rejecting it.

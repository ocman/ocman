# Session catalogue

`sessioncatalog` stores Ocman identities separately from native execution IDs
and conversation history. Its schema is registered in `server.ModuleSchemas()`
and lives in `state.db`. It does not launch agents or call platform adapters.
The original v1 schema is frozen. The append-only v2 migration preserves IDs,
bindings and creation receipts while upgrading earlier development layouts.

The canonical-core branch adds v3 legacy-deduplication markers and v4 canonical
remote metadata. `MirrorCanonical` stores an authenticated owner's ID without a
native execution binding on the hub. `GetCanonical` can read that metadata, but
`Resolve` cannot turn it into local execution authority. Native-link compatibility
and old creation receipts now live in `internal/platforms/owned`, not services.
The application-wide canonical cutover is still being wired; these helpers do
not by themselves replace startup's existing native-keyed state paths.

## Current implementation

- `Reserve` persists `ocm_<uuid>`, the stable owning-machine ID, platform,
  directory, Ocman parent ID and origin before platform creation. Routine and
  factory origins require their routine/attempt ID immediately.
- A request key is unique within an owner. Replays return the same identity
  with `created=false`, including interrupted and failed attempts. A hash of
  the complete creation payload prevents changed execution settings from
  being treated as the same request.
- `Bind` attaches an owner/platform/source/native-ID tuple. The tuple is unique
  and immutable. A native session cannot be stolen by another reservation.
- `Attach` persists a returned native ID in `configuring` before permissions or
  linkage run. `Bind` completes it to `ready`. Interrupted configuration retains
  the native ID for recovery; confirmed disposal records a failure without
  releasing the native binding for reassignment.
  For integrations with `SessionCreationPlanner`, `Attach` records the planned
  ID before native creation instead. OpenCode V2 accepts that ID atomically;
  a lost response or a crash before POST can resume through an explicit keyed
  retry without allocating a different session. Configuration and application
  linkage must still finish before `Bind`; startup does not publish unfinished
  sessions or automatically retry prompts. Older unplanned creates and forks
  retain their explicit-reconciliation requirement.
- `Discover` imports external sessions idempotently. Existing bindings retain
   their origin. It defers unknown sessions in directories with pending creates,
   because native events may arrive before creation returns its ID. Metadata
   proven older than a reservation is not blocked by it. Incomplete identities
   are omitted from lists rather than failing unrelated rows.
- `Reconcile` batches discovery metadata, preserves native parent references
  when parents arrive later, and repairs the Ocman parent links atomically.
  Cycles fail without committing a partial hierarchy. Empty historical
  directories do not prevent identity backfill.
- The catalogue module inventories statev2 metadata on startup, fenced by its
  durable outbox sequence, then consumes changes. It never reads conversation
  messages, opaque source payloads or platform content. Deferred announcements
  retry independently with bounded buffers/backoff; overflow rebuilds metadata.
- Explicit history purges scrub display metadata while retaining an identity
  tombstone. Startup also reads purge tombstones, so offline purges are not
  missed and stale inventories cannot restore a purged title.
- `Resolve` requires an explicit owner and a ready binding. It never probes a
  different owner or falls back from an unknown Ocman ID to a native ID.

The owner ID is the persisted machine identity, not the UI's `local` alias or a
remote connection ID. Platform names are owner-local, not `r-<remote>:<platform>`
registry names. Source keys identify physical platform data stores. Callers
must use the same canonical directory representation for reserve and discovery.

`creating`, `configuring`, `ready` and `creation-failed` describe identity binding, not agent
execution status. Runtime status remains platform-owned. An uncertain creation
outcome stays `creating` or `configuring`; automatically replaying the platform call could launch
a duplicate. Recovery must find the native execution and bind it, or establish
that creation did not happen before recording a definite failure. A failed
reservation does not authorize another execution with the same request key.
Migration v7 repairs databases whose earlier metadata migration was recorded
without the `configuring` constraint, preserving identities, native bindings,
parent links, purge markers and creation receipts.

Adapters can report `ErrCreationRejected` only when rejection precedes native
allocation. Those reservations become `creation-failed` and stop deferring later
external discovery. Network failures and unmarked errors remain uncertain. The
provider protocol and authenticated remote RPCs preserve this explicit guarantee.
Metadata observed while a native binding is `configuring` stays in bounded retry
tracking until binding completes, without requiring another history update.

## Transport integration

Owner-local `sessionsvc` creation and forks reserve before calling the platform, attach
the returned native ID before configuration, and complete binding before
publishing. `server.WithConversationHistory` installs it alongside
history ingestion. Routine and factory origins travel through worktree requests
and remote RPCs. The remote owner applies the initial permission rules and
reserves the identity; the hub does not allocate another ID. Completed keyed
requests return the existing native ID without launching again. Unkeyed callers
receive a new request key for each invocation.

Public session lists, detail/creation responses, MCP session reads and browser
events now project normalized references to Ocman IDs. `sessionview` leaves
native message/part IDs and opaque provider/tool data unchanged. Owner-scoped
identity RPCs batch projection and mirror the owner's ID on the hub. Unknown
IDs, disconnected owners and inactive physical sources fail closed.
Remote protocol v8 requires the identity RPCs before publishing adapters or
launching sessions. Both machines must be upgraded; an older remote is not
replaced with hub-minted IDs or a native-ID response fallback.

Projection follows explicit wire contracts rather than recursively rewriting
ID-like JSON keys. The wire shape and incomplete-list-row policy are separate;
omitting unresolved rows never allows an unresolved detail response. REST and
MCP share the same projector setup. Covered public boundaries include notifications and push
destinations, routine definitions/latest runs/history, Factory attempts/control
sessions, Inbox links, artifact filters/metadata, webhook dispatches, shares,
worktree/forge launches, feature MCP arguments/results, and REST/OCE content.
Analytics resolves by physical history instance before grouping session logs;
native message IDs and snapshot titles/directories remain unchanged.

Cold/reconnected `stream.hello` overlays project prompt and in-flight session
references, leaving prompt, message and part IDs unchanged. Batched task reads
keep canonical IDs until adapter selection; explicit owners never use the
native-only registry cache. Each task response carries its platform for projection.
Identity RPCs and request projections retain lookup causes and local 404/409
semantics instead of replacing them with generic transport/history errors.

Public session paths and declared top-level request references translate back
to native keys before mutation. REST bodies, query references and MCP arguments
share owner validation. REST request contracts select fields by route and method,
not Go reflection; unrelated ID-shaped fields remain opaque. Existing archive, seen, pin, queue and permission data
therefore retain their native ownership and do not need destructive rewriting.
Legacy browser links resolve through `/api/session-identity` before loading
the conversation or attaching its stream. The capability is enabled by the
backend automatically, not an experimental setting.

Browser adoption relocates existing composer text and merges bookmarks and
failed-send recovery without overwriting canonical edits. It copies uncertain
first-delivery receipts without executing them, before first-delivery readiness
is published. Canonical routes and first-delivery reconciliation share the same
in-flight lookup and adoption. Receipts suppress duplicate legacy bookmark copies and prevent
reassigning those copies after a physical-source change. Unqualified native
browser state is not assigned to a remote owner with a colliding ID.

The versioned plugin `session.v1` execution protocol and its native correlation
receipts remain unchanged for in-flight completion compatibility. Plugin notice
links use the owned browser identity, or the application root if unavailable.
Changing the execution protocol's IDs requires a separate versioned migration.

This is not an idempotent start-and-first-message endpoint. Browser draft/start
receipts must be integrated before automatically retrying uncertain submissions.

Forks reserve the parent link before copying native history and include the
native fork point in the request fingerprint. Existing parents missing from
the catalogue are adopted from their owner-local summary without loading
messages. Remote forks reserve on the owner, not the hub.

Legacy routine/factory provenance is backfilled only for the selected physical
source. Native-only legacy associations cannot be safely attributed to an
inactive source with a colliding native ID. Pin, seen, archive, permissions,
queues and other native-keyed state are not rewritten or removed by indexing.

## Remaining work

1. Carry durable caller request keys through browser start and first-delivery
   receipts without replaying a first message. Reconcile interrupted native
   creation and configuration.
2. Historical identities belonging to inactive source installations are retained
   but cannot be rebound to the currently selected native source. A dedicated
   retained-source read flow can expose those without restoring live authority.

Persisted drafts and draft UI changes are outside this work.

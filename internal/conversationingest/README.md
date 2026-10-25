# Conversation ingestion

This owner-local module populates the separate `state_conv.db`, which
serves settled conversation reads and analytics. Live command routing and
active-session streaming are unchanged.

## Source contract

Adapters optionally implement `platforms.HistoryReader`, with a credential-free
physical source identity, complete session inventory, and consistent full-session
snapshots. UI-filtered lists and paginated detail responses are not valid inputs.

- OpenCode inventories every source session. Metadata and content come from one
  read-only SQLite transaction. V2 reuses `ocv2.ConvertMessage`, preserves original
  native payloads, and retains unsupported/system messages as opaque records.
- Claude borrows the browsing supervisor's existing index. Inventory includes
  finished subagents. Full transcript snapshots use the existing converter and
  verify file size, mtime and identity before and after reading. A changed or
  malformed transcript fails the snapshot rather than publishing partial history.
- Neither source launches a runtime or calls live mutation/lifecycle handlers.
  Remote adapters are not history sources. Each owner imports its own data;
  a hub does not replicate its remotes' history in this increment.

## Identity and worker lifetime

The stable instance ID hashes the owner ID, platform type and physical source
key. Routing adapter IDs are separate. Several sources of the same platform can
coexist, including identical native session/message/part IDs.

The server builds the module before startup, registers it in `Server.modules`,
and joins `Run` before application cleanup closes the history database. Each
source gets one independent serialized worker. A failed source cannot rebind
another source or prevent its worker progressing. Source reads have deadlines and
shutdown cancellation; notifications perform no platform/database I/O.

Historical passes share one background slot across sources. The default
`BackfillDutyPercent` is 20: after each session, the importer rests for four times
the measured work duration, with a minimum 10 ms pause. This is a wall-time duty
cycle, not a machine-wide CPU cap. No transaction is held during the pause.
Live updates and requested conversation reads bypass the background budget;
waiting readers can interrupt the cooldown or another source's slot wait.
Already committed snapshots keep their checkpoints if the worker is cancelled.
A single snapshot still runs atomically, so its individual cost can cause a
short burst before the importer yields.

Detailed multi-instance discovery/configuration and UI remain separate work.
Current startup binds the local adapters already configured for this owner.

## Import and repair

Each worker picks one mode for its source:

- **Feed** (OpenCode v2, `feed.go`) follows the machine server's `/api/event`
  firehose. Each (re)connection catches up by comparing OpenCode's durable
  per-session event sequences with those already applied; live events read only
  the messages they name. It does not poll content.
- **Journal** (Claude, `journal_mode.go`) compares version hints once at
  startup, then targets the sessions named by hook journal records and their
  retained descendants, without inventorying unrelated transcripts. Claude
  resumes conversion at the last message group's byte offset, replaying that
  group for appended tool results and upserting only the transcript tail.
  Transcript progress and content commit atomically. Invalid cursors or
  detected replacements/truncations fall back to a full snapshot. The shared
  Claude index may still reindex the changed file to refresh its metadata.
- **Polling** (OpenCode v1, `worker.go`) compares inventory version hints every
  30 seconds and reads only invalidated, new or re-hinted sessions. A full
  verification rereads every session every six hours (`VerifyInterval`).

Full verification is manual-only for feed and journal sources. In every mode,
`ocman.session.*` change/activity/idle/status invalidations request repairs,
coalesced with a debounce. Invalidations that arrive during an import remain
queued. Queue overflow requests a repair of every session instead of losing
identities or allowing unbounded growth.

Live activity hints are separate from explicit repairs. Feed sources skip hints
and replayed events whose durable sequence is already applied; explicit repair
requests still validate the source even when its sequence has not advanced.
When a repair or explicit refresh overlaps a live event, the worker reads the
complete session before acknowledging it; named-message updates cannot satisfy
that repair because they may omit the completed reply.

Inventory versions are only hints. Verification catches old-message changes and
late imports that do not advance a session's timestamp. Persistent
per-session snapshot fingerprints use statev2 checkpoints to avoid rewriting
unchanged content, including after restart. The `history/snapshot-v1/` stream
names this normalization contract; incompatible normalization changes must use
a new version.

The importer reads the retained revision and checkpoint before taking a source
snapshot. One statev2 transaction commits normalized content, revision, fingerprint
and invalidation. A concurrent writer cannot be overwritten by a stale
snapshot. Failed reads, normalization or commits are retried; progress is recorded
only after success. Retrying sessions that already failed records a new failed
task only when the failure changes. A restart safely revisits all sources using persisted progress.

Missing sessions are marked source-deleted only after a complete successful
inventory. Missing messages/parts are marked only from a successful complete
snapshot. All content remains retained.

Unresolved creation reservations defer only the affected identities. Adapters
return the resolvable inventory with `platforms.HistoryDeferred`; workers import
those sessions and report a deferred counter. Such an inventory cannot authorize
source-deletion markers or complete-history verification. Ordinary source
errors still reject the inventory entirely. Project browsing uses committed
history immediately. Analytics also uses committed projections from initialized
local sources during incomplete or failing imports, without native fallbacks.
Its partial-data response header is diagnostic only; users inspect sync health
in Settings → System → Application health or Background tasks. Retrying ingestion
does not produce global import toasts or replace the application shell.

## Limits of this increment

Events are repair hints, not a lossless event archive. This module imports
committed platform history; transient streamed content absent from the platform's
durable history can still be missed. Supervisor acknowledgments are unchanged.
Direct event ingestion with persistence-before-acknowledgment remains necessary
before replacing platform-backed active conversation streaming.

No historical completion dispatches a queue message, routine, permission decision
or plugin action. The statev2 outbox contains history invalidations only. Future
completion reconciliation needs its own durable idempotency receipts.

Attachments and tool payloads are retained inline in normalized extensions for
now. Blob extraction, health endpoints, explicit backfill controls and reader
cutover are not included here.

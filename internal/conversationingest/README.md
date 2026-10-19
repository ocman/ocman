# Conversation ingestion

This owner-local module populates the separate durable `statev2.db`, which
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

Detailed multi-instance discovery/configuration and UI remain separate work.
Current startup binds the local adapters already configured for this owner.

## Import and repair

Each worker picks one mode for its source:

- **Feed** (OpenCode v2, `feed.go`) follows the machine server's `/api/event`
  firehose. Each (re)connection catches up by comparing OpenCode's durable
  per-session event sequences with those already applied; live events read only
  the messages they name. It does not poll content.
- **Journal** (Claude, `journal_mode.go`) compares version hints once at
  startup, then rereads the sessions named by hook journal records.
- **Polling** (OpenCode v1, `worker.go`) compares inventory version hints every
  30 seconds and reads only invalidated, new or re-hinted sessions. A full
  verification rereads every session every six hours (`VerifyInterval`).

Full verification is manual-only for feed and journal sources. In every mode,
`ocman.session.*` change/activity/idle/status invalidations request repairs,
coalesced with a debounce. Invalidations that arrive during an import remain
queued. Queue overflow requests a repair of every session instead of losing
identities or allowing unbounded growth.

Inventory versions are only hints. Verification catches old-message changes and
late imports that do not advance a session's timestamp. Persistent
per-session snapshot fingerprints use statev2 checkpoints to avoid rewriting
unchanged content, including after restart. The `history/snapshot-v1/` stream
names this normalization contract; incompatible normalization changes must use
a new version.

The importer reads the retained revision and checkpoint before taking a source
snapshot. One statev2 transaction commits normalized content, revision, fingerprint
and invalidation. A concurrent writer or purge cannot be overwritten by a stale
snapshot. Failed reads, normalization or commits are retried; progress is recorded
only after success. Retrying sessions that already failed records a new failed
task only when the failure changes. A restart safely revisits all sources using persisted progress.

Missing sessions are marked source-deleted only after a complete successful
inventory. Missing messages/parts are marked only from a successful complete
snapshot. All content remains retained. Explicitly purged identities are skipped
before their source content is read, and statev2 checks the tombstone again at
commit if a purge raced an in-flight snapshot.

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

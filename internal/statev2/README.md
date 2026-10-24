# Statev2 conversation storage

`statev2` is Ocman's owned conversation datastore. It
opens a **separate `state_conv.db`**, not tables in `state.db` or a new
analytics cache. It has no platform, server, or legacy state dependency.

Application startup always opens it and runs owner-local ingestion through
`internal/conversationingest`; there is no feature flag. Settled conversation
reads and conversation analytics come from statev2. Runtime actions and live
streaming of active sessions stay platform-owned.

## Opening and ownership

`Open(path)` requires an explicit file path. `Path(legacyStatePath)` resolves
`state_conv.db` beside the supplied application state path. Tests use `t.TempDir()`;
development must supply a worktree-isolated path. Do not open a live database
for development or copy a running database file without its WAL.

The application path is `-conversations-db`, else `OCMAN_CONVERSATIONS_DB`, else
the derived path beside `state.db`. The old `-statev2-db` flag is an alias and
`OCMAN_STATEV2_DB` is a fallback when the new environment variable is unset.
Either override changes only the conversation
database; it does not isolate legacy state, platform indexes or agent
subprocesses. A development server must still isolate
all of those using the development guide, not merely change this flag or port.

`state.db` retains application settings, queues, permission rules, canonical
identities and feature state. `state_conv.db` holds conversations and their
derived project/search/analytics data, including the persisted project index.
The internal package keeps its `statev2` name; the on-disk name is not a schema
version.

When the new default filename is first used, startup adopts an adjacent
`statev2.db` using SQLite's consistent snapshot (including WAL), without
removing the original or overwriting an existing destination. A marker in
application state records adoption, so deleting the new file later does not
resurrect retired data.

### Explicit offline reset

Stop Ocman first and back up both databases. Removing `state_conv.db` and any
of its `-wal`/`-shm` sidecars starts an empty conversation store on the next
launch; do not delete `state.db`. Available native sources will be ingested
again with the existing canonical identities. Settings, queues and rules stay
intact. History whose physical source has disappeared cannot be reconstructed
without a backup. Never remove SQLite files while Ocman is running. The
application never performs this reset automatically.

The database uses WAL, full synchronous commits, enforced foreign keys, private
database/sidecar permissions, and a serialized connection. This connection is
independent of `state.db`, so ingestion does not share its connection queue.
The file has an application ID and an append-only migration sequence. Opening a
foreign database, negative schema version, or newer schema fails closed. It
never wipes and rebuilds history. Future upgrades of an existing schema must
include a database-consistent pre-migration backup before applying new versions.

## Identities and data

An `Instance` is a stable ID plus immutable owner, platform type, and source
key. The source key identifies a particular installation or configuration and
must not contain credentials. Endpoint/connection settings are not part of this
storage contract. Registration may change a display name but cannot rebind an
identity. Multiple instances of the same platform are supported.

Sessions are keyed by `(instance ID, native session ID)`. Messages and parts
are scoped underneath that key. Source-defined positions plus native IDs give
deterministic pagination without using ingestion arrival times for ordering.
Extensions preserve structured platform details and unknown part kinds.

The normalized model deliberately does not import `internal/db` or persist
OpenCode's entire wire response as the canonical session record. Typed usage,
relationships, blobs, and runtime observations will be added with their owning
ingestion behavior rather than unused tables in this initial schema.

## Commit contract

`Commit` accepts one session's normalized upserts. Session metadata and each
supplied message/part are full replacements of that record's fields. Omitted
records remain untouched by default. `Batch.Complete` marks omitted messages and
parts source-deleted while retaining their content, for authoritative full
snapshots only. It atomically commits:

- Session, message and part changes.
- The new session revision.
- An optional source-stream checkpoint.
- A content-free invalidation in the change outbox.

`ExpectedRevision` is compare-and-swap protection against a concurrent repair.
An adapter must establish source ordering before constructing a batch. This
guard alone cannot tell that a snapshot fetched earlier is older than a live
event fetched later. Do not use wall-clock timestamps to resolve that race.

A checkpoint has an opaque cursor and expected predecessor. One cursor
identifies one committed batch. An immediate retry of the last cursor is a
no-op, even after restart. Other predecessor mismatches fail with `ErrConflict`.
Stream IDs must distinguish source generations when a supervisor resets its
sequence; separate supervisors must not share one checkpoint stream.

Only acknowledge upstream input after `Commit` succeeds. Each commit appends a
`change_outbox` row; readers use its highest sequence as a cache revision. The
outbox is not a queue of executable turn-end events. Reconstructing history must never directly dispatch follow-up
messages, permissions, routines, or plugin actions. Completion reconciliation
will need separate idempotent application receipts.

## Retention

Source deletion marks a record `SourceDeleted`; it never removes Ocman's
history. Readers include source-deleted records. Disabled or disconnected
platform instances must not be removed by cascading database deletion.
statev2 has no purge operation; the `purged_session` table is retained for
schema compatibility only.

## Next increments

1. Direct durable event capture, including acknowledgment gating and ephemeral
   streamed content. Current invalidations schedule committed-source snapshot
   repairs; they do not persist every transient event or change supervisor acks.
2. Typed usage, relationships, blob storage and ingestion health reporting.
3. Shadow comparison against existing readers, including usage and relationships.
4. Datastore-only session reads and snapshot-to-SSE cursor attachment.
5. Analytics and downstream consumer cutover, then removal of redundant caches.

Detailed multi-instance discovery, configuration, and UI behavior remain open.
Hub replication and cross-platform session resumption are outside this increment.

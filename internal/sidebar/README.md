# Compact session browsing

The conversation sidebar calls `GET /api/sessions?view=sidebar`. Its local
platforms share one SQL query. The regular session API keeps its richer
accounting response for pages that explicitly need it.

`state_conv.db` owns `session_sidebar`, updated in the ingestion transaction
after normalized messages change. It contains scalar display metadata, latest
visible message metadata, native-child visibility and project-directory scope.
It never stores a second conversation payload. The appended migration backfills
the projection without resetting history or ingestion checkpoints.

The read connection opens both databases read-only. It attaches the authoritative
`state.db` as `core`, joining catalogue publication/purge/source checks and user
flags directly instead of asynchronously copying them. Routine and Factory
origin fields support `origin` and `originId` filters before pagination. Seen,
pin, archive and snooze flags use indexed joins. Unread counts use a covering
message-timestamp index, not JSON payloads or accounting aggregates.

An owner-local module copies platform event caches into `sidebar_observation`
every 500 ms. It never lists transcripts, contacts supervisors or discovers
directories. Observations carry an adapter generation and a three-second lease;
a restart, a disconnected stream or an expired lease cannot establish busy.
Only pending prompts enter the bounded, source-scoped native ancestor walk.

Branch observations are persisted when the owner's existing Git-info endpoint
samples a directory. Older branch responses cannot overwrite newer samples.
The sidebar renders the stored branch while fresh Git information is unavailable.

Remote owners receive the same query through the optional `SessionsReq`
sidebar payload. Existing richer RPC behavior remains unchanged when absent.
For remote rows the hub reads its display preferences in one additional metadata
query, passes pinned/snoozed identities and seen cutoffs to the owner, and applies
hub-local flags without consulting native platforms. Remote origin tags survive.
Notifications and the running count use the compact path too. Conversation
headers reuse their already-loaded metadata and totals rather than running
another platform-wide summary/enrichment pass.

Conversation and metadata readers have a separate read-only WAL pool from the
history writer, so a large ingestion transaction cannot queue an interactive
read behind its connection. Both snapshot commits and stream deltas update the
sidebar projection atomically. The appended v12 migration repairs v11 projections
that predated stream maintenance without touching history or checkpoints.

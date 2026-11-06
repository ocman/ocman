---
title: Scheduled maintenance
weight: 49
---

The Background tasks page shows Ocman's owner-local scheduled maintenance alongside
history imports and preparation work. Open it through the `bgt` counter. Connected
remote owners contribute their own records and owner badges.

Each recurring job keeps one entry with its latest result, progress counters,
cadence, run count and next-check hint. An idle recurring job is not running work.
The counter counts only queued/running entries. Restarting Ocman clears stale
next-check hints until the loops run again. Jobs that stop also clear their hints.
The hint is approximate: slow passes, event-triggered runs and timer alignment can
change when a check actually starts.

## Tracked jobs

| Work | Schedule |
| --- | --- |
| Archived worktree cleanup | Startup and hourly |
| Auto-archive | Daily, with shorter retries while session preparation is pending |
| Expired composer attachments | Startup and daily |
| Snoozed session wake-ups | Startup and every minute |
| Project index and connected remote project inventories | Existing refresh intervals |
| Watched pull requests and scheduled routines | Existing watch/check intervals |
| Idle project runtime checks and queued-message recovery | Startup and every minute |
| Agent approval subscription reconciliation | Existing rescan interval |
| Inbox permission reconciliation | Existing reconciliation interval |
| Web notification candidate checks and delivery | Existing polling interval and delivery wake-ups |
| Webhook inbox polling and delivery-history cleanup | Per inbox every 15 seconds; history hourly |
| Plugin conversation/session reconciliation, delivery scans and history pruning | Existing reconciliation, retry and prune intervals |
| Session-reference recovery and pending platform reload checks | Existing recovery/reload intervals |
| Managed runtime health, machine runtime supervision and installed-version checks | Existing health/supervision intervals |
| Database-size sampling, subscription usage and analytics metrics | Existing sampling intervals, when enabled |
| Consumed history journal segment pruning | Hourly per journal source |

Cleanup reports removed, retained and already-removed checkouts plus unavailable
owners. Other jobs report counters where available. A percentage appears only
when the producer knows the total. A successful check does not mean it changed
anything. Webhook and notification delivery failures use generic task errors so
task history does not retain private endpoints or credentials.

## Internal timers

Transport heartbeats, SSE keepalives, connection retries, debounce delays,
subscription expiry, process shutdown grace periods and lock/capacity waits are
not standalone maintenance tasks. Provider-internal history indexing and Factory
dispatch retain their existing domain status rather than creating recurring
Background tasks entries. History ingestion already records actual sync work;
its idle journal/feed polls do not create receipts.

Observations do not enqueue another execution or bypass mutation fences. Queued
messages, routine runs and agent executions keep their own recovery records.

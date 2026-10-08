---
title: Routines
weight: 4
---

Routines save a prompt for a project. Run one by hand or give it a schedule.
Choose how each routine uses OpenCode sessions:

- **New session** starts a fresh session for every run. This is the default.
- **Reuse session** starts a session on the first run and continues it later.
- **Existing session** continues a session you select from the project.

A session a routine starts is titled with the routine's name and launch time (for example
`Daily check 2026-09-30 15:04`) and tagged as a
routine session. The session sidebar hides routine sessions and their children
until you check **Show routines** under **Filter sessions**. Opened and pinned
sessions stay visible through these filters and search, but must match the selected project. Existing sessions a
routine continues stay yours and are not tagged.

## Create and manage routines

Open **Routines** from the main navigation, then select **New routine**. Enter
a unique name, the prompt, the target project, session behavior, and a schedule.
The searchable agent and model lists use values seen in that project. Select
the default option to let OpenCode choose. You can also disable a routine
without deleting it.

Name, prompt, project, schedule, and enabled status stay visible at the top.
Expand **Session and model**, **After a run**, or **Permissions** for optional
settings. These groups start collapsed when you create or edit a routine.

Use **Edit** to change any of these fields. The change applies to future runs.
Existing history keeps the name, prompt, project, session behavior, agent,
model, and trigger recorded when each run started.

Select a routine row to open its editable settings and run history in the side
drawer.

When a run fails or is interrupted, ocman adds an Inbox item with the run
status and the session's final assistant message. Successful runs stay out of
the Inbox unless you turn on **Notify in the Inbox after a successful run**
under **After a run** (`notify_on_success` in the MCP `routines` tool). A run
uses the setting that was in force when it started.

## Webhook deliveries

A webhook inbox only captures deliveries. Routines subscribe to an inbox and
decide which deliveries run them, so one inbox (for example, every event from
a Forgejo repository) can drive several routines.

Agents can also create inboxes and subscribe routines through the `webhooks`
MCP tool. See [Webhook triggers](../mcp/#webhook-triggers) for the setup sequence
and author-only GitHub PR filters.

1. Save the relay URL and enrollment token once under **Settings → Webhooks**.
   An empty relay URL falls back to the share relay.
2. Open **Routines → Webhook inboxes → New inbox**, name it, and optionally set
   a shared secret and the header that carries it. Forgejo can send an
   `Authorization` header: use `Authorization` with a `Bearer …` secret.
   Copy the ingestion URL into the provider's webhook settings.
3. Edit a routine and pick the inbox as its **Trigger**, then add
   conditions. The trigger is either a schedule or one webhook inbox, not
   both; choosing a webhook clears the schedule.

Conditions combine header and RFC 6901 JSON Pointer predicates with AND.
They support only `equals`, `is one of`, `exists`, and `is missing`; scripts
and regular expressions are not evaluated. With no conditions, every delivery
runs the routine. A matching delivery sends the routine prompt unchanged,
followed by this fixed untrusted-data envelope:

```json
{
  "inboxId": "...",
  "deliveryId": "...",
  "method": "POST",
  "query": {},
  "receivedAt": 0,
  "headersPath": "/…/ocman/webhook-deliveries/<hash>.headers",
  "bodyPath": "/…/ocman/webhook-deliveries/<hash>.body",
  "bodyBytes": 0
}
```

Headers and body are not inlined. Both paths are absolute `0600` files:
`headersPath` holds a JSON object of canonical header name to values (for
example `{"X-Forgejo-Event":["pull_request"]}`) and `bodyPath` holds the raw
request body. The session reads only what it needs, for example
`jq -r '."X-Forgejo-Event"[0]' "<headersPath>"` or `jq -r .number "<bodyPath>"`.
Allow the routine's session to read `<state dir>/webhook-deliveries/*` (an
`external_directory` permission rule), and query the files with `jq` rather
than reading them whole. The files follow the 30-day history retention below.

A webhook runs its routine on the machine that received it. A subscription can
only point at a routine that runs on this machine; a routine later moved to a
remote is skipped with "routine runs on another machine".

Select an inbox row to open its drawer: the ingestion URL, key reset and
revoke, renaming, replacing or removing the shared secret (the URL stays the
same; this needs a relay with the `PUT /inboxes/{id}/secret` endpoint), the subscribed routines, and the ten most recent deliveries with their
filtered headers, body, and what happened for each subscriber (ran, no match,
skipped because the routine is disabled, or failed). Use it to check a
condition against what the provider actually sent. The shared secret is filled in and
masked; use the eye button to inspect it. Inboxes created before ocman kept
the secret show that one is set but can't display it until you enter it again.
The relay only removes a secret on an explicit `"secret": ""`; a request
without a `secret` field is rejected. **Redeliver** replays a
logged delivery as a new one: every subscriber is matched and run again, and
the replay gets its own log entry. It carries the original headers, query and
body; deliveries logged before the query was kept replay without one.

Deliveries are logged, not posted to the Inbox. The Inbox only hears about
failures: a delivery ocman gave up on after 8 attempts, or a routine that
could not be started for a delivery.

Disabled routines are skipped, while deleted routines cancel queued
dispatches. Revoking an inbox removes its subscriptions. Delivery and dispatch
history is retained for 30 days.

Use **Delete** to remove a routine from the UI and stop future scheduled runs.
Deletion is soft: ocman retains the routine and its run history in `state.db`.

The **Delete after a successful run** option performs the same soft delete
only after a run succeeds. A failed run leaves the routine available.

Enable **Archive session after a successful run** to hide completed sessions
from the active session list. This is off by default and applies to scheduled
and manual runs. Failed or interrupted runs stay visible. The routine and its
schedule remain active, and history still links to the archived session.
The setting is captured when each run starts; edits apply to future runs.

## Run a routine

The Routines page and the composer provide different actions:

- **Run now** starts or selects a session according to the routine and sends the saved prompt.
  It records a manual run in the routine's history.
- `/routines` in a session composer opens a picker and inserts the chosen
  prompt into the composer. It does not send the prompt, start a session, or
  create a history entry. Edit the text or send it like any other message.

Scheduled runs use the same session behavior as manual runs.

## Schedule semantics

- **None** has no automatic run time. Use **Run now** when needed.
- **Timeout** accepts a delay from now. When you save the routine, ocman turns
  that relative delay into one absolute due time. Restarting ocman or editing
  the routine later does not restart the original countdown. Saving an edit
  with a timeout calculates a new due time from that save. A timeout missed
  while ocman is offline expires at startup instead of running late.
- **Once** runs at one absolute date and time. The selected local time is
  saved as an absolute instant.
- **Cron** uses a standard five-field cron expression plus an IANA timezone,
  such as `0 9 * * 1-5` with `Europe/Brussels`. Ocman calculates each next
  occurrence in that timezone after the previous run settles.

The scheduler checks due routines every five seconds. Disable a routine to
stop automatic runs without deleting it. Webhook delivery polling is
independent of the scheduler and runs on the machine that owns the inbox,
which also runs the subscribed routines.

## Completion and history

A run remains **running** while its session is active or waiting for the
session to settle. Session **done** marks the run **success**. Session
**error** marks it **failure**. An interrupted or unavailable session marks
the run **interrupted**. Dispatch errors, including
an unavailable host or a session that could not be created or prompted, also
produce a failed history entry.

Open a routine row to see when each run started, whether it was manual or
scheduled, and its current result. Failed runs show their error. Runs with a
linked session include an **Open** link. The drawer shows the newest 50 runs;
**Load older runs** pages further back.

The routine list refreshes every five seconds. `GET /api/routines` includes
each routine's newest run as `latestRun`, so the list's status and **Last run**
columns need no history request. `GET /api/routines/{id}/history` returns one
page, newest first: `limit` (default 50, at most 200), and for older runs the
last shown run's `beforeCreatedAt` and `beforeId`.

Run records preserve their routine snapshot and remain available after a
restart. Ocman resumes observing linked running sessions after startup. If a
restart interrupted dispatch before ocman linked a session, the run fails
instead of starting a duplicate session.

## Recover legacy Workflow data

Older ocman versions stored DAG definitions and runs in `workflow_*` tables.
Those tables and rows remain inert in `state.db`. Routines do not read,
execute, migrate, or delete them. A DAG cannot be converted losslessly to one
prompt because its nodes, dependencies, approvals, commands, outputs, and
branching have no equivalent in a routine.

There are no compatibility APIs. Use SQLite in read-only mode to inspect or
export the old data. Stop ocman first if you plan to copy the database.

```sh
DB="$HOME/.local/share/ocman/state.db"
sqlite3 -readonly "$DB" ".tables 'workflow_*'"
sqlite3 -readonly "$DB" ".dump 'workflow_*'" > legacy-workflows.sql
sqlite3 -readonly -json "$DB" "SELECT * FROM workflow_definition ORDER BY created_at;" > workflow-definitions.json
sqlite3 -readonly -json "$DB" "SELECT * FROM workflow_version ORDER BY workflow_id, revision;" > workflow-versions.json
sqlite3 -readonly -json "$DB" "SELECT * FROM workflow_run ORDER BY created_at;" > workflow-runs.json
sqlite3 -readonly -json "$DB" "SELECT * FROM workflow_node_run ORDER BY run_id, position;" > workflow-node-runs.json
sqlite3 -readonly -json "$DB" "SELECT * FROM workflow_node_attempt ORDER BY run_id, node_id, seq;" > workflow-node-attempts.json
```

The full authored DAG is in `workflow_version.definition_json`. To list the
saved definitions in a readable form:

```sql
SELECT d.name, v.revision, v.created_at, v.definition_json
FROM workflow_definition AS d
JOIN workflow_version AS v ON v.workflow_id = d.id
ORDER BY d.name COLLATE NOCASE, v.revision;
```

Open that query without modifying the database:

```sh
sqlite3 -readonly -header -column "$DB"
```

Paste the SQL at the SQLite prompt. Extract any useful agent prompt from
`definition_json`, then create a routine manually. Keep commands, approval
steps, dependencies, and output handling outside the routine prompt unless
the agent can safely perform them itself.

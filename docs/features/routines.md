---
title: Routines
weight: 4
---

Routines save a prompt for a project. Run one by hand or give it a schedule.
Choose how each routine uses OpenCode sessions:

- **New session** starts a fresh session for every run. This is the default.
- **Reuse session** starts a session on the first run and continues it later.
- **Existing session** continues a session you select from the project.

## Create and manage routines

Open **Routines** from the main navigation, then select **New routine**. Enter
a unique name, the prompt, the target project, session behavior, and a schedule.
The searchable agent and model lists use values seen in that project. Select
the default option to let OpenCode choose. You can also disable a routine
without deleting it.

Use **Edit** to change any of these fields. The change applies to future runs.
Existing history keeps the name, prompt, project, session behavior, agent,
model, and trigger recorded when each run started.

Select a routine row to open its editable settings and run history in the side
drawer.

Use **Delete** to remove a routine from the UI and stop future scheduled runs.
Deletion is soft: ocman retains the routine and its run history in `state.db`.

The **Delete after a successful run** option performs the same soft delete
only after a run succeeds. A failed run leaves the routine available.

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
stop automatic runs without deleting it. Webhook triggers are deferred and
are not supported.

## Completion and history

A run remains **running** while its session is active or waiting for the
session to settle. Session **done** marks the run **success**. Session
**error** marks it **failure**. An interrupted or unavailable session marks
the run **interrupted**. Dispatch errors, including
an unavailable host or a session that could not be created or prompted, also
produce a failed history entry.

Open a routine row to see when each run started, whether it was manual or
scheduled, and its current result. Failed runs show their error. Runs with a
linked session include an **Open** link.

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

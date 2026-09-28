# Project model list with quota fallthrough

Each project may have an ordered list of models. `models[0]` is the
project default, used when a prompt names no model. When a provider runs
out of tokens, ocman cools that provider down and continues the session
on the next model in the list.

This replaces an earlier, larger design (per-session overrides, audit
tables, episode state) that was dropped during review.

## Storage

- The existing global `setting` KV table in `state.db`. No new table, no
  migration.
- Key `project:<repoRoot>`; value `{"models":[...],"off":bool}`.
- Directories fold through `state.ProjectRootForDirectory` (the same fold
  project archiving uses), so a worktree shares its repository's entry.
- An empty list deletes the row. `off` disables fallthrough while keeping
  the list configured.
- Local-only: no multi-remote routing, no `hostsvc.Host` seam, no gRPC.

## API

- `GET /api/project/settings?dir=<abs>` → `{"models":[],"off":false}` when
  unconfigured.
- `POST /api/project/settings` `{directory, models, off}` → `{"ok":true}`,
  or 400 on invalid input with nothing persisted.

Validation (`state.ValidateProjectModels`, shared by every caller): each
entry is `provider/model` with no whitespace, at most 300 characters, at
most 10 entries, no duplicates. Entries are **not** checked against the
live provider catalogue; a disconnected provider may reconnect later and
the runtime skips unusable models anyway.

## Model selection

One place: `sessionsvc.Service.SendMessage` (every prompt converges
there), mirrored into `ExecuteCommand` for slash commands and compaction.

1. Empty model → `models[0]`.
2. Provider cooled down → first model in the list whose provider is not.
   This deliberately overrides an explicit user pick; configuring a list
   is that instruction. No substitution when `off`, and no substitution
   when nothing in the list is usable (the request fails visibly).

## Cooldown state

One in-memory map, `provider → cooledUntil`. No per-session state, no
`tried` set, no audit table, no climb-back rule. A restart forgets it,
costing one wasted failure.

Every recorded cooldown is floored at the patience threshold (default
5 min). That floor terminates the walk: each failure cools its provider,
so the chain makes at most one attempt per model, then stops with an
"all models exhausted" notice. The fallback duration is 15 min when no
reset time is known. Both are global settings.

## Detection

Fed by the headless event watcher.

- **Retry park:** `session.status {type:"retry"}` whose `next` is further
  away than the patience threshold, or whose `action.reason` is
  `account_rate_limit` / `free_tier_limit`.
- **429 turn:** session idle after an assistant `APIError` with status
  429; reset time from the persisted `responseHeaders`
  (`retry-after`, `anthropic-ratelimit-*-reset`), then retry `next`, then
  the fallback.
- Auth failures and context overflow are ignored: another model fixes
  neither.

The cooldown is recorded synchronously on the idle edge **before** the
queue flush is enqueued, so a held message cannot drain into a dead
provider.

## Continuation

After a failed turn ocman sends a continuation prompt; selection picks the
replacement model. OpenCode drops the errored assistant message from the
model-facing context, but its file writes remain on disk, so the prompt
must tell the model to inspect the working tree before redoing work. That
sentence is the entire mitigation. Every assistant message already records
its model, so the switch documents itself.

## Explicitly rejected

Revert-and-resend, `ask` mode, per-session override,
`POST /api/session/:id/model`, writing `opencode.json`, proactive
switching on subscription-usage percentages.

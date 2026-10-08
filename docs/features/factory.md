---
title: Factory delivery
weight: 9
---

## Factory sessions

The session list tags every session a Factory attempt ran in (and its direct
subagents) with `factoryAttemptId`. The session sidebar hides those sessions and
their children until you check **Show factory** under **Filter sessions**, and a
conversation loads Factory recovery choices only when it carries the tag. The
sidebar never polls the Epic list; regular conversations no longer poll it either.

## Cost and token usage

The Epic page shows input, output, cache-read and cache-write tokens, recorded
billing, and estimated model-price cost. Totals include the attempt's session
and all its subagent descendants, split into Plan, Implement, Verify and
Deliver. Expand **Attempts** to inspect each run, including retries and failed
attempts. The Queue shows the same Epic phase totals and the current Attempt's
usage beside active work.

Recorded billing stays separate from the estimate, since subscription sessions
may record zero billing while still consuming tokens. Usage refreshes every ten
seconds. If a session is unavailable or deleted, the page marks totals incomplete
instead of presenting its usage as zero. Attempts that never launched have zero
usage.
Factory implements a Work Epic sequentially across one or more local Git
repositories. It keeps an independent shared branch and workspace for each
project, then runs a separate Project Delivery session for every changed
project. This model is defined by [ADR 0008](../adr/0008-coordinate-factory-work-across-projects.md).

Dispatch wakes on completion, implementation-session idle events, and changes
that make work ready, such as approval, resume, graph edits, or capacity changes.
A startup scan and five-minute recovery scan catch missed events. Retry deadlines
and outstanding merge gates retain their own timers; they do not wait five minutes.
Factory views refresh every 15 seconds.

## Projects and Issue targets

Every Work Epic has an **Epic project set**. Its original Epic project is the
permanent default; when creating the Epic, you can select additional local Git
repositories and acknowledge local command execution for each one. Attached
remote-host projects are not supported. A secondary project can be removed only
before work or Delivery history exists for it, and the Epic project cannot be
removed.

Planning and unblock sessions start in the Epic project and may read every
admitted project, but no other external project. Each executable Issue has one
project target, shown in Issue and queue views. A plan or graph mutation may set
the target explicitly; an omitted target inherits the Epic project, and a target
outside the admitted set is rejected. Dispatch, capacity accounting, retries,
and recovery use the Issue target. Factory still runs only one implementation
Attempt per Epic at a time.

Each project has its own Factory workspace lineage: branch, base, checkpoint,
Delivery remote, pull request, recovery, and resume history do not cross project
boundaries. An implementation session writes in its target workspace. It gets
read access to other admitted projects and explicit edit-deny rules for those
paths. Shell commands remain available, however, so this is agent policy rather
than filesystem or container isolation.

## Use an existing plan

If a regular session has already planned the work, ask it to import that plan
into Factory. It creates an Epic, reads `issues` to find the root `mol` ID,
then calls `import_proposal` with the ticket breakdown and dependency edges.
No dedicated planning session starts.

The imported proposal still requires **human approval**. Review its tickets,
choose an implementation model, then approve through the action card or Epic
page. Importing alone does not materialize tickets or start implementation.

Include the scope, verification steps, and relevant decisions in the ticket
descriptions and proposal rationale. Implementation sessions do not inherit the
original conversation.

Every implementation node that isn't a reference must list `acceptanceCriteria`:
1–20 verifiable outcomes, written before any code. Factory adds them to the
Issue description as a checklist, so the approved plan contains them. The
verification session receives every Issue's checklist and must report each
criterion as PASS or FAIL. Proposals without criteria are rejected.

Import is available before any Factory attempt has been claimed. It marks
the planning work complete, so a competing planner cannot start while the proposal is
awaiting review. You can request a revision and have the original session call
`import_proposal` again. Each import creates an immutable revision; approval
must match its exact revision and hash. Approved or rejected plans cannot be
replaced through import. Proposal history and the pending gate survive restart.

## Formula workflows

A Formula is a YAML workflow. Its named `steps` contain their own `kind`,
`needs`, `prompt`, and `config`. Dependencies point to prerequisite step names,
following the convention used by GitHub Actions jobs. This is ocman's schema,
not a GitHub Actions workflow file.

```yaml
version: 2
name: Tracer
steps:
  plan:
    kind: planning
    prompt: |
      Inspect the repository, clarify requirements, and propose focused tasks.
  approve:
    kind: approval
    needs: [plan]
  implement:
    kind: implementation
    needs: [approve]
    config:
      concurrency: 1
    prompt: |
      Implement the assigned task and run its checks.
  verify:
    kind: verification
    needs: [implement]
    config:
      commands: [make lint, make test]
    prompt: |
      Review the combined changes and run the repository-required checks.
      Request recovery if a required check fails.
  deliver:
    kind: delivery
    needs: [verify]
    prompt: |
      Summarize the changes and verification results in the final pull request.
```

Use **Customize Tracer** in Factory configuration to start from the current
built-in workflow, `ocman/tracer@4`. Expand **Formula source** to edit the YAML
in a full-width, 15-line editor that can be resized vertically. Validate and
preview it before saving an immutable revision. The graph includes implementation
and every post-implementation check. Expand the implementation phase in an Epic's
graph to see its planned tasks.

The built-in planner writes the design into the proposal rationale before
approval: the problem and desired behaviour, the contracts and data models, and
the program design (new or changed types, signatures, and call flow). It splits
the work into vertical slices, each of which can be checked on its own.
Verification reads that approved rationale and reports where the code departs
from it. The delivery PR lists those departures first, then maps each change to
its design item and acceptance criterion. Epics pinned to `ocman/tracer@3` keep
their original prompts.

The first version supports one planning step, one implementation group, and one
final delivery step. An initial plan approval must precede implementation. Add
further human approvals before implementation or after checks, and any number
of verification steps, by naming them and
declaring `needs`. Every step must lead to final delivery; cycles, unknown keys,
missing dependencies, and disconnected steps are rejected.

The implementation group expands into the approved tasks. It succeeds only when
all applicable required tasks succeed. Runnable optional tasks also finish before
verification; deferred optional tasks do not hold the group open. Verification and delivery run once per
changed project in that project's assigned worktree. A dependent step waits for
all project instances of its prerequisites. Failed or paused checks block
delivery; recovery or retry is explicit. Additional approval steps appear in
the action inbox and require a user decision. A user can reconsider a rejected
step with **Approve step**, which rechecks its prerequisites.

Agent steps require a multiline prompt of at most 32 KiB. Optional `name` gives
a step a display label. `config.model` accepts a `provider/model` reference and
overrides the approved implementation model for that step. The implementation
group supports `config.concurrency: 1`, reflecting the shared workspace's
sequential execution. Planning can supply `config.scope_expansion_prompt` for
additive replanning. Verification steps can declare `config.commands`
(described below). Other configuration keys are rejected.

## Verification

A verification session is a **validator**: a fresh session that did not write
the code. Its profile denies edits. It keeps shell access so it can run checks.
If the shared branch HEAD moved from the last implementation checkpoint, Factory
rejects its completion. Unless you pick a verification model (via the Epic's
**Models** controls or the step's `config.model`), it uses the preferred
planning model (Fable or Astra), not the implementation model. If neither is
available, it uses the runtime default. The prompt tells it to review the
combined diff and check each acceptance criterion. It must also look for work
that passes checks without doing the task, such as skipped or deleted tests and
new suppressions.

`config.commands` is a block of shell commands defined in the Formula. The
agent still does its own review. When it calls `complete_attempt`, Factory:

1. answers `checks_running`, and the agent ends its turn;
2. runs each command in order with `sh -c` in the project's worktree, with
   `CI=1` and a one-hour limit for the whole block;
3. fails the run if the worktree was dirty before it started, or if HEAD or
   the worktree changed while it ran. Keep build output in `.gitignore`;
4. scans the diff against the remote-tracking target branch for deleted test
   files, edited test or lint configuration (`Makefile`, `package.json`,
   linter, test-runner, and CI config), and newly added skip,
   lint-suppression, or type-ignore markers;
5. sends the exit codes, the last 4 KB of output per command, and the scan
   findings back to the validator as a new message.

With those results, the validator either calls `request_recovery` or calls
`complete_attempt` again. Factory accepts completion only if every command
exited 0 at that exact HEAD, and records the passed commands in the Attempt
summary. A failed result is reported once. The next `complete_attempt` reruns
the checks, for example after a flaky test or a human resume.

Commands run with ocman's own privileges, not the agent's permission profile.
Forge tokens (`GITHUB_TOKEN`, `GH_TOKEN`, `FORGEJO_TOKEN`, `GITEA_TOKEN`) and
`OCMAN_*`/`OTEL_*` variables are removed from their environment. The commands
come from the Formula revision pinned to the Epic. Agents with the `factory`
MCP tool can save Formulas and create Epics, so review a Formula's `commands`
the way you review CI configuration before approving its plan.

A run stops when its attempt ends (retry, cancel, or another terminal outcome)
and when ocman shuts down; it is never orphaned. Results are kept in memory.
After a restart, Factory tells every validator started before the restart to
call `complete_attempt` again, which reruns the checks.

Known limits: the scan is a line-based heuristic that points the validator at
suspicious changes; it does not block completion. Remote-host projects cannot
run Formula checks. Factory does not run them on remote hosts yet.

Factory adds runtime context, repository restrictions, attempt credentials, and
completion instructions to each prompt. Changing a prompt does not bypass
permissions, approvals, or commit/PR validation. Planned tasks cannot inject
delivery nodes into a YAML workflow; delivery is declared in the Formula.
Within an implementation group, use ordinary task dependencies. The legacy
cross-project merge-gate plan format remains available only to older revisions.

The built-in workflow has been converted to YAML. Historical TOML revisions stay
readable for pinned Epics; their source and hashes are not rewritten. New Epics
use the selected YAML revision. Runtime step definitions and their project
instances persist across restarts. Source edits, including comments and formatting,
create a new revision rather than silently restoring an earlier source file.

## Planning and implementation models

New planning sessions prefer an available Fable or Astra model. If neither is
available, they use the runtime default.

Plan approval includes an implementation model selector in the planning session,
Epic page, and inline action card. It suggests an available Opus or Sol model for
balanced implementation, or Sonnet or Terra when only a fast model is available.
Choose a fast model for speed, any other available model, or Runtime default.
An amendment initially retains the saved implementation model. While its gate
is open, a new selection, including Runtime default, replaces that saved choice.

Approval saves the exact model reference with the Plan gate and copies it into
each implementation Attempt, including final delivery. Retrying approval keeps
the original choice. MCP callers can supply `implementation_model` as a
`provider/model` reference to `approve_plan` after confirming it with the user.

The Epic page's **Models** controls pick a planning, implementation, and
verification model for that Epic at any time (`POST
/api/factory/epics/<id>/models`). A choice there wins over the workflow step's
`config.model` and the approval-time choice; Default falls back to them.
Delivery uses the implementation model. Each Attempt freezes its model when it
is claimed, so a change mid-implementation affects only work that has not
started.

The picker combines catalogs from all projects attached to the Epic and searches
model display names as well as IDs. **Refresh models** reloads those catalogs.
If a configured model is missing after a configuration change, restart that
project's OpenCode instance and refresh. You can still select an unlisted model
by entering its full `provider/model` ID in the search field and choosing **Use**.
Selection does not guarantee that the model is configured in every Epic project.

## Implementation checkpoints

Each implementation session tests its change, commits it, pushes the shared
branch, and calls `factory` with `action: "complete_attempt"`, `attempt_id`,
`attempt_token`, and `summary`. It omits `pr_url`.

Ocman checks that the worktree is clean, that its HEAD matches its upstream, and
that it includes the previously accepted commit. The successful Attempt stores
the branch, target branch, and commit SHA. Before launching the next Issue,
Ocman checks that the branch still matches that checkpoint. Missing branches,
dirty worktrees, or unexpected commits require reconciliation rather than a
silent reset. Checkpoints survive an ocman restart.

Factory preserves session history when stopping, retrying, or cancelling work,
including failed prompt delivery. Stopping a session aborts its running turn
without deleting the session or its transcript.

Factory archives its sessions once their work is finished: an implementation
or delivery session when its Attempt completes, a planning session when its
plan is approved or applied as a scope replan, and every remaining Factory
session of an Epic when that Epic is closed. Later replies from the agent do
not unarchive them.

An active session that stops making progress without completing or requesting
recovery is paused by a watchdog. Factory probes live attempts at most once a
minute. After 30 minutes without activity in the session or its direct
subagents, it opens a recovery gate: resume with guidance, retry, or cancel.
The 30 minutes count from the attempt start, or from the last time a human
resumed it. Sessions waiting on a permission or question prompt, and validators
waiting on Formula checks, are not counted as idle.

Recovery gates link to the stuck attempt's conversation through **Inspect
recovery session**. The same question, choices or text input, and resume,
retry, and cancel controls appear above that session's composer, so you can
inspect the conversation and decide there. A failed resume keeps the saved
response available for retry.

Implementation sessions run in the Issue's target project workspace. They may
read other projects admitted to the Epic, but path-specific permission rules
deny edits there. Shell access remains enabled, so these rules enforce agent
policy; they are not filesystem isolation.

## Project Deliveries

Factory runs a required Project Delivery for each project with executable work.
YAML workflows wait for the declared checks and approvals across all changed
projects. Older revisions allow Delivery as soon as the project's own required
work succeeds. Starting it seals only that project. The delivery model reviews that
project's combined changes, runs the required checks, and creates a review-ready
PR against its recorded remote and target branch. It searches for an existing
open PR from the same branch into the recorded target first, so retries can reuse
a PR created before the session was interrupted.

Delivery admission rechecks the current graph in the claim transaction, so new
required work cannot be skipped between a dependency refresh and launch. Ready
optional tasks run before delivery too. Deferred or blocked optional work does
not delay delivery; after delivery succeeds, it remains visible as not applicable
and cannot start on the delivered branch.

The Delivery session completes with `pr_url`. Ocman validates the repository,
source branch, target branch, and pushed HEAD. It rejects draft, closed, merged,
and cross-fork PRs. Project status and Delivery lineage are shown on the Epic.
The Epic shows **Ready for review** only after every required Project Delivery
succeeds; Factory does not merge the PRs.

New work discovered before Factory observes a Project Delivery PR merge refreshes
that Delivery on the same branch and PR. After a merge gate records the merge,
new work creates a successor Delivery from the updated target branch and
publishes a new PR. Existing merge gates stay pinned to their original Delivery;
newly created gates use the latest Delivery lineage.

## Cross-project dependencies

An ordinary dependency can cross projects and is satisfied by the blocker's
validated implementation checkpoint. Use a **merge-gated dependency** when a
downstream Issue must wait for an upstream Project Delivery PR to merge. Factory
polls the forge and records the observation; only a merged PR at the recorded
Delivery commit satisfies the gate. Open, draft, closed-unmerged, changed, and
temporarily unavailable PR observations keep the dependent Issue blocked and
show the reason rather than failing it automatically.

## Graph changes and scope expansion

When an agent finds a gap, it can use `mutate_graph` to add tickets or change
dependencies and descriptions on work that has not started. Factory saves an
immutable revision of the updated graph and shows an approval card. Review it
in the card or Epic page. Approval must match the exact revision and hash;
another edit makes an older approval stale.

Agents submit the complete change in one call. The `mutation_json` payload uses
`action: "batch"`, the Epic's `epicId`, an ordered `mutations` array, and
`rationaleMarkdown` explaining what changed and why. Each edit inherits the
outer Epic and actor. All edits commit together as one approval revision;
an invalid edit rolls back the entire batch. The Markdown rationale appears
beside the approval controls in both the conversation card and the Epic page.
Nested batches and edits to another Epic are rejected. Single edits remain
supported for existing callers.

The approval preview uses that frozen graph, including parent groups and
dependencies on other Epics. External endpoints are labelled with their Epic
ID. The initial proposal must materialize before an agent can edit live work;
until then, submit a revised proposal so its planned tickets are preserved.

On the Epic page, open **Plan**. The pending proposal expands automatically
and has an **Approve revision** button. Its graph marks added Issues and
connections, comparing amendments with the last approved revision rather than
an intermediate unapproved edit. The **Graph** tab also marks added Issues
while approval is pending. An already approved plan shows **Approved** and
**No new proposal is awaiting approval** instead of another approval button.
The first amendment compares against the materialized graph captured before
the edit, so new containment and reparenting connections are highlighted too.

Missing work can also be proposed during verification, after the implementation
group has completed. Adding a task reopens that group; completed tasks stay
closed. An active validator can finish its current scope, but Factory runs a
fresh verification of the approved amendment before delivery, even if the new
task leaves the commit checkpoint unchanged.

New dispatch pauses until that graph is approved. Already-running work can
finish, and completed Issues and checkpoints remain intact. The implementation
model is retained. Requesting a revision keeps dispatch paused; rejecting the
graph closes the Epic and cancels its remaining open work. Pending approval and
proposal history survive a restart. Agents cannot approve their own changes.
Approving a graph revision does not complete any active scope-planning session.
Each dispatch captures its Issue text, Formula ancestry, and verification
criteria in the same transaction that claims its approved revision.
Agent graph edits are available only after initial planning and materialization;
before the first proposal, submit that proposal rather than editing live work.
Blockers proposed for removal remain effective for dependent work in other Epics
until the source amendment is approved. Previously approved removals remain applied
when later amendments are proposed.

An implementation agent that discovers another required repository can request
it with the project path and a reason. Factory pauses and preserves that Attempt
until a user decides. Approval verifies the local Git repository, requires a new
local-execution acknowledgement, admits it to the project set, and launches an
additive replan of the remaining work. The new graph needs a separate human
approval before any new tickets run. Completed Issues and checkpoints remain
intact, and the paused Issue retries only after graph approval and any new blockers succeed.
Rejection records the response and resumes the same implementation session.

If a PR is merged before Factory records Delivery, the agent must request
recovery. Factory accepts that merged PR only after a human resumes the same
Attempt with `Force-complete the delivery attempt using merged PR #<number>`.
The PR number and target branch must match, and the assigned worktree must
still be clean at its pushed checkpoint.

Delivery uses the same recovery gates, launch retries, and Reopen control as
implementation. A Delivery failure does not reopen completed implementation
Issues. Structural graph edits are paused while a Delivery is running. Once all
required project Deliveries succeed, additional work belongs in a new Epic.

## Existing work

Already-running implementation Attempts can complete with a clean, pushed commit
and no `pr_url`, even if their original prompt required a PR. Factory recovers the
session's actual worktree branch, including numbered successor branches, and
records its checkpoint for subsequent Issues. This completion does not consult
or change an existing PR. Only Project Delivery requires a review-ready PR.

Previously completed PR-based results remain valid history. When launching new
work from that history, Factory resolves the prior PR once to preserve the shared
branch and completed work. On Forgejo, converting an existing PR to draft uses
the `WIP: ` title prefix.

---
title: Factory delivery
weight: 9
---

Factory implements a Work Epic sequentially across one or more local Git
repositories. It keeps an independent shared branch and workspace for each
project, then runs a separate Project Delivery session for every changed
project. This model is defined by [ADR 0008](../adr/0008-coordinate-factory-work-across-projects.md).

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

Include the scope, acceptance criteria, verification steps, and relevant decisions
in the ticket descriptions and proposal rationale. Implementation sessions do
not inherit the original conversation.

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
built-in workflow, `ocman/tracer@3`. Expand **Formula source** to edit the YAML
in a full-width, 15-line editor that can be resized vertically. Validate and
preview it before saving an immutable revision. The graph includes implementation
and every post-implementation check. Expand the implementation phase in an Epic's
graph to see its planned tasks.

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
additive replanning. Other configuration keys are rejected.

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

Approval saves the exact model reference with the Plan gate and copies it into
each implementation Attempt, including final delivery. Retrying approval keeps
the original choice. MCP callers can supply `implementation_model` as a
`provider/model` reference to `approve_plan` after confirming it with the user.

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

## Scope expansion

An implementation agent that discovers another required repository can request
it with the project path and a reason. Factory pauses and preserves that Attempt
until a user decides. Approval verifies the local Git repository, requires a new
local-execution acknowledgement, admits it to the project set, and launches an
additive replan of the remaining work. Completed Issues and checkpoints remain
intact, and the paused Issue retries only after any new blockers succeed.
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

---
title: Factory delivery
weight: 9
---

Factory implements a Work Epic sequentially on one shared branch, then runs a
separate delivery session to publish the final pull request.

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

## Final delivery

Factory adds a required delivery Issue with dependencies on the required work.
Once implementation finishes, the Epic shows **Implementation complete, delivery
pending**. The delivery model reviews the combined changes, runs the required
checks, and creates a review-ready PR. It searches for an existing open PR from
the same branch into the recorded target first, so retries can reuse a PR that
was created before the session was interrupted.

Delivery admission rechecks the current graph in the claim transaction, so new
required work cannot be skipped between a dependency refresh and launch. Ready
optional tasks run before delivery too. Deferred or blocked optional work does
not delay delivery; after delivery succeeds, it remains visible as not applicable
and cannot start on the delivered branch.

The delivery session completes with `pr_url`. Ocman validates the repository,
source branch, target branch, and pushed HEAD. It rejects draft, closed, merged,
and cross-fork PRs for final delivery. Success shows **Ready for review**; it
does not merge the PR.

If a PR is merged before Factory records delivery, the agent must request
recovery. Factory accepts that merged PR only after a human resumes the same
Attempt with `Force-complete the delivery attempt using merged PR #<number>`.
The PR number and target branch must match, and the assigned worktree must
still be clean at its pushed checkpoint.

Delivery uses the same recovery gates, launch retries, and Reopen control as
implementation. A delivery failure does not reopen completed implementation
Issues. Structural graph edits are paused while delivery is running and after
successful delivery; additional work after delivery belongs in a new Epic.

## Existing work

Already-running implementation Attempts can complete with a clean, pushed commit
and no `pr_url`, even if their original prompt required a PR. Factory recovers the
session's actual worktree branch, including numbered successor branches, and
records its checkpoint for subsequent Issues. This completion does not consult
or change an existing PR. Only final delivery requires a review-ready PR.

Previously completed PR-based results remain valid history. When launching new
work from that history, Factory resolves the prior PR once to preserve the shared
branch and completed work. On Forgejo, converting an existing PR to draft uses
the `WIP: ` title prefix.

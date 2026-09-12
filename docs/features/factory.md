---
title: Factory delivery
weight: 9
---

Factory implements a Work Epic sequentially on one shared branch, then runs a
separate delivery session to publish the final pull request.

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

Already-running Attempts retain their original PR-based completion contract.
When existing work first adopts checkpoints, Factory resolves its prior delivery
PR once to preserve the shared branch and completed work. Later implementation
handoffs no longer depend on that PR's status or branch metadata.

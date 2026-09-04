# Ocman

Ocman coordinates coding-agent sessions and the work required to deliver
software changes.

## Language

**Work Epic**:
A mutable goal-oriented group containing Issues and one or more Mols, which may come from different Formulas.
_Avoid_: Formula instance, Molecule

**Factory graph identifier**:
A human-addressable, hierarchical identifier: a Work Epic uses its goal's lowercase ASCII word-initial prefix (up to ten characters; `epic` if empty) and a unique four-character cryptographic base32 suffix; Mols and Issues append immutable monotonic child indices from their parent (for example, `flp-s3de.1.1`).
_Avoid_: Opaque Factory ID, Formula-key ID

**Issue**:
A node in the Factory graph, classified as an Epic, Mol, Plan, Materialization, task, or Gate and connected by explicit hierarchy and dependency relationships.
_Avoid_: Ticket, Work Item

**Issue lifecycle status**:
One of `open`, `in_progress`, `deferred`, `retry_wait`, `closed`, or `pinned`. Dependency waiting is derived, while external conditions are modeled as Gates rather than a `blocked` status.
_Avoid_: Dispatch state, closed outcome

**Mol**:
One composable group of Issues created from an exact Formula revision inside a Work Epic or parent Mol. Child Mols retain independent lifecycles.
_Avoid_: Work Epic, workflow run

**Plan**:
An Issue containing an immutable, machine-readable graph manifest for human review, optionally accompanied by Markdown rationale. Approval records acceptance, closes the Plan and ends its planning Attempt, but does not itself create Mols, Issues, or dependencies.
_Avoid_: Separate plan object, planning mode

**Materialization Issue**:
An Issue designated to apply one exact approved Plan revision through a deterministic, validated, atomic, idempotent graph mutation. Materialization requires no agent session. It is also satisfied when executable work is added by hand to an approved Work Epic, since the graph then already exists.
_Avoid_: Plan Issue, implicit graph expansion

**Materialization provenance**:
An authorization record linking a materialized Issue or dependency to the Plan revision, transaction, and manifest element that created it. Shared graph elements may have multiple provenance records.
_Avoid_: Creation metadata, agent attribution

**Manifest key**:
A stable author-provided local identity for a manifest node or edge that makes repeated materialization safe.
_Avoid_: Runtime Issue ID, array position

**Gate**:
An Issue whose lifecycle closure records both the general terminal outcome and a Gate-specific resolution. Only a satisfying resolution releases the success path.
_Avoid_: Special workflow stage, implicit approval, closed-means-satisfied

**Parent-child relationship**:
The structural placement of one Issue beneath an Epic or Mol. It does not affect readiness or propagate blocking relationships.
_Avoid_: Dependency, inherited blocker

**Blocking dependency**:
An explicit success-sensitive relationship between executable or Gate Issues in the local Factory graph, including across Work Epics, that is satisfied only when an ordinary blocker closes successfully or a Gate additionally records a satisfying resolution. A failed, cancelled, or rejected blocker leaves the edge unsatisfied. Blocking never propagates through parent-child hierarchy.
_Avoid_: Parent-child relationship, implicit group lock

**Cross-Work Epic dependency**:
A user-managed local Factory dependency between executable or Gate Issues in different Work Epics. It uses ordinary blocking or failure semantics, is globally cycle-checked, and is explained on the waiting Issue without changing either Work Epic's local progress or closure requirements.
_Avoid_: Cross-host dependency, shared Work Epic progress

**Failure dependency**:
An explicit `on_failure` relationship that activates recovery work only when an ordinary blocker closes as failed or a Gate closes as rejected. Success or cancellation makes the recovery branch not applicable rather than blocked.
_Avoid_: Completion dependency, outcome expression

**Conditional Issue**:
An Issue activated by a conditional dependency such as `on_failure`. An inactive branch is excluded from container closure requirements.
_Avoid_: Optional Issue, deferred Issue

**Not applicable**:
A derived dispatch state for an unexecuted conditional Issue whose activation condition is impossible. It is excluded from readiness, progress, and closure requirements while retaining the derivation reason and triggering outcome for audit.
_Avoid_: Issue status, cancelled outcome

**Deferred Issue**:
An unfinished Issue excluded from dispatch until a timestamp or explicit resume. It remains an unsatisfied blocker for Issues that depend on it.
_Avoid_: Scheduled Issue, pinned Issue

**Retry-wait Issue**:
An unfinished Issue excluded from dispatch during automatic retry backoff (1s, 30s, 5m), with an attempt count and wake timestamp after which it returns to open. Once the budget is exhausted the Issue closes as failed; only a user can reopen it.
_Avoid_: Deferred Issue, blocked Issue

**Stuck Work Epic**:
An open Work Epic whose closure is blocked while no Issue is ready, running, or waiting to retry and no Gate is open, so nothing can change without a user action. Surfaced in the action inbox.
_Avoid_: Failed Work Epic, blocked Work Epic

**Terminally blocked**:
A derived dispatch state for an open Issue whose success-sensitive dependency can no longer be satisfied. It is non-dispatchable until explicitly replanned, replaced, or closed as cancelled or failed; failed blocker IDs and reasons are retained for explanation.
_Avoid_: Issue status, closed outcome

**Pinned Issue**:
Persistent reference context that never dispatches, never blocks dependents, and does not count toward progress. Only reference Issues may be pinned; converting executable work to a pinned reference requires a newly approved Plan revision.
_Avoid_: Deferred Issue, permanent blocker

**Planning Session**:
The agent session launched at the target project's canonical root when the dispatcher claims a ready Plan Issue. Its durable Attempt records the session ID; the agent may inspect the project, submit immutable proposal revisions, and maintain local Factory Issues unless they are in progress or closed. Operator decisions remain user actions.
_Avoid_: Planner, planning mode

**Planning Work**:
Agent work performed before Plan approval under the planning permission profile to inspect one target repository and contribute to a proposal.
_Avoid_: Implementation planning, planning subtask

**Draft Issue**:
An open, unassigned Issue that may still be changed or deleted before Plan approval. Any local agent session may create or maintain one.
_Avoid_: Proposed task, temporary ticket

**Plan approval**:
The successful, approved resolution of a human Gate for one immutable Plan proposal revision. Changing the proposal invalidates approval; a revision request leaves the Gate open, while terminal rejection closes it as failed and must not unblock dependents.
_Avoid_: Start button, planner completion

**Plan revision**:
The immutable, auditable Plan output accepted by one Plan approval Gate.
_Avoid_: Current plan, latest graph

**Required Issue**:
An executable Issue marked by an approved manifest as counting toward progress and necessary for successful closure of its ancestor containers.
_Avoid_: Blocking dependency, direct child

**Optional Issue**:
An executable Issue reported separately from required progress that does not guard successful container closure. Closing its Mol cancels unclaimed optional work; active optional work must first complete, be cancelled, or be reparented.
_Avoid_: Deferred Issue, best-effort retry

**Reference Issue**:
A non-dispatchable Issue excluded from progress and closure checks.
_Avoid_: Pinned Issue, optional work

**Soft-deleted Issue**:
An unstarted Issue removed from the active graph while retained with its audit history and provenance; deleting a Mol soft-deletes all descendants and their incident dependencies atomically.
_Avoid_: Hard-deleted Issue, cancelled Issue

**Terminal Issue outcome**:
The succeeded, failed, or cancelled result recorded separately when any Issue closes, together with its reason. Gates additionally record their typed resolution.
_Avoid_: Issue status, Gate result

**Plan amendment**:
A new planning Issue that pauses new claims while unclaimed work is revised for another Plan approval.
_Avoid_: Edit approved plan, reopen planning

**Factory permission profile**:
A named, versioned capability boundary selected explicitly for one executable Issue.
_Avoid_: Permission mode, role

**Factory attempt**:
One execution of an Issue under its frozen permission profile.
_Avoid_: Retry session, inherited session

**Paused Factory Attempt**:
A preserved session and worktree halted on a Recovery Gate. It releases implementation capacity until the Gate resumes it, starts a fresh retry, or cancels its Issue.
_Avoid_: Failed Attempt, active capacity slot

**Factory project**:
A repository on one execution host targeted by Factory work. Its worktrees and symlinked paths belong to the same Factory project as their canonical main repository.
_Avoid_: Working directory, repository name

**Authority escalation Gate**:
A human decision requested when Factory work needs authority beyond its selected permission profile.
_Avoid_: Automatic permission widening, approval prompt

**Authority exception**:
A human grant for one exact privileged request, target, and Factory attempt.
_Avoid_: Allow always, profile override

**Local execution acknowledgement**:
An operator's acceptance that a target's repository-controlled commands run as the local ocman user without process isolation.
_Avoid_: Sandbox, trusted agent

**Permission request**:
A request from a coding-agent platform for authority to perform one specific action.

**Auto-approval evaluation**:
Ocman's attempt to resolve a Permission request without human intervention, using a cached decision or a model Judgment.

**Evaluation result**:
The outcome of an Auto-approval evaluation, independent of how the Permission request is ultimately resolved.

**Judgment**:
A model's safe or unsafe assessment of a Permission request.

**Manual preemption**:
A user's approval or rejection of a Permission request before its Judgment completes.

**Permission resolution**:
The final approval, rejection, or cancellation of a Permission request, independent of its Evaluation result.

**Factory dispatcher**:
The single active hub-local coordinator that discovers and starts ready Issues.
_Avoid_: Worker, scheduler

**Ready Issue**:
An open, unassigned Issue whose explicit blockers are satisfied and for which execution capacity is available.
_Avoid_: Queued item

**Recovery Gate**:
A first-class Gate Issue dynamically created in the owning Mol when an implementation agent explicitly cannot safely continue. It links to the paused Factory Attempt, is excluded from progress and closure requirements, and records a human decision to resume, retry, or cancel.
_Avoid_: Error prompt, failed Attempt

**Factory-owned claim**:
An Issue assignment made by the Factory dispatcher and linked to a durable Factory attempt.
_Avoid_: Stale claim, orphan

**Observed Gate**:
A Gate satisfied only when ocman verifies a typed predicate against external state.
_Avoid_: Automated Issue, polling task

**Factory workspace**:
The persistent project-specific working context shared sequentially by one Work Epic's executable Issues.
_Avoid_: Item workspace, temporary checkout

**Delivery base**:
The target branch and exact revision from which a project's delivery work begins, frozen when its plan is approved.
_Avoid_: Latest base, current default branch

**Merge-gated dependency**:
A cross-project dependency that becomes satisfied only when the upstream change is reported merged by its forge.
_Avoid_: PR dependency, delivery ordering

**Workspace handoff**:
The validated boundary where an Issue leaves its Factory workspace clean and advances it with commits, or reports that no change was required.
_Avoid_: Agent completion, auto-commit

**Delivery identity**:
The immutable association between one project's Delivery, its deterministic branch, Factory workspace, and pull request.
_Avoid_: Branch convention, PR mapping

**Workspace residue**:
Committed or uncommitted changes left by an unsuccessful Factory attempt and preserved for explicit recovery.
_Avoid_: Dirty files, failed changes

**Delivery refresh**:
An explicit system Issue that pushes a corrected workspace revision to an existing Delivery branch and pull request.
_Avoid_: Delivery retry, reopened Delivery

**Delivery divergence**:
A mismatch between expected and observed Delivery history that ocman cannot safely reconcile automatically.
_Avoid_: Remote update, branch drift

**Delivery revision**:
The exact commit published to a Delivery branch and used to evaluate revision-specific provider checks.
_Avoid_: Latest PR head, current branch

**Built-in Formula**:
An immutable, ocman-shipped template that seeds Planning Work, required Gates, and delivery policy for a Mol.
_Avoid_: Default workflow, editable preset

**Custom Formula**:
A hub-local editable copy of a Built-in Formula for creating Mols.
_Avoid_: Project formula, overridden built-in

**Formula revision**:
An immutable saved version of a Custom Formula, identified by its formula, revision, content, and originating Built-in Formula version.
_Avoid_: Current formula, saved draft

**Formula composition**:
A Mol step that pins an exact child Formula revision, binds every parameter explicitly, and carries a stable step ID. The approved manifest records the resolved revision and bindings; pouring rejects missing revisions, unresolved parameters, and composition cycles.
_Avoid_: Latest Formula reference, implicit parameter inheritance

**Formula instantiation**:
The transactional creation of a Mol inside a Work Epic from one exact Built-in Formula version or Custom Formula revision and validated inputs.
_Avoid_: Workflow start, live formula link

**Formula policy**:
The constraints carried by a Formula revision that Planning Work must satisfy when expanding the draft Work Epic graph.
_Avoid_: Workflow rules, hidden defaults

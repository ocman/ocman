# Ocman

Ocman coordinates coding-agent sessions and the work required to deliver
software changes.

## Language

**Planning Session**:
The first claimed Agent Work Item of a Work Epic, where the user and coding agent turn an initial goal into a validated plan of unclaimed Work Items.
_Avoid_: Planner, planning mode

**Planning Work**:
Agent Work performed before Plan approval under the planning permission profile to inspect one target repository and contribute to the Work Epic plan.
_Avoid_: Implementation planning, planning subtask

**Draft Work Item**:
An open, unassigned Work Item that may still be changed or deleted before Plan approval.
_Avoid_: Proposed task, temporary ticket

**Plan approval**:
The user's acceptance of a validated Work Epic plan, freezing its execution boundary before autonomous dispatch begins.
_Avoid_: Start button, planner completion

**Plan revision**:
The immutable, auditable version of a validated Work Epic graph accepted by one Plan approval.
_Avoid_: Current plan, latest graph

**Plan amendment**:
A new Planning Work Item that pauses new claims while unclaimed work is revised for another Plan approval.
_Avoid_: Edit approved plan, reopen planning

**Factory permission profile**:
A named, versioned capability boundary selected explicitly for one executable Work Item.
_Avoid_: Permission mode, role

**Factory attempt**:
One execution of a Work Item under its frozen permission profile.
_Avoid_: Retry session, inherited session

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
The single active hub-local coordinator that discovers and starts ready Work Items.
_Avoid_: Worker, scheduler

**Ready Work Item**:
An open, unassigned Work Item whose blockers are satisfied and for which execution capacity is available.
_Avoid_: Queued item

**Recovery Gate**:
A human decision requested when Factory execution cannot safely continue or retry unattended.
_Avoid_: Error prompt, failed item

**Factory-owned claim**:
A Work Item assignment made by the Factory dispatcher and linked to a durable Factory attempt.
_Avoid_: Stale claim, orphan

**Observed Gate**:
A Gate satisfied only when ocman verifies a typed predicate against external state.
_Avoid_: Automated Work Item, polling task

**Factory workspace**:
The persistent project-specific working context shared sequentially by one Work Epic's executable Work Items.
_Avoid_: Item workspace, temporary checkout

**Delivery base**:
The target branch and exact revision from which a project's delivery work begins, frozen when its plan is approved.
_Avoid_: Latest base, current default branch

**Merge-gated dependency**:
A cross-project dependency that becomes satisfied only when the upstream change is reported merged by its forge.
_Avoid_: PR dependency, delivery ordering

**Workspace handoff**:
The validated boundary where a Work Item leaves its Factory workspace clean and advances it with commits, or reports that no change was required.
_Avoid_: Agent completion, auto-commit

**Delivery identity**:
The immutable association between one project's Delivery, its deterministic branch, Factory workspace, and pull request.
_Avoid_: Branch convention, PR mapping

**Workspace residue**:
Committed or uncommitted changes left by an unsuccessful Factory attempt and preserved for explicit recovery.
_Avoid_: Dirty files, failed changes

**Delivery refresh**:
An explicit System Work Item that pushes a corrected workspace revision to an existing Delivery branch and pull request.
_Avoid_: Delivery retry, reopened Delivery

**Delivery divergence**:
A mismatch between expected and observed Delivery history that ocman cannot safely reconcile automatically.
_Avoid_: Remote update, branch drift

**Delivery revision**:
The exact commit published to a Delivery branch and used to evaluate revision-specific provider checks.
_Avoid_: Latest PR head, current branch

**Built-in Formula**:
An immutable, ocman-shipped template that seeds Planning Work, required Gates, and delivery policy for a Work Epic.
_Avoid_: Default workflow, editable preset

**Custom Formula**:
A hub-local editable copy of a Built-in Formula for creating Work Epics.
_Avoid_: Project formula, overridden built-in

**Formula revision**:
An immutable saved version of a Custom Formula, identified by its formula ID, revision number, content hash, and originating Built-in Formula version.
_Avoid_: Current formula, saved draft

**Formula instantiation**:
The transactional creation of a new Work Epic's initial work graph from one exact Built-in Formula version or Custom Formula revision and validated inputs.
_Avoid_: Workflow start, live formula link

**Formula policy**:
The constraints carried by a Formula revision that Planning Work must satisfy when expanding the draft Work Epic graph.
_Avoid_: Workflow rules, hidden defaults

# Worktree Sessions - Requirements

## Overview

A new ocman feature that lets the user launch an OpenCode session inside a
dedicated git worktree, on demand, from the dashboard. Each session gets its
own working tree, eliminating the file-edit / rebuild / staging conflicts
that occur when two concurrent agent sessions operate on the same checkout.

The feature surfaces as:

1. A `/wt` (or `>wt`) command in the existing command palette.
2. A new per-project **Worktrees** view at `/projects/:id/worktrees`.

Both entry points run the same flow: pick a branch, ocman creates the
worktree on disk, launches `opencode --port 0` inside it via the existing
tmux launcher, and navigates the user to the new tmux session.

## Goals

- Eliminate cross-session interference (file edits, rebuild thrash, staging
  conflicts) by isolating each agent session in its own working tree.
- Keep worktree creation **on-demand** — no implicit per-session worktrees.
- Keep the user in flow: a single command palette interaction creates the
  worktree, launches OpenCode, and switches the user to the new tmux
  session.
- Make the feature idempotent: re-running `/wt` against the same branch
  does the right thing (reuse worktree, reuse tmux session, don't
  double-launch opencode).
- Reuse existing ocman primitives: command palette, tmux launcher, gitinfo
  worktree resolution, project page.

## Target Users

The single user / maintainer of ocman, running multiple concurrent OpenCode
sessions against the same repository. The feature is designed around that
workflow; it is not (yet) generalised for multi-user / multi-platform
scenarios.

## Functional Requirements

### FR-1: `/wt` command in the command palette

- **Description**: Add a new entry to the command palette that initiates a
  "create worktree session" flow. Discoverable by typing `wt` or `>wt`
  after opening the palette (Alt+Space). Listed under the existing
  static / scoped commands alongside `new-project`.
- **Acceptance Criteria**:
  - Typing `wt` (or `>wt` / `:wt`) in the palette surfaces a command labelled
    something like "New worktree session".
  - The command is **hidden** when the OpenCode platform is not registered
    (capability-gated, not branched on `platform === 'opencode'`).
  - Selecting the command opens the worktree-creation form (FR-3).

### FR-2: Worktrees view per project

- **Description**: A new page at `/projects/:id/worktrees` that lists all
  git worktrees of the project's repository, with their associated
  sessions and quick actions.
- **Acceptance Criteria**:
  - Discoverable from the project page (link/tab "Worktrees").
  - Lists every worktree of the project's repo (discovered via
    `git worktree list`), including the main checkout.
  - Each row shows: branch, worktree path, associated session(s) (if any
    ocman sessions have a `cwd` matching the worktree path), and last
    activity.
  - Per-row actions: **Open in tmux**, **Open in VS Code** (reuses the
    existing handlers).
  - A primary action **New worktree session** opens the same form as
    FR-3 with the project pre-selected.
  - The view is **hidden** when OpenCode is not registered.

### FR-3: Worktree-creation form

- **Description**: A small form (modal or palette sub-mode) that collects
  the inputs needed to create the worktree.
- **Inputs**:
  - **Project** — defaults to the current project when invoked from a
    project/session page; otherwise prompts the user to pick one.
  - **Branch name** — required, free-form text.
  - **New branch?** — checkbox, defaults to **on**.
  - **Base ref** — visible when "New branch?" is on; defaults to the
    repo's default branch (HEAD of the upstream / `main` / `master`,
    whichever resolves), user-overridable.
- **Acceptance Criteria**:
  - The form validates that branch name is non-empty.
  - When "New branch?" is off, base ref input is hidden.
  - Submitting the form calls the new backend endpoint (FR-4) and shows
    a "Creating worktree…" spinner until it returns.
  - On success, the user is **navigated to the new tmux session** (via
    the existing tmux switch flow).
  - On error, a toast notification surfaces the error message
    (e.g. "branch already checked out in another worktree").

### FR-4: Backend "create worktree and launch" endpoint

- **Description**: A new POST endpoint (e.g. `/api/worktree/create-and-launch`)
  that:
  1. Resolves the project's repo root.
  2. Computes the worktree path:
     `<repo-parent>/.worktrees/<repo-name>/<slugified-branch>/`.
  3. If the path already exists and is a valid worktree for that branch,
     **skips creation** and proceeds to launch (idempotent reuse).
  4. Otherwise runs `git worktree add` with appropriate flags:
     - `git worktree add -b <branch> <path> <base-ref>` for new branches.
     - `git worktree add <path> <branch>` for existing branches; if the
       branch only exists on a remote, git auto-creates a tracking branch.
  5. Reuses or creates a tmux session named after the worktree path
     (existing `launchOpencodeInTmux` flow); **skips the
     `send-keys "opencode --port 0"`** when the tmux session already
     existed (avoids double-launching opencode).
  6. Returns the tmux session name + worktree path so the frontend can
     navigate.
- **Acceptance Criteria**:
  - Method is **POST**, **localhost-only** (consistent with the existing
    tmux launch endpoint).
  - Returns 200 with `{ tmux_session, worktree_path, branch, reused }` on
    success.
  - Returns 4xx with a clear error message when:
    - The branch is already checked out in another worktree.
    - `git` or `tmux` is not on PATH.
    - The project ID does not resolve to a known repo.
  - Branch slugification: `/` → `-`, lowercase, strip unsafe chars; the
    branch name itself is **not** slugified, only the on-disk path.
  - Behaviour against a dirty main checkout is **silent** — no warning,
    since isolation is the point.

### FR-5: Capability gating

- **Description**: All UI affordances for this feature are gated through
  `/api/capabilities` and `useCapabilities()`, not via
  `platform === 'opencode'` branching. This is enforced by
  `scripts/check-platform-branching.sh`.
- **Acceptance Criteria**:
  - A new capability flag (e.g. `worktree_sessions`) is exposed on
    `/api/capabilities`, set to `true` only when an OpenCode adapter is
    registered AND `git` + `tmux` are on PATH.
  - The `/wt` palette command, project-page Worktrees link, and
    `/projects/:id/worktrees` route are hidden when the capability is
    `false`.
  - `make lint` continues to pass (no platform string comparisons in the
    frontend).

## Non-Functional Requirements

### NFR-1: Idempotency

- **Description**: Running `/wt` repeatedly with the same project + branch
  must converge to "session running in the worktree", not error or
  duplicate.
- **Acceptance Criteria**:
  - Re-running `/wt` against an existing valid worktree reuses it.
  - Re-running against an existing tmux session reuses it without
    re-sending `opencode --port 0`.
  - The user lands in the same tmux session in both first-run and
    re-run cases.

### NFR-2: Localhost-only

- **Description**: The backend endpoint that creates worktrees and
  launches tmux processes must reject non-localhost requests, matching
  the existing tmux endpoints.
- **Acceptance Criteria**: requests from non-localhost origins return
  403 (or whatever the existing `requireLocalhost` wrapper returns).

### NFR-3: Performance / responsiveness

- **Description**: The worktree-creation flow happens in a single
  blocking request from the user's perspective, but should feel
  responsive.
- **Acceptance Criteria**:
  - The frontend shows a "Creating worktree…" spinner from form-submit
    until the backend responds.
  - Typical end-to-end latency on a healthy repo is under ~3 s
    (subjective; no hard SLO).

### NFR-4: No regression in existing flows

- **Description**: The existing tmux launcher, project page, and
  command palette continue to work identically when the worktree
  feature is unused.
- **Acceptance Criteria**: existing Go and frontend tests continue to
  pass; CI green.

## Data Requirements

- **No new persistent state**. Worktrees are discovered on demand via
  `git worktree list`. ocman's own `state.db` does not track them.
- The existing `gitinfo` package already resolves worktree roots and
  computes diffs per-worktree; sessions launched in a worktree appear
  as a normal directory-keyed project in ocman's existing model.
- Slugification of the branch name for the on-disk path is a pure
  function (no state).

## Integration Points

- **`git` CLI** — `git worktree list`, `git worktree add` (with `-b` for
  new branches, optional base ref). Required on PATH.
- **`tmux` CLI** — same dependency the existing launcher already has.
  Required on PATH.
- **OpenCode CLI** — invoked indirectly via `tmux send-keys
  "opencode --port 0"` (existing helper). Required on PATH on the
  spawning host.
- **Existing ocman internals**:
  - `internal/server/tmux.go` — `launchOpencodeInTmux`,
    `tmuxSessionNameForPath`, `listTmuxSessions`.
  - `internal/gitinfo/` — repo / worktree resolution.
  - `internal/server/handlers_git.go` — for any worktree-list
    endpoint additions.
  - `frontend/src/components/CommandPalette.tsx` — adds the new
    static (or scoped) command.
  - `frontend/src/lib/uiStore.ts` — possibly a new `paletteMode`
    value for the worktree-form sub-flow.
  - `frontend/src/lib/api.ts` + `apiStore.ts` — typed client for the
    new endpoint.

## Constraints

- **Platform**: v1 supports OpenCode only. Claude Code is explicitly
  out of scope (FR-5 / NFR-2).
- **Tooling**: requires `git`, `tmux`, and `opencode` on PATH on the
  host running ocman. Same OS support as today (macOS / Linux).
- **No exported Go**: all new code lives under `internal/` per repo
  convention.
- **No platform branching in the frontend**: feature must be
  capability-gated; `make lint` enforces this.
- **No new persistent ocman state**: nothing added to `state.db`.

## Assumptions

- The user always wants the worktree under
  `<repo-parent>/.worktrees/<repo-name>/<slug>/`. *Architect to confirm
  this works on systems where `<repo-parent>` is not user-writable.*
- Branch-name slugification is straightforward enough that v1 can ship
  with a simple rule (lowercase, `/` → `-`, strip non-`[a-z0-9-_]`).
  *Architect to confirm corner cases (Unicode, leading dashes, very
  long names).*
- `git worktree add <path> <branch>` against a remote-only branch
  reliably auto-creates a local tracking branch on the user's git
  version. *Architect to confirm minimum git version.*
- Re-running `tmux send-keys "opencode --port 0"` against an existing
  tmux session that already has opencode running would launch a
  second instance; therefore the new endpoint must skip the
  send-keys when the tmux session pre-existed. *Architect to confirm
  this is the right detection signal.*
- The "default base ref" picker can resolve the upstream's default
  branch using `git symbolic-ref refs/remotes/origin/HEAD` or fall
  back to the current branch. *Architect to confirm the resolution
  rule.*
- A worktree-launched session shows up as a distinct project in
  ocman's existing project listing (because its `cwd` is unique).
  *Architect to confirm this matches what's already happening for
  manually-created worktrees today.*

## Out of Scope

- **Deleting or pruning worktrees** from the ocman UI. Users manage
  cleanup themselves via `git worktree remove` or by deleting the
  directory.
- **Claude Code support.** A future iteration could add a
  `launchClaudeInTmux` helper and broaden the capability flag.
- **Configurable worktree base path.** v1 hard-codes
  `<repo-parent>/.worktrees/<repo-name>/<slug>/`.
- **Moving an already-running session into a worktree.** Sessions are
  cwd-locked at launch; this feature only creates fresh sessions.
- **Auto-archiving worktrees** when their associated session ends or
  is archived.
- **Cross-project / global worktrees view.** v1 ships a per-project
  view only.
- **Custom slugification rules** beyond the simple lowercase + `/`-to-
  `-` mapping.
- **Persistent tracking of which worktrees ocman created** (no
  `state.db` schema change). Discovery is always via `git worktree
  list`.

## Success Criteria

Subjective: the maintainer (primary user) stops experiencing rebuild
thrash and staging conflicts in their day-to-day work when running
multiple concurrent OpenCode sessions on the same repo. No formal
metric; ship-it-and-see.

Secondary indicators that the design is working:

- `/wt` becomes the maintainer's default way to start a new agent
  session (anecdotal).
- No new bug reports about cross-session interference.
- The feature does not require frequent maintenance / fixups after
  initial ship.

## Open Questions

- **Default base ref resolution**: should we prefer
  `origin/HEAD` (upstream's default), the current branch of the main
  checkout, or always `main`? Listed under Assumptions for the
  Architect to settle.
- **Slugification edge cases**: do we need to handle Unicode branch
  names, very long names, or names that collide post-slugification
  (e.g. `feature/login` and `feature-login` both slug to
  `feature-login`)? Probably not in v1, but worth noting.
- **Tmux "already running opencode" detection**: is "tmux session
  pre-existed" a sufficient signal, or do we need to actually grep
  the pane contents / process list to confirm opencode is alive
  before skipping the send-keys?
- **Project page integration**: should the project page get a
  permanent "Worktrees" tab, or just a button/link that navigates to
  `/projects/:id/worktrees`? Architect/UX call.
- **What does the worktrees view show for the *main* checkout?** Is
  it a normal row in the list (just labelled "main"), or visually
  distinguished? v1 default: show it as a normal row.

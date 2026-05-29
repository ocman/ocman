# PR & Issue Sidebar - Requirements

## Overview

A new sidebar in ocman that surfaces **pull/merge requests** and **issues**
for the current project, fetched live from the project's upstream forge
(GitHub or Forgejo). Each entry can be expanded inline to view its title,
description, and a link to the upstream, and launched as a "handle this
PR/Issue" prompt in a new OpenCode session or git worktree.

The feature reuses ocman's existing session-launch primitives
(`split_to_session`, `split_to_worktree` via the MCP server / direct API)
and the worktree flow (`spec/worktree-sessions/`). It does **not** attempt
to be a full forge client — read + launch only, no write actions in v1.

## Goals

- Give the user a fast, in-context view of open work (PRs + issues) for the
  project they're already looking at, without switching to the browser or a
  separate CLI.
- Keep "from PR/Issue to working agent session" to a single click: pick an
  item, hit the launch button, and a new OpenCode session starts with a
  context-rich prompt about that PR/Issue.
- Support both **GitHub** and **Forgejo** as first-class upstreams, with
  graceful detection per project (and per remote).
- Reuse the existing CLI tooling (`gh`, `tea`) for auth so the user doesn't
  re-enter tokens that already work elsewhere.
- Make the sidebar fully optional and self-hiding: if no supported upstream
  is detected for the current project, it doesn't appear at all.

## Target Users

The single user / maintainer of ocman, working across multiple repositories
hosted on GitHub and Forgejo, who frequently triages PRs and issues and
wants to spin up parallel agent sessions to handle them.

## Functional Requirements

### FR-1: Upstream detection per project

- **Description**: For the project derived from the active session's
  directory, ocman inspects all configured git remotes and detects which
  ones point at a supported forge (GitHub or Forgejo). Detection runs on
  project context change and on manual refresh.
- **Acceptance Criteria**:
  - All remotes (not just `origin`) are inspected via `git remote -v` (or
    equivalent).
  - A remote is classified as **GitHub** if its host is `github.com` (HTTPS
    or SSH form).
  - A remote is classified as **Forgejo** if its host matches a login
    configured in `tea`'s `~/.config/tea/config.yml`.
  - Remotes that don't match either are ignored.
  - Detection is exposed via a dedicated endpoint:
    `GET /api/project/<dir>/upstreams` returning
    `{ upstreams: [{ remote, host, type, repo }] }` where `type` is
    `"github" | "forgejo"` and `repo` is `"owner/name"`.
  - The detection result is cached for the lifetime of the project view
    (re-runs only on project switch or manual refresh).
  - If the endpoint returns an empty list, the sidebar pane is hidden
    entirely (FR-2).

### FR-2: Conditional sidebar visibility

- **Description**: The PR/Issue sidebar is only rendered when at least one
  supported upstream is detected for the current project.
- **Acceptance Criteria**:
  - The sidebar mounts when ≥1 GitHub or Forgejo remote is detected.
  - The sidebar is absent (not just empty / collapsed) when no supported
    remote is detected — no DOM, no API calls.
  - Visibility updates reactively when the active project changes.

### FR-3: Two tabs — PRs and Issues

- **Description**: The sidebar has two tabs: **PRs** and **Issues**. Each
  tab independently fetches and renders the corresponding entity type from
  all detected remotes.
- **Acceptance Criteria**:
  - Both tabs are always present when the sidebar is visible (even if one
    is empty).
  - The active tab is preserved across project switches within a session
    (best-effort, no persistence required).
  - Each tab fetches independently; switching tabs the first time triggers
    the fetch for that tab.

### FR-4: List rows

- **Description**: Each row shows the minimum information needed to triage
  the item without expanding it.
- **Acceptance Criteria**:
  - Row shows: number (`#123`), title, author, status, last-updated
    timestamp (relative, e.g. "2h ago"), labels (color chips), and
    assignees (avatars or initials).
  - Status reflects the forge state:
    - PRs: `open`, `draft`, `merged`, `closed`.
    - Issues: `open`, `closed`.
  - Rows are visually grouped by host when more than one supported remote
    exists (e.g. a "GitHub" group and a "Forgejo" group).

### FR-5: Filters

- **Description**: Each tab exposes filter controls.
- **Acceptance Criteria**:
  - State filter: `open` (default) / `closed` / `all`.
  - **Mine** toggle: when on, only show items where the current user is
    the **author OR an assignee OR a requested reviewer** (PRs only for
    "requested reviewer"; issues use author OR assignee).
  - "Current user" is resolved from the respective forge auth (e.g.
    `gh api user` / `tea` config) and cached per host for the session.
  - Default sort is **most recently updated**, descending. Sort control is
    not required in v1.
  - Filter selections are local to the tab and reset on full page reload
    (no persistence required).

### FR-6: Manual refresh, no caching

- **Description**: Data is fetched on tab open and on explicit user refresh
  only. No background polling, no persistence to ocman's state DB.
- **Acceptance Criteria**:
  - Fetch fires when a tab is opened for the first time in a project
    context.
  - A visible refresh button in the tab header re-fetches the current tab.
  - No automatic polling or interval-based refresh.
  - No write to `state.db` for PR/Issue data (the feature is stateless on
    the ocman side).
  - Switching tabs back to one already loaded shows the previously fetched
    data without re-fetching.

### FR-7: Inline detail expansion

- **Description**: Clicking a row expands it inline within the sidebar to
  show additional detail. Clicking again (or another row) collapses it.
- **Acceptance Criteria**:
  - At most one row is expanded per tab at a time.
  - Expanded content shows:
    - Title (full, wrapped if long).
    - Description / body, rendered as markdown.
    - A link to the item on the upstream forge (opens in a new tab).
    - The launch control (FR-9).
  - Comments, review threads, diffs, and CI status are **out of scope**
    for v1 (see "Out of Scope").

### FR-8: Pagination

- **Description**: The list paginates inside the sidebar so repos with many
  open items remain usable.
- **Acceptance Criteria**:
  - Each page fetches a bounded number of items (e.g. 30) per remote.
  - The UI exposes pagination controls (prev/next or "load more") visible
    in the sidebar.
  - Pagination is per-tab; switching tabs does not reset pagination of the
    other tab.
  - When multiple remotes are present, pagination is per-remote group
    (each group has its own controls).

### FR-9: Launch button — split control

- **Description**: Each expanded item has a single launch control: a split
  button whose default action launches a **new session** in the project's
  current directory, with a pulldown to choose **new worktree** instead.
- **Acceptance Criteria**:
  - The main button label is something like "Handle in new session".
  - A pulldown / dropdown on the same control offers "Handle in new
    worktree".
  - For **session** launches:
    - A new OpenCode session is started in the project's current
      directory (current branch unchanged).
    - The session prompt is the rendered template for the item type (FR-10).
  - For **worktree** launches:
    - A new git worktree is created (reusing the worktree-sessions flow)
      and a new OpenCode session is started inside it.
    - For **PRs** on a branch in the same repo: the worktree is created
      from the PR's source branch (auto-checkout).
    - For **PRs from a fork** (cross-fork): see FR-9a.
    - For **Issues**: the worktree is created from the default branch (or
      current HEAD); no auto-checkout logic.
  - Failures (worktree creation, session launch, branch fetch) surface as
    an inline error on the row with a retry option.

### FR-9a: Cross-fork PR worktree launches

- **Description**: When the user picks **worktree** launch for a PR whose
  head branch lives on a fork (not the upstream repo), ocman prompts for
  explicit confirmation before fetching the PR ref locally, then creates
  the worktree from the fetched ref.
- **Acceptance Criteria**:
  - Cross-fork is detected by comparing the PR's head repo to the base
    repo in the forge API response.
  - When the user invokes worktree launch on a cross-fork PR, a small
    confirmation prompt is shown inline (e.g. "This PR is from a fork.
    Fetch `pull/<n>/head` and create a worktree?") with Confirm / Cancel.
  - On confirm, ocman runs the forge-appropriate fetch (e.g.
    `git fetch <upstream-remote> pull/<n>/head:ocman/pr-<n>` for GitHub;
    Forgejo's equivalent ref) before creating the worktree.
  - The fetched local ref is named deterministically (e.g. `ocman/pr-<n>`)
    so re-running is idempotent.
  - Fetch failures surface as an inline error with a retry option; no
    worktree is created on failure.
  - **Session** launch (FR-9 default) is unaffected by fork status —
    it never fetches, never checks out.

### FR-10: Customizable prompt templates

- **Description**: The prompt sent to the new session is rendered from a
  user-customizable template, with separate templates for PRs and Issues.
  Templates live in ocman settings.
- **Acceptance Criteria**:
  - Two templates exist: `pr_prompt_template` and `issue_prompt_template`.
  - Both are editable in an ocman settings UI (or settings file — see
    Open Questions).
  - Supported placeholders, expanded at launch time:
    - `{number}` — PR/Issue number.
    - `{title}` — title.
    - `{body}` — description / body (markdown, as-is from the forge).
    - `{url}` — canonical link to the item on the forge.
    - `{branch}` — PR source branch (PRs only; empty for issues).
    - `{author}` — author login.
    - `{host}` — upstream host (e.g. `github.com`).
    - `{repo}` — `owner/name`.
  - Ocman ships **sensible defaults** for both templates so the feature
    works out of the box without configuration.
  - Unknown placeholders are left as literal text (no template errors).

### FR-11: Authentication chain

- **Description**: Ocman acquires forge credentials by checking
  environment variables first, then falling back to CLI-managed tokens.
  This matches the convention used by most CLI tools (env-var override
  wins).
- **Acceptance Criteria**:
  - **GitHub**: use `GITHUB_TOKEN` env var if set; otherwise call
    `gh auth token` (or equivalent inspection of `gh`'s config).
  - **Forgejo**: use `FORGEJO_TOKEN` env var if set, else `GITEA_TOKEN`
    env var; otherwise read the token (and host URL) from `tea`'s
    `~/.config/tea/config.yml`.
  - The Forgejo **host URL** discovery is independent of the token: when
    the token comes from an env var, the host URL still comes from
    `tea`'s config (matched against the git remote's host), since the
    env var carries no host information.
  - If neither source yields a token for a detected remote, that remote's
    section of the sidebar shows an auth error with a hint to run
    `gh auth login` / `tea login add` (FR-12).
  - Tokens are read on demand; never logged, never written to ocman's
    state DB.

### FR-12: Error states with retry

- **Description**: Any fetch or launch failure surfaces a clear inline
  error with a retry button.
- **Acceptance Criteria**:
  - Network failures show "Could not reach <host>" with a retry button.
  - 401/403 responses show an auth-specific message with a hint
    (`gh auth login` / `tea login add`).
  - 429 / rate-limit responses show a rate-limit message and disable
    retry until the forge-reported reset time. The wait is derived from
    `Retry-After` if present, else from `X-RateLimit-Reset` (Unix
    seconds). The remaining time is shown as a live countdown
    (e.g. "Rate limited — retry in 4m 12s"). If neither header is
    present, fall back to a 30s cool-down.
  - Other 4xx/5xx responses show the status code and a retry button.
  - Errors are scoped to the failing remote / tab; a GitHub failure does
    not break the Forgejo group, and a PR-tab failure does not break the
    Issue tab.

## Non-Functional Requirements

### NFR-1: Latency on tab open

- **Description**: The sidebar must feel responsive even though it has no
  cache.
- **Acceptance Criteria**:
  - The tab paints its skeleton/loading state within one frame of click.
  - Fetches run in parallel across remotes (not serially).
  - First page of results renders as soon as it returns, even if other
    remotes are still loading.

### NFR-2: Platform-agnostic frontend

- **Description**: Per ocman's AD-12a, the frontend must not branch on
  session platform; capability gating goes through `/api/capabilities`.
- **Acceptance Criteria**:
  - Upstream detection results are exposed via a dedicated endpoint
    (FR-1), not inferred from `session.platform` in the UI.
  - `scripts/check-platform-branching.sh` continues to pass.

### NFR-3: Localhost-only API surface

- **Description**: Any new ocman API endpoints added for this feature
  follow ocman's existing constraints.
- **Acceptance Criteria**:
  - New endpoints use `requireGET` / `requirePOST` wrappers.
  - Endpoints that proxy forge APIs (and therefore handle tokens) require
    `localhost` origin via `requireLocalhost`, matching the existing
    tmux/hook pattern.
  - Tokens never leave the Go backend; the frontend receives forge data
    only, not credentials.

### NFR-4: No state DB writes for PR/Issue data

- **Description**: The feature is stateless on the ocman side.
- **Acceptance Criteria**:
  - No new migrations in `internal/state/` for PR/Issue records.
  - Settings (prompt templates) may be persisted (see Open Questions for
    where), but PR/Issue payloads are not.

### NFR-5: Test coverage

- **Description**: Follow ocman's existing testing conventions.
- **Acceptance Criteria**:
  - Go: table-driven unit tests for upstream detection, auth resolution,
    and template rendering. Integration tests for the new HTTP handlers,
    with forge clients faked.
  - Frontend: vitest coverage for the sidebar component, filter logic,
    pagination, and the launch split-button. Playwright e2e using stable
    locators (ARIA / `data-testid`), not CSS classes.
  - `make test` and `make lint` pass.

## Data Requirements

### Entities (in-memory only)

- **Remote**: `{ name, url, host, type: 'github' | 'forgejo', repo: 'owner/name' }`.
- **PR**: `{ number, title, body, author, status, updatedAt, labels[],
  assignees[], requestedReviewers[], branch, url, host, repo }`.
- **Issue**: `{ number, title, body, author, status, updatedAt, labels[],
  assignees[], url, host, repo }`.
- **CurrentUser** (per host): `{ host, login }`.

### Settings (persisted)

- `pr_prompt_template: string`
- `issue_prompt_template: string`

Storage location is an Open Question (state DB vs. a config file). No
PR/Issue payloads are persisted.

### Data flow

1. Project context resolved → backend reads git remotes → classifies
   each as GitHub / Forgejo / unsupported.
2. Frontend opens a tab → calls backend → backend fetches forge API per
   remote in parallel using tokens from FR-11 → returns aggregated,
   normalized list.
3. User expands a row → frontend already has the body and metadata; no
   second fetch required (no comments/diffs in v1).
4. User clicks launch → backend renders the template, then calls the
   existing session/worktree launch path with the rendered prompt.

## Integration Points

- **GitHub REST API**: list pull requests, list issues, get authenticated
  user. (GraphQL is acceptable if it materially simplifies pagination /
  filters, but not required.)
- **Forgejo / Gitea REST API**: list pulls, list issues, get authenticated
  user. Endpoint derived from `tea` config.
- **`gh` CLI**: source of GitHub tokens (read via `gh auth token`).
- **`tea` config file**: source of Forgejo URLs and tokens
  (`~/.config/tea/config.yml`).
- **Existing ocman primitives**:
  - Session launch path (currently exposed via MCP `split_to_session` /
    `split_to_worktree` — backend implementation reused directly).
  - Worktree creation flow (`spec/worktree-sessions/`).
  - Project resolution from the active session's directory.
  - `/api/capabilities` for upstream-detection exposure to the frontend.

## Constraints

### Technical

- Backend in Go; pure-Go SQLite (`modernc.org/sqlite`); no CGo. Forge API
  calls use `net/http` (with `otelhttp` instrumentation per ocman
  convention).
- Frontend in React + TypeScript + Vite; state via Zustand; routing via
  react-router-dom.
- Markdown rendering for the body must reuse whatever markdown renderer is
  already present in the ocman frontend (no new heavyweight dependency).
- Must comply with AD-12a (no `session.platform === 'opencode'` style
  branching).

### Business / scope

- Single-user, self-hosted ocman; no multi-tenant auth.
- Read-only forge access in v1.

### Team

- Single maintainer / developer; favor simple, incremental implementation.

## Assumptions

The following are assumptions made during requirements gathering. The
**Architect** should validate them before implementation begins.

- **A-1**: `gh auth token` is the canonical way to retrieve GitHub tokens
  from the `gh` CLI and is sufficient for REST API calls.
- **A-2**: `tea`'s `~/.config/tea/config.yml` schema is stable enough to
  parse for `{host, url, token}` triples.
- **A-3**: A single configured token per host is enough; we don't need to
  handle multiple GitHub or Forgejo accounts per host in v1.
- **A-4**: Both GitHub and Forgejo expose a fetchable PR-head ref
  (`pull/<n>/head` on GitHub; the equivalent on Forgejo) usable by FR-9a's
  cross-fork fetch. To be confirmed for the Forgejo version targeted.
- **A-5**: The active session's directory unambiguously identifies the
  project for sidebar scoping (matches how ocman already resolves project
  context elsewhere).
- **A-6**: Existing markdown rendering in the frontend can be reused for
  PR/Issue bodies without new dependencies.
- **A-7**: An ocman "settings" UI surface exists (or is acceptable to
  introduce) for editing the two prompt templates; if not, a settings
  file under `~/.config/ocman/` is acceptable.

## Out of Scope

The following are explicitly **not** part of v1:

- Creating PRs or issues from ocman.
- Commenting on PRs or issues.
- Reviewing PRs (approving, requesting changes, leaving review comments).
- Merging or closing PRs / issues.
- Editing labels, assignees, milestones, or any other PR/Issue metadata.
- Notifications when new PRs/issues arrive (toast, badge, sound, etc.).
- Showing CI / check status on rows or in the detail view.
- Showing diffs or file changes in the detail view.
- Showing comments or review threads in the detail view.
- Background polling or auto-refresh.
- Caching PR/Issue data across ocman restarts.
- Multiple accounts per forge host.
- Sort controls beyond the default "recently updated" order.
- Persistent filter state across restarts.

## Success Criteria

- The user can open a project in ocman, see PRs and Issues for it in the
  sidebar within a perceptible second, and launch a "handle this" session
  with one or two clicks.
- The user does not need to configure any tokens in ocman if `gh` /
  `tea` are already logged in.
- Projects with no supported upstream show no sidebar (no clutter, no
  errors).
- Failures degrade gracefully and per-remote — one broken remote never
  blocks the other tab or the rest of the UI.
- `make test` and `make lint` pass; e2e tests use stable locators.

## Open Questions

The following need clarification before or during implementation:

- **OQ-1**: ~~Where do the customizable prompt templates live?~~ **Resolved:**
  ocman's writable `state.db`, via a new `settings` table + migration
  (next available version). Requires a minimal in-app editing surface.
- **OQ-2**: ~~Is the sidebar a right-hand sidebar, a panel inside the
  existing project view, or a dedicated column?~~ **Resolved:** add a
  new pane to the existing `RightPanel` (`frontend/src/components/RightPanel.tsx`)
  alongside `info` / `session` / `working-tree`. The pane is listed in
  `ALL_TABS` only when an upstream is detected (FR-2). The pane's content
  is the two-tab PR/Issue UI from FR-3.
- **OQ-3**: ~~Should "open" PRs include draft PRs in the default filter?~~
  **Resolved:** yes — drafts are included in the `open` filter; the
  `draft` status is surfaced visually on the row (per FR-4).
- **OQ-4**: ~~Cross-fork PR worktree behavior?~~ **Resolved:** see FR-9a —
  auto-fetch the PR ref after explicit user confirmation, then create the
  worktree from the fetched ref.
- **OQ-5**: ~~Rate-limit cool-down duration?~~ **Resolved:** use
  `Retry-After` / `X-RateLimit-Reset` for a real countdown; 30s fallback
  when neither header is present (FR-12).
- **OQ-6**: ~~Expose upstream detection via `/api/capabilities` or a
  dedicated endpoint?~~ **Resolved:** dedicated per-project endpoint
  `GET /api/project/<dir>/upstreams` (see FR-1).
- **OQ-7**: ~~Token precedence between CLI and env var?~~ **Resolved:**
  env var wins, CLI is the fallback (FR-11). Matches the convention used
  by most CLI tools.

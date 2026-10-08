# Project detail layout plan

Status: planned for later implementation.

## Goal

Give project detail one consistent layout for Sessions, Worktrees, and Settings.
Use shared tables and controls, with project defaults available through the
existing quick-settings popover. Keep CSS with the component that owns it.

## Preparation completed

- Rebased `chore/shared-ui-controls` onto `origin/main` at `f8cc0c454` before
  inspecting the settings implementation.
- `ProjectDetail.tsx` currently shows sessions and separate header buttons for
  the worktree/settings routes. Its Settings link does not carry `remoteId`.
- `WorktreesView.tsx` already has a framed `DataTable`, owner-scoped requests,
  and explicit delete/force-delete decisions.
- `ProjectSettingsView.tsx` edits the ordered fallback model list and the
  disable-fallthrough flag. Its reads/writes need the same explicit owner as
  the rest of the project page.
- `ProjectQuickSettings.tsx` already edits default model, default agent, and
  default worktree behavior through `Popover`. It loads owner-scoped settings
  and catalogs, retains saved choices missing from the catalog, and calls
  `clearSettingsCache()` after a successful save.
- `DefaultAgentSetting.tsx` is the global default. Project overrides must keep
  its inherited behavior rather than duplicate that setting.

## Layout

- One project header and shared `Tabs` for Sessions, Worktrees, and Settings.
  Persist the selected view in the URL alongside the explicit machine owner.
- Sessions reuse `SessionTable`. Keep search, time range, archived filtering,
  project identity matching, and session actions.
- Worktrees reuse the current framed table and action controls. Keep branch,
  path, session count, last activity, and actions together in each row.
- Settings use a framed name/value/action table. Show the project default
  model, agent, worktree behavior, ordered fallback models, and fallthrough
  setting. Distinguish inherited defaults from explicit overrides.
- Put `ProjectQuickSettings` beside the project header for quick edits of the
  three defaults. Reuse its form for those edits instead of adding another
  settings request/save implementation.
- Preserve the existing worktree/settings URLs as entry points to the
  corresponding view. Carry `remoteId` through links and navigation.
- On phones, wrap header controls and scroll wide tables within their frame.
  Keep names readable and row actions keyboard-accessible.

## Implementation slices

1. **Project shell and sessions.** Add URL-backed view navigation, preserve
   owner identity and filters, and expose the existing quick-settings popover.
2. **Worktrees.** Move the current content under the project shell. Extract a
   reusable content component only where required to avoid duplicate headers
   and preserve owner-keyed state and destructive confirmations.
3. **Settings.** Add the settings table, reuse the defaults editor, and adapt
   fallback-model reads/writes to explicit ownership and cache invalidation.
   Keep optimistic rollback and pending-save protection.

Verify each slice and present desktop/phone screenshots before proceeding.

## Targeted verification

- Project filters and selected view survive refresh and back/forward.
- Switching owners with identical directory paths never carries rows, drafts,
  catalogs, or delete consent to the other machine.
- Remote disconnection stays visible and retryable; actions never fall back
  to the hub.
- Worktree launch, delete, dirty-worktree refusal, and explicit force delete
  retain their existing behavior.
- Default overrides save on the selected owner, inherited values remain
  selectable, and new conversations see updated defaults after saving.
- Model reordering/removal preserves the first-entry default, reverts failed
  saves, and blocks overlapping edits.
- Popover keyboard dismissal, nested picker Escape, focus return, long names,
  and phone table scrolling work.
- Run tests for changed components/routes, typecheck, and targeted lint.

No project-detail UI implementation is included in this planning change.

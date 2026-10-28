# Page UX consistency rollout

The agreed design and behavior defaults live in
[the page design guidelines](../../.agents/page-design.md). This checklist
tracks adoption without treating every existing page as already compliant.

## First batch

- [x] Add a shared dedicated empty/unavailable state with accessible headings.
- [x] Routines and webhook inboxes: dedicated states, skeletons and retry.
- [x] Keep independent routine/inbox list loading independent.
- [x] Artifacts: dedicated states, URL search/project filters and Clear filters.
- [x] Preserve Artifact filters through detail navigation and return to the list.
- [x] Preserve successful list content on refresh failures, including empty lists.
- [x] Worktrees: preserve rows during refresh and put actions in the toolbar.

## Remaining work

- [ ] Review pages for stacked independent sections; split related views into
  tabs and unrelated tasks into separate routes.
- [ ] Projects, Sessions and Background tasks: align page states and URL filters.
- [ ] Browse-and-open lists: explicit title controls and safe whole-row clicks.
- [ ] Collection pages: stacked phone rows and visible primary actions.
- [ ] Factory: reuse shared drawers and navigation styling.
- [ ] Drawers: shared wide variant and unsaved-edit protection. The shared
  `useUnsavedChangesGuard` exists; editors still need to adopt it.
- [ ] Destructive actions: shared confirmation dialog and pending/error states.
  `useConfirm` exists and plugins use it; host `window.confirm` calls remain.
- [ ] Dates, spacing and platform-neutral copy: remove page-specific variants.
  `formatExactDate` and `RelativeTimestamp` are the shared formatters.

## Verification

Unit tests cover initial failures without false empty messages, Retry, skeleton
loading, independent lists, refresh failures with retained content, pagination
recovery and URL filter restoration. Initial-failure/false-empty and refresh-row
regressions were observed failing before the fixes. The cache-invalidation
regression after deleting an artifact was also observed failing before its fix.

Playwright uses the mocked API fixtures, not a writable backend. Check dedicated
states at desktop and phone widths, URL reload and detail-return behavior, and
the Worktrees action toolbar. Run `make lint`, `make coverage SUITE=frontend`
and `make coverage-check SUITE=frontend BASELINE_DIR=<baseline>` before committing.

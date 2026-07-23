# Beads Ticket Sidebar

## Problem Statement

Ocman users working in repositories managed with Beads cannot see the repository's current ticket hierarchy without leaving ocman and running `bd list`. Users whose repositories do not use Beads should not see irrelevant controls or discover that ocman contains a Beads integration.

## Solution

Add a read-only Beads tab to the existing right sidebar. When the active local or remote repository has a supported Beads workspace, the tab reproduces the compact hierarchical information from the default `bd list`: status, ticket ID, priority, title, and parent/child nesting. When Beads is not installed, no workspace exists, or the installed version is unsupported, ocman renders no Beads-specific UI.

## User Stories

1. As a user working in a Beads repository, I want a Beads sidebar tab, so that I can inspect current tickets without leaving ocman.
2. As a user, I want the sidebar to show the same essential information as `bd list`, so that its meaning is familiar.
3. As a user, I want tickets nested under their parents, so that I can understand the work breakdown.
4. As a user, I want each ticket's colored Beads-style status circle, priority, type, title, and muted ID, so that I can assess the work at a glance.
5. As a user, I want closed tickets excluded, so that the view matches the default `bd list` focus on active work.
6. As a user, I want Beads' other default list exclusions and 50-ticket limit preserved, so that ocman and the CLI produce a consistent, bounded view.
7. As a user working in a repository without Beads, I want no Beads tab, loading state, empty state, or error, so that the feature does not add noise.
8. As a user with an unsupported Beads version, I want no Beads UI, so that I do not encounter a broken integration.
9. As a user switching repositories, I want Beads availability recalculated for the new repository, so that stale UI does not leak between projects.
10. As a user opening a remote repository, I want Beads to run on that repository's owning host, so that remote and local projects behave consistently.
11. As a user, I want the tree refreshed every 30 seconds while visible, so that changes made by agents appear without constant background work.
12. As a user, I want a manual refresh action, so that I can request current data immediately.
13. As a user, I want refreshes to stop while the pane is hidden, so that ocman does not run unnecessary processes.
14. As a user, I want the last successful tree retained during a transient refresh failure, so that useful information does not disappear.
15. As a user whose discovered Beads workspace becomes unhealthy, I want the tab to remain visible with a generic retryable error, so that I can recover without guessing why it vanished.
16. As a user, I want ticket titles rendered as plain text, so that repository-controlled content cannot inject markup into ocman.
17. As a user, I want ocman to inspect Beads without intentionally changing tickets, so that viewing status is safe.

## Implementation Decisions

- Beads is repository- and host-scoped, not a coding-agent platform. The integration uses the existing directory-owner `Host` seam and supports both local hosts and remote hosts through ocman's existing remote transport.
- Availability is determined on the owning host. Beads 1.1.0 is the minimum supported version. A missing `bd` executable, missing Beads workspace, older version, or unsupported JSON shape returns `available: false` and produces no frontend UI.
- Workspace discovery uses Beads' directory-aware CLI discovery rather than checking for a literal `.beads` directory, because Beads supports ancestor discovery, redirects, and worktree sharing.
- Commands use fixed argument arrays with a bounded context; no shell is involved. Best-effort `--readonly` behavior is accepted even though Beads startup may migrate local data or start a configured Dolt server.
- The list follows default `bd list` behavior: closed tickets remain excluded, Beads' other default exclusions remain intact, and the default 50-ticket limit is preserved.
- Ocman consumes machine-readable issue rows and parent-child dependency records. It does not parse terminal formatting or infer hierarchy from dotted ticket IDs.
- The backend returns only the fields needed by the UI: availability, ticket ID, title, status, priority, optional parent ID, and a bounded generic error state. It does not expose database paths, raw stderr, descriptions, metadata, credentials, or full Beads payloads.
- Parent relationships whose parent is absent from the bounded result are rendered as top-level rows; the backend does not issue recursive expansion queries.
- The frontend does not render the tab while availability is unresolved or false. Dynamic tab filtering applies to ordering, open-tab state, collapsed state, sizing, and pane rendering, so persisted sidebar state cannot expose an empty Beads pane in another repository.
- Ticket rows use the normal sidebar font size. The first line contains the colored Beads-style status circle, priority, and title; the second line contains the issue type with the muted ticket ID aligned right. Visible connector lines communicate parent/child depth.
- Each row owns one fixed-width marker rail containing its ancestor guides, elbow, child bridge, and status circle. Connector geometry does not depend on wrapped content or nested-list dimensions.
- Once workspace discovery succeeds, later command failures keep the tab visible, preserve the last successful tree, and show a generic retry action. Diagnostics remain bounded and backend-only.
- Data is fetched when the active repository directory or owner changes. While the pane is visible, refreshes run every 30 seconds without overlap. Hiding the pane stops polling; a manual refresh remains available.
- Beads data is not persisted in ocman's state database. The Beads repository remains authoritative.
- Ticket content is treated as untrusted and rendered as plain text.
- The architecture documentation is updated to include the optional Beads CLI dependency and the browser-to-owning-host data flow.

## Testing Decisions

- Tests assert externally visible behavior rather than command-runner internals or component implementation details.
- The primary backend seam is one owner-routed `Host` operation returning the complete sidebar model. Its local implementation uses a small injected command runner so tests do not require a Beads installation.
- Backend tests cover a missing executable, missing workspace, valid list and hierarchy data, default command flags, malformed JSON, unsupported schema, timeout, absent parent rows, and a discovered workspace whose list query fails.
- HTTP tests cover directory validation, explicit remote-owner selection, local and remote delegation, unavailable responses, and available/error responses with a fake host.
- A remote round-trip test proves that the repository directory reaches the owning remote host and that its model returns to the hub unchanged.
- Frontend resource tests cover repository/owner changes, cancellation, non-overlapping refresh, visible-only 30-second polling, manual refresh, and retention of stale data after transient failure.
- Right sidebar tests cover unresolved, unavailable, available, and unhealthy states; hierarchical rendering; persisted open-tab state in unavailable repositories; repository switching; and retry behavior.
- Frontend tests use accessible role or label queries for the tab, tree, error, and refresh action.
- Coverage must not decrease on either the Go or frontend side.

## Out of Scope

- Creating, editing, closing, assigning, or reprioritizing Beads tickets.
- Rendering ticket descriptions, notes, comments, labels, metadata, or Markdown.
- Ticket detail pages, search, filtering, pagination controls, or configuration of `bd list` flags.
- Showing closed tickets or more than Beads' default 50 results.
- Parsing `.beads` storage or terminal-formatted output directly.
- Persisting or indexing Beads data in ocman.
- Supporting legacy or unsupported Beads JSON shapes.

## Further Notes

The supporting investigation and primary-source citations are recorded in `spec/beads-status-sidebar/research.md`. Missing or unsupported Beads installations are intentionally indistinguishable from a build of ocman without this feature. A successfully discovered workspace is different: subsequent failures remain visible and retryable.

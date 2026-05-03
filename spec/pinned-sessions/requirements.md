# Pinned Sessions - Requirements

## Overview

A new feature that lets the user pin sessions so they stay visible at
the top of the sidebar, regardless of age or activity. Pinned sessions
appear as a dedicated "Pinned" group in the project-grouped sidebar
view, and as a top section in the flat "recent" view. Pins are ordered
by when they were pinned (most recently pinned first).

The feature surfaces as:

1. A **pin/unpin toggle** on each session row in the sidebar.
2. A **"Pinned" group** at the top of the project-grouped sidebar view.
3. A **pinned section** at the top of the flat "recent" sidebar view.
4. A `pinned` boolean on the `Session` API response, persisted in
   ocman's `state.db`.

## Goals

- Let the user keep important sessions visible even after they age out
  of the "recent" time window.
- Provide a quick-access area for sessions the user is actively
  monitoring or frequently switching between.
- Follow the existing patterns for session state (archived, seen) so
  the feature is consistent and low-maintenance.

## Target Users

The single user / maintainer of ocman, juggling multiple concurrent
sessions across projects. The feature is designed for that workflow.

## Functional Requirements

### FR-1: Pin/unpin toggle in the sidebar

- **Description**: Each session row in the sidebar gains a pin button
  (visible on hover, like the existing archive button). Clicking it
  toggles the pinned state.
- **Acceptance Criteria**:
  - A pin icon button appears on hover for each session row.
  - Clicking the button pins an unpinned session (or unpins a pinned
    one).
  - The toggle is optimistic: the UI updates immediately, then
    confirms with the backend.
  - The pin button is visually distinct from the archive button
    (different icon, e.g. a thumbtack / pin icon from Bootstrap
    Icons).
  - Pinned sessions show a persistent (non-hover) pin indicator so
    the user can see at a glance which sessions are pinned.

### FR-2: "Pinned" group in the project-grouped sidebar view

- **Description**: When the sidebar is in "projects" mode, pinned
  sessions appear in a dedicated group at the top, above all project
  groups.
- **Acceptance Criteria**:
  - The group is labelled "Pinned" (or similar).
  - Sessions within the group are ordered by `pinned_at` descending
    (most recently pinned first).
  - The group only appears when there are pinned sessions.
  - Pinned sessions still appear in their normal project group as
    well (they are not removed from the project group — they appear
    in both places). *Rationale*: the user may want to see the
    session in project context too; removing it would be confusing.
  - The "Pinned" group is not collapsible (it's always expanded).
  - The aggregate status indicator on the "Pinned" group header
    follows the same rules as project groups (pending > error >
    busy > waiting > none).

### FR-3: Pinned section in the flat "recent" sidebar view

- **Description**: When the sidebar is in "recent" (flat) mode,
  pinned sessions appear at the top, above the chronologically
  sorted list.
- **Acceptance Criteria**:
  - Pinned sessions are visually separated from the rest (e.g. a
    subtle divider or different background).
  - Within the pinned section, sessions are ordered by `pinned_at`
    descending.
  - Unpinned sessions follow in the normal chronological order.
  - Pinned sessions do not also appear in the chronological section
    below (they are deduplicated — shown only in the pinned section).

### FR-4: Backend pin/unpin endpoint

- **Description**: A new POST endpoint to toggle the pinned state of
  a session, persisted in `state.db`.
- **Acceptance Criteria**:
  - Endpoint: `POST /api/session/pin` with body
    `{ platform, sessionId, pinned }` where `pinned` is a boolean.
  - When `pinned` is `true`: records the session as pinned with a
    `pinned_at` timestamp.
  - When `pinned` is `false`: removes the pin.
  - Idempotent: pinning an already-pinned session is a no-op (does
    not update `pinned_at`).
  - The endpoint follows the same auth pattern as
    `/api/session/archive` and `/api/session/seen`.

### FR-5: `pinned` field on the Session API response

- **Description**: The `GET /api/sessions` response includes a
  `pinned` boolean and `pinnedAt` timestamp for each session.
- **Acceptance Criteria**:
  - `pinned` is `true` when the session has a row in the
    `pinned_session` table.
  - `pinnedAt` is the Unix timestamp (milliseconds) of when the
    session was pinned, or `0` when not pinned.
  - The overlay logic follows the same pattern as `archived` and
    `seen` in `applySessionState`.

### FR-6: Pinned sessions survive the "recent" time window

- **Description**: The sidebar loads sessions from the last N hours.
  Pinned sessions must appear even if they are older than that
  window.
- **Acceptance Criteria**:
  - The backend (or frontend) ensures pinned sessions are always
    included in the sidebar list, regardless of the `since`
    parameter.
  - If a pinned session is also archived, the archive takes
    precedence: it does not appear in the sidebar (unless the user
    has "show archived" enabled).

## Non-Functional Requirements

### NFR-1: Performance

- **Description**: Pinning/unpinning must feel instant.
- **Acceptance Criteria**:
  - The UI updates optimistically on click.
  - The backend round-trip does not block the UI.
  - Loading pinned sessions adds negligible overhead to the
    `/api/sessions` response (the pinned table is small; a single
    bulk read is sufficient).

### NFR-2: No regression

- **Description**: Existing sidebar behaviour (sorting, grouping,
  archiving, seen state, status indicators) is unchanged for
  unpinned sessions.
- **Acceptance Criteria**: All existing Go and frontend tests pass.

## Data Requirements

- **New table in `state.db`**: `pinned_session` with columns
  `platform TEXT`, `session_id TEXT`, `pinned_at INTEGER`, primary
  key `(platform, session_id)`.
- **Schema migration**: version 5 (following the existing migration
  pattern in `internal/state/migrate.go`).
- The `pinned_at` column stores a Unix timestamp in milliseconds,
  used for sort order within the pinned group.

## Integration Points

- **`internal/state/db.go`**: new CRUD methods for pinned sessions,
  following the pattern of `ArchiveSession` / `ArchivedSessions`.
- **`internal/server/handlers.go`**: extend `applySessionState` to
  overlay `Pinned` and `PinnedAt` fields; add the new endpoint.
- **`internal/db/types.go`**: add `Pinned bool` and `PinnedAt int64`
  fields to `db.Session`.
- **`frontend/src/lib/api.ts`**: add `pinned` and `pinnedAt` to the
  `Session` interface; add `pinSession` / `unpinSession` API methods.
- **`frontend/src/pages/SessionDetail.tsx`**: modify `renderRow`,
  `sidebarProjectGroups`, and the flat view to support pinned
  sessions.

## Constraints

- All new Go code lives under `internal/`.
- No platform branching in the frontend.
- Follow the existing model-favorites / archive / seen patterns for
  consistency.
- The pin state is per `(platform, session_id)`, consistent with all
  other session state in `state.db`.

## Out of Scope

- **Pin ordering / drag-and-drop reordering**: v1 uses `pinned_at`
  order only. Manual reordering is deferred.
- **Pin from the session detail header**: v1 only adds the toggle to
  the sidebar row. A header pin button can be added later.
- **Pin limit**: no cap on the number of pinned sessions in v1.
- **Pin persistence across session IDs**: if a session is recreated
  with a new ID (e.g. `/clear`), the pin does not carry over.
- **Keyboard shortcut for pin/unpin**: deferred.

## Success Criteria

- The user can pin sessions and they stay visible at the top of the
  sidebar.
- Pinned sessions survive page reloads and outlive the "recent" time
  window.
- The feature feels native — consistent with the existing archive
  button pattern.

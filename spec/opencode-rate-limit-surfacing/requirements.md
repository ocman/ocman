# OpenCode Rate-Limit Surfacing - Requirements

## Overview

OpenCode already emits useful throttle feedback when a prompt cannot be
served because the account is rate-limited, for example:

> this request would exceed your account’s rate limit. Please try again later [retrying in 5m attempt 1]

Ocman currently reduces that state to a generic session `error` signal,
so the user can see that something failed but not *why*, *whether it is
temporary*, or *when it may recover*.

This feature adds a first-class session-level notice for transient
rate-limit / backoff conditions so the UI can surface them in the
sidebar and session detail view without requiring the user to inspect
raw OpenCode logs.

## Goals

- Make OpenCode rate-limit failures visible in ocman.
- Distinguish temporary throttling from generic hard errors.
- Show the retry/backoff hint when OpenCode provides one.
- Keep the frontend platform-agnostic: it renders a normalized session
  notice, not OpenCode-specific wire shapes.

## Target Users

The single user / maintainer of ocman, monitoring multiple concurrent
OpenCode sessions and needing to know which ones are temporarily blocked
by provider/account limits.

## Functional Requirements

### FR-1: Session payload exposes a normalized transient notice

- **Description**: `GET /api/sessions` and `GET /api/session/{id}` expose
  a structured notice when the latest assistant failure represents a
  retryable rate-limit condition.
- **Acceptance Criteria**:
  - Sessions with a latest assistant failure matching the OpenCode
    rate-limit pattern include a `notice` object.
  - The notice includes at least:
    - `kind` = `rate_limit`
    - `message` = user-visible summary
    - `retryAt` = Unix ms timestamp when retry is expected, or `0` when
      unknown
    - `attempt` = retry attempt number when available, or `0`
  - Sessions without an active rate-limit condition return no notice.
  - The wire shape is platform-agnostic; the frontend must not inspect
    `platform === 'opencode'` to use it.

### FR-2: OpenCode retry hints are parsed and normalized

- **Description**: Ocman derives the notice from the latest OpenCode
  assistant error payload.
- **Acceptance Criteria**:
  - The parser recognizes messages like:
    - `this request would exceed your account’s rate limit...`
    - trailing retry metadata such as `[retrying in 5m attempt 1]`
  - `retrying in <duration>` is converted into `retryAt` relative to the
    failing message timestamp.
  - `attempt <n>` is extracted when present.
  - If the message is rate-limit related but no retry duration is
    present, ocman still surfaces `kind=rate_limit` with `retryAt=0`.
  - Non-rate-limit errors must not be misclassified.

### FR-3: Session detail view shows a visible rate-limit banner

- **Description**: When the active session carries a rate-limit notice,
  the session page shows a persistent banner near the composer / thread
  controls.
- **Acceptance Criteria**:
  - The banner is visible without opening developer tools or inspecting
    raw assistant JSON.
  - The banner text communicates that the session is rate-limited and,
    when known, when retry is expected (e.g. `Retrying in ~5m`).
  - When an attempt count is known, it is shown in secondary text.
  - The banner disappears automatically once a later successful assistant
    turn clears the condition.

### FR-4: Sidebar rows surface the transient reason

- **Description**: The recent/project sidebar should make rate-limited
  sessions distinguishable from generic errors.
- **Acceptance Criteria**:
  - A rate-limited session row has a visible hint in addition to the
    existing error status treatment.
  - At minimum, hovering the row or indicator reveals the normalized
    notice message.
  - The projects-group aggregate status remains unchanged (`error`
    still rolls up as error); this feature adds explanation, not a new
    top-level status bucket.

### FR-5: Immediate send rejections use the same language

- **Description**: If the prompt submission itself fails with an
  upstream rate-limit rejection, the failed-send UI should surface the
  same normalized wording as the session notice.
- **Acceptance Criteria**:
  - A direct send failure caused by upstream throttling shows a
    rate-limit-specific message in the existing failed-send banner.
  - If a retry hint is present, it is included in the failed-send copy.
  - Existing behaviours for busy, unreachable, and generic upstream
    rejections are preserved.

## Non-Functional Requirements

### NFR-1: No frontend platform branching

- **Description**: The UI must consume a normalized session notice, not
  OpenCode-specific fields.
- **Acceptance Criteria**: `make lint` continues to pass the
  platform-branching check.

### NFR-2: No extra per-row live fetches

- **Description**: Surfacing the notice must not require the sidebar to
  fetch each errored session individually.
- **Acceptance Criteria**:
  - The sidebar gets all required data from existing session-list
    payloads.
  - Any backend enrichment is done during existing session assembly.

### NFR-3: Best-effort parsing, safe fallback

- **Description**: OpenCode error strings may change over time.
- **Acceptance Criteria**:
  - If parsing fails, ocman falls back to the generic existing error UX.
  - Malformed or unknown retry hints do not crash request handlers or
    the frontend.

### NFR-4: No regression in existing session rendering

- **Description**: Non-rate-limit sessions should behave exactly as they
  do today.
- **Acceptance Criteria**: Existing Go and frontend tests continue to
  pass.

## Data Requirements

- Add a normalized optional `notice` field to the session API model.
- The notice is derived data only; no new persistent tables in
  `state.db`.
- The backend may add internal-only fields needed to inspect the latest
  assistant error message during normalization.

## Integration Points

- **`internal/db/sessions.go`** — expose the latest assistant error
  payload/message needed for classification.
- **`internal/db/types.go`** — add the normalized session notice type
  and session field.
- **`internal/server/handlers.go`** — enrich session payloads with the
  normalized notice.
- **`internal/platforms/opencode/`** (or shared helper under
  `internal/`) — rate-limit parser/normalizer.
- **`frontend/src/lib/api.ts`** — add the typed `notice` field.
- **`frontend/src/pages/SessionDetail.tsx`** — render the banner.
- **Sidebar rendering in `SessionDetail.tsx`** — expose the row hint /
  tooltip.
- **`frontend/src/components/AssistantThread.tsx`** and/or send-flow
  helpers — align failed-send copy with the normalized message.

## Constraints

- No new platform-specific branches in the frontend.
- Do not replace the existing `error` session status; layer the notice
  on top of it.
- Keep the feature additive and small: no new polling loops, stores, or
  background jobs.

## Out of Scope

- Automatic retry scheduling by ocman.
- Notifications outside the existing session UI (desktop notifications,
  email, etc.).
- A new top-level session status enum value such as `rate_limited`.
- Parsing every possible provider quota message across all platforms;
  v1 focuses on the known OpenCode wording while keeping the API shape
  generic.

## Success Criteria

- A user can tell, from ocman alone, that a session is blocked by rate
  limits rather than a generic error.
- The UI shows the retry hint when OpenCode provides one.
- The feature fits the current architecture and does not regress other
  session flows.

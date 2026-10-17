# App improvement research

Date: 2026-10-02

## Recommendation

Prioritize correct machine ownership, predictable composer interactions, and bounded background work. Ocman already has substantial functionality. The best next investment is making the existing workflows reliable as sessions, history, and connected machines accumulate.

The highest-value first batch is:

1. Preserve explicit machine identity in worktree operations and session runtime recovery.
2. Fix cache-expiry full scans and zero-delay retries after failed reconciliation.
3. Fix composer Tab navigation, composing Enter, and modal Escape handling.
4. Stop polling every routine's complete history.
5. Make routine results directly addressable and MCP setup available again after dismissal.

## Method and limits

Three research passes inspected UX, backend behavior, and product workflows. Findings were checked against implementation, existing tests, and documentation. The main pass independently re-read the strongest implementation paths and ran focused frontend tests.

This is a source-based audit, not a production performance profile or usability study. “Source-backed” means the mechanism is visible in code. It does not mean a failure was reproduced in a running browser. Performance claims describe work performed, not measured milliseconds saved. Feature recommendations remain hypotheses until users or usage data validate demand.

The checkout was being changed by other work during research. It initially used `perf/analytics-index-window` at `9df1f0251`, then moved to `e3897277cf2e76dc546291801a67d10ffd8d46b8` on other feature branches with unrelated working-tree changes. Retained backend findings were unchanged across those commits. Line references describe the inspected files and may move. In-progress analytics work was excluded. This audit changed no application source and used no live database or application startup.

Browser inspection was unavailable because the browser tool reported an occupied profile. No visual, mobile-device, or browser-performance results are claimed.

### Checks run

Existing cache and queue tests passed:

```sh
go test ./internal/queuesvc ./internal/platforms/opencode \
  -run '^(TestEnqueue_|TestFlush_|TestSweep_|TestDrainGuardIsPerPlatform|TestFlushDrainsOnlyItsOwnPlatform|TestGetSessionsCached_|TestStartSessionsRefresher_|TestRefreshSessionsIncremental_|TestRefreshSessionNow|TestIncrementalRefreshRecordsBudget)' \
  -count=1 -timeout=90s
```

Existing frontend tests passed, 59 tests across four files, run from `frontend/`:

```sh
pnpm exec vitest run src/components/assistant/Composer.queue.test.tsx src/components/assistant/Composer.focus.test.tsx src/components/Modal.test.tsx src/components/CommandPalette.render.test.tsx
```

These passing tests establish the current baseline. They do not exercise the newly identified failure scenarios. Each recommendation below includes the missing regression or measurement.

## Ranked opportunities

Priority means recommended order, not an incident severity claim. Small means a localized change; medium crosses components or service boundaries. These are scope estimates, not delivery dates.

| Rank | Improvement | Evidence | Scope |
| --- | --- | --- | --- |
| 1 | Keep machine ownership in worktrees and runtime recovery | Source-backed wrong-host paths | Medium |
| 2 | Correct session-cache scheduling | Source-backed unnecessary full scans and failure retry loop | Small |
| 3 | Make composer keyboard behavior safe and accessible | Source-backed handlers; IME needs device validation | Small |
| 4 | Bound routine polling and history | Source-backed N+3 requests per successful five-second cycle | Medium |
| 5 | Respect filters in remote stale-session fallback | Source-backed cross-project fallback | Small/medium |
| 6 | Recover silent headless event streams | Missing inactivity bound; occurrence unmeasured | Small/medium |
| 7 | Handle uncertain queued-message delivery explicitly | Source-backed replay window | Medium |
| 8 | Read bounded lifecycle metadata for queue decisions | Source-backed history-dependent work | Medium |
| 9 | Expose touch actions for Send now and Queue | Confirmed control gap | Small |
| 10 | Finish overlay and mobile navigation accessibility | Source-backed focus gaps | Small/medium |
| 11 | Carry machine identity into notifications and artifact scope | Source-backed identity loss | Medium |
| 12 | Open exact routine results from Inbox | Confirmed navigation gap; feature recommendation | Medium |
| 13 | Restore access to MCP setup in Settings | Confirmed setup re-entry gap | Small |
| 14 | Invalidate stale project-search selections immediately | Source-backed debounce window | Small |
| 15 | Measure large sidebar rendering before changing it | Static scaling hypothesis | Small experiment |
| 16 | Consider explicit cross-device draft handoff | Product hypothesis | Medium; defer pending demand |

## 1. Keep machine ownership throughout host operations

The Worktrees view lists and deletes by directory without carrying `remoteId`. It also associates sessions by directory and opens a session URL without platform identity. The backend supports strict explicit-owner routing, but inferred directory ownership can fall back to local. Two machines using the same checkout path expose the ambiguity.

Runtime recovery has the same problem independently. It resolves the correct session adapter, reads its directory, then uses `ForDir` to choose a host again. A reachable remote whose OpenCode process has stopped can therefore trigger a launch on a different host if project inventory is missing or ambiguous.

**Sources:** `frontend/src/pages/WorktreesView.tsx:49-72,95-99,183-186`; `frontend/src/lib/worktrees.ts:12-23`; `internal/server/host.go:58-73`; `internal/hostsvc/router.go:92-125`; `internal/server/queue.go:166-178`; `internal/remote/manager.go:506-523`.

**Impact:** Operations or recovery may target an unintended machine. Wrong-host deletion requires a matching worktree on the inferred host and passing its existing deletion checks; this is not a claim that an arbitrary path bypasses those checks.

**Smallest change:** Preserve owner identity through navigation and every worktree request. In runtime recovery, derive the host from the already-resolved adapter, using explicit remote lookup or local routing. Fail closed if the named owner disappears.

**Validation:** Fake local, A, and B hosts with identical paths. Assert all operations reach the selected owner; disconnect A and assert zero local or B mutation calls. Existing relaunch tests only cover a local stub at `internal/server/queue_test.go:625-715`.

## 2. Correct session-cache scheduling

### Ordinary expiry still schedules full scans

The global snapshot has a 15-second TTL. A normal stale read returns immediately but calls `refreshSessions`, which performs a full scan. The existing incremental refresher can renew freshness without querying when no sessions are dirty, but that is not the expired-read path. Idle polling can therefore trigger whole-list scans well before the intended five-minute reconciliation.

**Sources:** `internal/platforms/opencode/models_cache.go:293-310,635-665,764-830`; `internal/platforms/opencode/models_cache_test.go:1249-1267`; `internal/platforms/opencode/models_cache_dirty_test.go:399-425`.

### Failed reconciliation can retry with zero delay

The background loop ignores refresh errors and schedules its next attempt from the last successful full refresh. If that timestamp is missing or overdue, the delay is zero. Immediate database failures can produce back-to-back retries while sessions are in demand.

**Sources:** `internal/platforms/opencode/models_cache.go:463-526,792-817,912-938`.

**Smallest change:** Route ordinary expiry through the existing incremental path. Keep cold loads and explicit invalidation semantics. Add a nonzero failure retry delay without clearing dirty state.

**Validation:** Counting fake store plus controlled time. A clean TTL expiry must issue no full query; overdue reconciliation must issue one. An always-failing store must have a bounded attempt rate and cancel promptly. Compare full-scan counts under five-second polling over ten simulated minutes. Do not infer latency savings from test runtime.

## 3. Make composer keyboard behavior predictable

Three interactions need regression coverage:

- Plain Tab and Shift+Tab change the agent and prevent focus movement whenever agent options and the callback exist. Users cannot tab normally out of the editor.
- Enter-to-send has no composition guard. IME candidate confirmation may submit unfinished Chinese, Japanese, or Korean input. The missing guard is certain; browser-specific reproduction remains necessary.
- Both Modal and a running Composer listen for window-level Escape. Modal dismissal does not consume the event, while Composer aborts a running turn if default was not prevented. Closing help can also invoke abort.

The main textarea also lacks a stable accessible name.

**Sources:** `frontend/src/components/assistant/Composer.tsx:776-842,1041-1061`; `frontend/src/components/Modal.tsx:39-45`; `frontend/src/components/assistant/HelpDialog.tsx:23-46`. Existing help focus tests use `isRunning={false}` at `Composer.focus.test.tsx:82-110`.

**Smallest change:** Restore ordinary Tab behavior outside autocomplete. Keep the existing Agent picker. Ignore composing Enter events before selection/submission. Let the active dialog consume Escape before global abort handling. Add a “Message” label.

**Validation:** Tab exits without changing the agent; composing Enter sends nothing; Escape closes help without aborting a running turn; the textarea is discoverable by role and accessible name. Verify IME behavior on supported real browsers.

## 4. Bound routine polling and history

The Routines page polls every five seconds. Each successful cycle requests the routine list, projects, every routine's history, and webhook inboxes. With N routines, that is N+3 requests per cycle. History queries have no limit. Fifty routines would produce 53 requests per completed cycle; this is arithmetic from the code, not a measured production workload.

**Sources:** `frontend/src/pages/Routines.tsx:128-162`; `internal/state/routine_runs.go:75-89`.

**Smallest change:** Include a latest-run summary in the routine list. Fetch bounded history only for the opened drawer and page older runs. Avoid reloading stable project metadata on every run-status poll.

**Validation:** Request count remains constant as routine count grows. Use an isolated fixture with many routines and long histories to measure response bytes, allocations, and render cost. Keep the existing protection against overlapping refresh cycles.

## 5. Respect filters in remote fallback caching

Every successful remote session-list request replaces a single fallback slot and ownership map, including filtered requests. On a later RPC error, the fallback ignores the new request's directory and time filters. A project B request can receive project A's cached rows. A narrow request can also erase the broad fallback and remembered ownership in that adapter.

**Sources:** `internal/remote/platform.go:94-143,176-182`; `internal/server/fanout.go:42-86`. Existing stale-fallback coverage at `internal/remote/integration_test.go:483-515` only uses an unfiltered request.

**Qualification:** Fan-out skips adapters already known to be unavailable. The strongest failure case is the request that fails while the adapter is still admitted, not indefinite offline display.

**Smallest change:** Always filter fallback output. Preserve a complete snapshot for complete fallback behavior; filtered reads must not replace the full ownership set. Avoid unbounded keys containing rolling `since` values.

**Validation:** Interleave broad, project A, and project B calls, then fail one RPC. Assert exact directory/time filtering and preserved ownership.

## 6. Recover silent headless event streams

The headless watcher reads its SSE body with `io.Copy` and has no inactivity deadline. Discovery leaves the subscription alone while its port remains present. A stream that stops emitting without closing can keep stale live status and miss prompts or idle edges indefinitely.

Browser-facing SSE already has a 60-second inactivity timeout, so there is an existing pattern to reuse.

**Sources:** `internal/autoapprove/watcher_stream.go:137-186`; `internal/autoapprove/watcher.go:191-226,257-278`; `internal/platforms/opencode/events.go:65-99`.

**Smallest change:** Bound response-header wait and byte inactivity, then use the existing reconnect loop to reseed state. Do not impose a fixed total lifetime on healthy streams.

**Validation:** A fake upstream sends one heartbeat and stalls while its port remains discoverable. Assert reconnect and prompt/status recovery within the configured bound. A regularly heartbeating stream must stay connected.

## 7. Make uncertain queue delivery recoverable

Queue draining sends upstream before deleting the durable queue row. If the send succeeds and local deletion fails, or the process exits in between, the row remains pending. The next drain can send it again. The stable queue ID is not included as a delivery identity in the request.

**Sources:** `internal/queuesvc/service.go:292-340`; `internal/platforms/opencode/operations.go:397-438`.

**Impact:** Repeated prompts can cost money and repeat agent work. Whether a particular upstream deduplicates internally was not established. Normal per-session locking and successful dequeue already prevent ordinary concurrent duplicate sends; this finding concerns the acknowledgment window.

**Smallest safe change:** Persist dispatch/uncertain status and avoid automatic replay of uncertain rows. After acknowledged success, retry local finalization instead of sending again. Expose uncertain outcomes for explicit reconciliation or retry. Use automatic idempotent replay only after verifying an end-to-end platform contract.

**Validation:** Inject successful send followed by failed dequeue; trigger another idle edge and restart over the same temporary store. Assert one submission and a visible recoverable state. Separately test acceptance with a lost response.

## 8. Make queue status reads independent of transcript size

Queue lifecycle checks ask for `Session(..., 1, 0)`. That limits the returned message page, but the implementation still loads/converts full history before pagination and attaches session-tree information. With pricing enabled, tree costs read additional histories. A sweep may call this detail path several times for one decision.

**Sources:** `internal/server/queue.go:95-134`; `internal/platforms/opencode/adapter.go:263-331`; `internal/platforms/opencode/client.go:211-243`; `internal/platforms/opencode/adapter_session_detail.go:46-66`; `internal/db/sessions.go:543-583`; `internal/queuesvc/service.go:272-311,356-389`.

**Smallest change:** One owner-routed metadata read for settled status and latest message ID/time/role. Reuse existing status settling and bounded SQL; omit parts and session trees.

**Validation:** Fixtures with 10, 1,000, and 10,000 messages. Assert no transcript-part/tree reads and bounded payload size. Measure allocation and sweep duration. Preserve existing idle-edge ordering tests.

## 9. Give touch users explicit send and queue actions

Queueing requires Ctrl/Meta-click or Ctrl/Cmd+Enter. While running, the visible Send control becomes Stop. A touch-only user has no explicit Queue action or on-screen Send now action during generation.

**Sources:** `frontend/src/components/assistant/Composer.tsx:340-357,817-819`; `frontend/src/components/assistant/QueuedMessages.tsx:21-80`.

**Smallest change:** Expose Send now and Queue in a touch-accessible control while keeping Stop available. Reuse existing submission paths. Do not silently change all mid-turn sends into queued sends; immediate interleaving is intentional.

**Validation:** Tap-only tests can reach all three actions without modifier keys. Check on a narrow viewport with the soft keyboard open.

## 10. Finish accessibility in global search and mobile navigation

Global search uses a custom backdrop without the shared modal's focus containment, background isolation, or opener restoration. Keyboard focus can leave the visible search overlay. The closed mobile navigation is moved off-screen with a transform while links remain mounted and focusable.

**Sources:** `frontend/src/components/CommandPalette.tsx:630-669`; `frontend/src/components/MainNav.css:62-82`; `frontend/src/components/MainNav.tsx:62-113`; `frontend/src/components/Modal.tsx:47-105`.

**Smallest change:** Reuse Modal for search, retaining its existing combobox/listbox semantics. Make closed mobile navigation inert or hidden at the mobile breakpoint, and manage focus when opening and closing it.

**Validation:** Tab stays inside open search and returns to its opener on dismissal. Hidden mobile links are absent from focus traversal. Desktop navigation remains usable.

## 11. Preserve identity in notifications and artifact presentation

The notification projection combines sessions from multiple machines but omits platform. Browser baseline/deduplication keys and links use session ID alone. Imported or copied databases can produce identical session IDs on different owners, suppressing a notification or opening the wrong session.

Artifacts have a related presentation problem. Storage is instance-local and remote browsing is unsupported. The pane says “No artifacts yet,” and project scope drops platform entirely, so a remote project can display the hub's artifacts for an identical directory.

**Sources:** `internal/server/handlers_sessions.go:128-161`; `frontend/src/lib/useNotifyBaseline.ts:52-53`; `frontend/src/lib/useNotificationNotify.ts:93-103,140-152`; `frontend/src/components/ArtifactsPane.tsx:34-37,66-72`; `internal/server/handlers_artifacts.go:75-105`; `docs/features/artifacts.md:81-85`.

**Smallest change:** Carry compound session identity through notification DTOs, deduplication, and URLs. Distinguish unsupported remote artifact browsing from an empty collection and prevent local results from being attributed to remote projects. Artifact replication is unnecessary for this fix.

**Validation:** Two owners with the same session ID get independent notifications and correct destinations. Identical project paths never cross-label artifacts.

## 12. Open exact routine results from Inbox

Routine notices link to `/routines`, while selected history exists only in component state. Deleted routines disappear from the list even though history survives. A failed pre-session run or a self-deleted one-shot is therefore harder to investigate from its notification.

**Sources:** `internal/state/routine_runs.go:230-240`; `frontend/src/pages/Routines.tsx:106-118,277-279`; `internal/server/handlers_routines.go:118-129`.

**Smallest feature:** A URL identifying routine and run, read-only access to retained deleted history, and the existing Edit/Run now actions for active routines. Separate immutable run snapshots from the current definition.

**Success criterion:** One click from Inbox opens the exact outcome, including failures before session creation. Viewing history must never dispatch a run.

## 13. Make MCP setup discoverable after dismissal

“Not now” persists per endpoint in the setup prompt. There is no Settings entry to revisit the existing status/install flow. Users who later want artifacts or agent integration can end up looking for browser-storage or configuration-file workarounds.

**Sources:** `frontend/src/components/McpConfigPrompt.tsx:8-28,48-61`; `frontend/src/pages/dashboard/SettingsTab.tsx:57-71`; existing `/api/mcp/config` and `/api/mcp/config/install` routes.

**Smallest feature:** A searchable Settings entry with configuration status, endpoint, Install/Update, manual-edit instructions where needed, and Refresh. Reuse the existing safe installer.

**Success criterion:** Recover from dismissal in three or fewer UI actions without clearing browser storage.

## 14. Prevent selection of results from the previous project query

Project search waits 180ms before marking a request as loading. During that interval, previous successful results remain selectable. Typing query B after query A and immediately pressing Enter can open A's selected directory. AbortController protects later network responses, not results already displayed during debounce.

**Sources:** `frontend/src/components/CommandPalette.tsx:368-409,449-457,640-643,679-683`.

**Smallest change:** Associate results with the input query and reject selection when they differ, or invalidate results synchronously on query changes.

**Validation:** Fake timers; search A, type B, press Enter before 180ms. No A result may activate. Selecting the stale result currently browses a directory; a session launch still requires another action.

## 15. Measure sidebar scaling before replacing rendering

The session sidebar renders every matching row. That makes it a plausible long-session-list cost, but no trace established a visible regression.

**Source:** `frontend/src/pages/session-detail/SessionSidebar.tsx:299-317`.

**First step:** Benchmark deterministic 100/1,000/5,000-session fixtures and record DOM count plus search/selection interaction latency. If needed, start with incremental row reveal while keeping a selected session reachable. Use virtualization only if simpler bounding fails the measured target.

Initial conversation pagination, conversion caching, stable renderer maps, and debounced link processing already exist. A blanket conversation-renderer rewrite is not justified by this audit.

## 16. Validate demand for cross-device unsent drafts

Composer drafts survive refresh in localStorage but do not follow the user to another browser. The message queue is not a draft store because queued content can execute.

**Sources:** `frontend/src/lib/composerDraft.ts:3-51`; `frontend/src/components/assistant/useComposerDrafts.ts:33-64`; `internal/server/queue.go:28-46,74-83`.

**Potential feature:** Explicit “Save draft for another device” and Restore, text-only initially, keyed by compound session identity with conflict detection. No background sending.

**Decision gate:** Confirm that users regularly switch devices mid-draft. If they do, validate exact recovery across two browser profiles and zero sends from save/restore. Automatic synchronization is a larger commitment and is not justified yet.

## Work already present

Do not create duplicate projects for these capabilities:

- Global session snapshots, dirty-row refresh, periodic reconciliation, and singleflight.
- Owner-qualified queue locks, drain guards, bounded failure handling, and sweep recovery.
- Browser SSE inactivity recovery and remote reconnect supervision.
- Initial conversation pagination and existing rendering caches.
- Routine failure notifications, restart recovery, expired-timeout status, and webhook redelivery.
- Inbox permission decisions, search, pinning, and archive.
- Artifact download and sharing.
- Worktree dirty-tree checks and explicit force-delete retry.
- Draft restoration and failed-send preservation/retry in the current browser.

## Suggested delivery sequence

### First: correctness and small interaction fixes

Take the explicit-owner fixes, both cache scheduling issues, and composer keyboard changes. Give each failure mechanism a failing-first regression. Add the textarea label and MCP Settings re-entry as small independent changes.

### Next: scaling and unattended recovery

Bound routine-history requests, introduce bounded queue metadata reads, fix stale remote filter handling, and recover silent headless streams. Add uncertainty handling for queued delivery with explicit restart/fault-injection coverage.

### Then: complete the everyday workflows

Add touch send/queue actions, exact routine-result links, consistent overlay/mobile focus behavior, and accurate notification/artifact identity. Address sidebar rendering only after measurement. Keep cross-device drafts behind a demand check.

The success measures are concrete: zero wrong-owner calls, bounded refresh attempts, no clean-TTL full scans, constant routine polling request count, transcript-independent queue status reads, and no unexpected send/abort from ordinary text input or dialog dismissal.

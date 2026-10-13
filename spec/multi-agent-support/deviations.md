# Multi-Agent Support - Deviations from the spec

### Main-integrated creation defaults and native-provider availability recovery
- **Task:** Recovery: restore platform defaults and runtime availability after main integration (claude-code-provider.1.3.136).
- **Spec:** FR-1/2/4, AD-1/3/13. Defaults resolve before launch, selections follow their platform and machine, and native supervisors run in tmux.
- **What we did instead:** Verification at `e4d4d25` found three unapproved gaps: directory-bearing REST creation passed an empty runtime key, owner/project defaults could select and submit an OpenCode model or agent on Claude, and host capabilities advertised native-provider launch without tmux. REST creation now reuses `newSessionTarget.resolvePlatform`, including optional authoritative `remoteId`, before ensure/create. The approved small split moves catalog selection to `draftCatalog.ts`: implicit models need an available provider in the selected catalog, primary-agent defaults need catalog support, and explicit picks are preserved. Static model aliases are not a universal custom-model allowlist.
- **Representation:** Optional `hostsvc.RuntimeAvailability` adds `RuntimeAvailable() bool` alongside `RuntimeEnsurer`. Claude reports availability of its resolved CLI and tmux separately from its browsing adapter; host capabilities consult this optional method. Adapters without native prerequisites can omit it. No wire protocol, RPC, dependency or database schema changes.
- **Why:** Reuse existing owner/default resolution and let each runtime report its prerequisites without frontend platform identity checks. Recovery gate (claude-code-provider.1.3.137) approved the small catalog-default extraction before editing the oversized component.
- **Evidence:** Behavioral Go regressions failed before the fix, including missing prerequisites and mixed registries. Four real-composer rendered regressions failed with `gpt-4.1` still selected after platform/machine switches, then passed with `sonnet`, no foreign agent, and retained draft identity, text and image. Custom-model/primary-agent controls pass. Check results and all initial failures are in `defaults-recovery-report.md`.
- **Follow-up:** R1-R6 remain unresolved, partial and explicitly deferred under scope revision 32. Exact-checkpoint CI and fresh read-only verify (claude-code-provider.1.5) are still required before deliver (claude-code-provider.1.2).

Every place the implementation departs from `requirements.md` or
`architecture.md` is recorded here, in the same commit as the change.
The final conformance review (Factory task `spec-conformance`) audits the
code against the spec and adds anything missing.

A deviation is anything a reviewer reading the spec would not expect:
a different type, signature, package, protocol shape, behaviour, default,
or a requirement left unimplemented or only partly implemented.

Each entry:

```markdown
### <short title>
- **Task:** <Factory task key, e.g. claude-interactive>
- **Spec:** <FR-n / NFR-n / AD-n and the sentence departed from>
- **What we did instead:** <one or two sentences>
- **Why:** <the constraint or finding that forced it>
- **Follow-up:** <none | spec updated in this commit | ticket/issue reference>
```

If the spec itself was wrong and has been corrected, still record the entry
and set **Follow-up** to "spec updated in this commit".

## Entries

### Main integration: migration reconciliation and moved implementation paths (1.3.134)
- **Task:** Integrate current main and resolve Claude provider delivery conflicts (claude-code-provider.1.3.134).
- **Spec:** AD-1/3/7/10/12/13, FR-1/2/4/5/8; integration acceptance criteria. Approved design revision 2 and scope revision 32 remain authoritative.
- **What we did instead:** Merge main `e243236a3caac87efe4bad0399de39876d1fdab6` into the shared branch, resolving 54 conflicting files. Main independently used migration numbers 112/113, so the provider additions now run through reconciliation migration 122; the original idempotent helpers are also retained to upgrade the old provider database history. Main moved Host lifecycle, RPC, DB/TS session types, MCP creation, judge input handling and routine forms into smaller files; Claude functionality follows those paths. Custom reviewer endpoints retain precedence over platform fallback. Main's MCP default-worktree behavior now applies to Claude too, with explicit current-checkout selection still supported. Main's IndexedDB draft identity and project-default agent survive platform/machine switching. The obsolete routine form extraction is removed; its platform behavior is in `RoutineEditorDrawer`. RPC stubs are regenerated. Full inventory, paths, exact commands, original failures and reruns are in `integration-report.md`.
- **Why:** Preserve both current main behavior and completed Claude work without rewriting checkpoints or repairing explicitly deferred product defects. Recovery gate 1.3.135 approved in-place oversized source resolution without splitting; deferred `prompts.go` R5 is unchanged.
- **Follow-up:** Fresh read-only verify (claude-code-provider.1.5), then separate deliver (claude-code-provider.1.2). R1-R6 and the existing active omissions remain unresolved.

### Main-integrated check run and official coverage comparison (1.3.134)
- **Task:** Integrate current main and resolve Claude provider delivery conflicts (claude-code-provider.1.3.134).
- **Spec:** NFR-6/7; recovery gate 1.3.67's official frontend comparison obligation; integration criteria 2/3.
- **What we did instead:** Required local checks pass on the integrated tree, with exact commands and initial failures retained in `integration-report.md`. First official comparisons pass at Go 87.4 vs 86.7 and frontend 90.14 vs 89.38; final source comparisons pass at Go 87.5 and frontend 90.17, using the same baseline and unchanged 0.1% tolerance. A full server race run exhausted its default 10-minute total package budget; the same tests pass with `-timeout=20m`, 804.558 seconds, without changing any individual deadline or assertion. Isolated Chromium initially had two MCP-toast failures from an incomplete host-capability fixture; the corrected production-shaped fixture passes 317/317 with zero retries. Earlier R6 failures and pre-integration coverage failures remain evidence.
- **Why:** Main-integrated code requires new measurements and fresh verification; old passing verification cannot certify a merge.
- **Follow-up:** The deferred official comparison has been repeated, not waived. Delivery still checks its then-current head/main/baseline and required CI after fresh read-only verify (claude-code-provider.1.5).

Newest first: the delivery deferrals and report (claude-code-provider.1.3.124,
`delivery-report.md`), the recovery for verification 1.5 at `fdcda97c`
(claude-code-provider.1.3.120), the recovery for verification 1.5 at `251290f6`
(claude-code-provider.1.3.108), the recovery for verification 1.5 at `f279f45c`
(claude-code-provider.1.3.105, with its human-approved departures), the
recovery for verification 1.5 at `bf1548e2` (claude-code-provider.1.3.99), the fixes for verification 1.5
(claude-code-provider.1.3.97),
the recovery tasks (claude-code-provider.1.3.53-1.3.61), then
the entries found by the first conformance review (1.3.17), whose **Task**
names the commit that introduced the departure, then the entries recorded by
the implementing tasks. A **Follow-up** names the task that resolved the
departure, or says what stays open. The 1.3.61 re-audit checked every entry
against the code at its commit and corrected stale follow-ups in place.
Per-requirement status and evidence are in `conformance.md`.

### Delivery deferrals at `b03e27cc` (1.3.124): R1-R6 shipped unfixed
- **Task:** claude-code-provider.1.3.124 *Delivery report: document current limitations and prioritized follow-up work* (verification claude-code-provider.1.5 at `b03e27cc`, findings R1-R6; graph revision 32)
- **Spec:** FR-4, FR-5, AD-5, AD-7, AD-10, the configuration-scoped read-only contract, the 400-line source rule and NFR-7's passing checks.
- **What we did instead:** At the human's request (revision 31) the branch is delivered with these defects unfixed and marked partial in `conformance.md`. None is claimed fixed:
  - **R1** a stale turn's terminal message publication failure (`turns.go:58-86,238-259`, `live.go:121-131,156-166`) can mark the newer turn `error` and kill its child.
  - **R2** an AI reply whose HTTP response is lost or late, a restart before recording, or an unreadable approval row (`background.go:330-380`, `inherit_capture.go:131-155,295-324,363-380`) loses the AI provenance or records it as the user's.
  - **R3** `live.rename` (`live_ops.go:194-213`) skips the `foreign` check, so another configuration's session can be renamed.
  - **R4** `PreviousVersion` equals `CurrentVersion` (3); AD-5's previous version is not spoken.
  - **R5** `internal/claudeprovider/prompts.go` is 405 lines.
  - **R6** `TestStreamOnceCancellationLeavesNoGoroutines` failed once under race at verification (and at 1.3.105); reruns passed. The failure stands as evidence.
- **Why:** The human asked to deliver what exists with a follow-up report instead of further repair rounds.
- **Follow-up:** `delivery-report.md` FU-1 to FU-6 (code references, trigger, impact, regression acceptance), with the other open entries grouped in FU-7 to FU-13.

### Delivery report check run (1.3.124)
- **Task:** claude-code-provider.1.3.124
- **Spec:** NFR-7 coverage ratchet; task 1.3.124 criterion 4.
- **Environment:** macOS arm64; `GOMAXPROCS=4 GOFLAGS=-p=4`. Documentation-only change; product source identical to `b03e27cc`. Fake CLI, temporary directories and databases only; no real `claude`, and the live ocman and its databases were never contacted. No thresholds, exclusions, skips, retries or assertions changed.
- **Results:** `make lint` pass (golangci-lint 0 issues; ESLint 0 errors, 88 warnings as before; guards pass); `make test` pass (Go packages; vitest 322 files / 3,211 tests); `go test -count=1 .` pass; `go test -race -count=1` on `internal/claudeprovider`, `internal/platforms/provider`, `internal/providersupervisor`, `internal/providerprotocol`, `internal/permissions`, `internal/sessionsvc`, `internal/autoapprove`, `internal/state`, `internal/queuesvc`, `internal/hostsvc/local` and the root package's `Claude|Drain|Browser|Provider` tests pass (R6 did not recur in this run; its earlier failures at 1.3.105 and verification 1.5 stand); `pnpm build`, `go build -o /dev/null .`, `go mod tidy -diff` pass; `make proto` leaves `internal/remote/proto`, `go.mod` and `go.sum` unchanged; `make docs-build` pass (45 pages); Chromium E2E 207/207, one worker, 0 retries, on `vite preview` 127.0.0.1:18354 with `/api` and `/mcp` proxied to the closed port 1 (temporary config, removed). `make coverage SUITE=all` pass; per-suite `make coverage-check` against the gh-pages baseline (`ff09a63c`, data from main `8f588524`): Go 86.7 vs 86.7, **PASS** (+0.00); frontend 86.28 vs 89.39, **FAIL** (-3.11), the approved deferral: repeated at delivery after the main merge (gate 1.3.67).
- **Why:** Record of the required checks.
- **Follow-up:** Frontend official baseline at delivery (gate 1.3.67); R6 in `delivery-report.md` FU-5.

### Verification 1.5 recovery at `fdcda97c` (1.3.120): inherited legacy grants, stale turn failures, replayed acknowledged lifecycle, unknown reply outcomes, replayed AI provenance
- **Task:** claude-code-provider.1.3.120 (verification claude-code-provider.1.5 at `fdcda97c`, comment #41, findings R1-R5)
- **Spec:** FR-4/5, AD-4/5/7/7a/10; task 1.3.108 criteria 1-3 and 7.
- **What we did instead:** The 1.3.108 entry below overstated these paths; each now has a test that failed on the unfixed code. Behaviour and the type/signature/structure departures:
  - **R1, approvals children inherited before exact patterns.** New `provider.(*Rules).EscapeInheritedApprovals([]state.ApprovedPermission)` (file `provider/rules_upgrade.go`, which also now holds `escapeLegacyLocked`) and store marker `exactRules`: once, at Claude startup before any adapter serves rules (`claude_platform.go`, after `EscapeLegacyApprovalPatterns`), every `allow` rule in `claude-rules.json` `rules` whose permission and pattern equal a recorded approval's permission key and literal value is escaped (`echo *` → `echo \*`), and a pattern-less `webfetch`/`websearch` allow for a key recorded per URL or query is removed. Denies are never touched. A user-written allow that coincides with an approval is narrowed the same way (it only ever narrows; the user is asked again). Approvals come from the new `state.DB.ListPlatformApprovals(ctx, platform)`; a read failure disables Claude sessions rather than serving unconverted rules. A grandchild forked afterwards inherits the converted live rules. `provider` tests now import `internal/permissions` and `internal/state`. `TestLegacyInheritedRulesBecomeExact` (child forked before the upgrade, reloaded, grandchild forked after; the bypass command and any URL asked in both; user glob and deny kept; second start converts nothing).
  - **R2, stale turn failure.** `settle` handles its publication failure with new `live.turnFailed(id, err, gen)`, which marks the session failed and kills a busy child only while `l.accepted[id] == gen`, checked under `l.mu` (which `BeginTurn`'s accept also takes); a newer accepted turn is left alone and the old end stays with the unsettled retry. `publishFailed` keeps its signature for event failures within the running turn. New `testHook("failed")` point. `TestStaleTurnFailureSparesNextTurn` (A's terminal publication fails; paused before failure handling: spool restored, B accepted and running; B stays busy and finishes `done`).
  - **R3, replayed acknowledged lifecycle.** `providerprotocol.Event` gains `Acknowledged bool` (`json:"acknowledged,omitempty"`), and `CurrentVersion` is 3. The supervisor's headless stream sets it on `session.*` records at or before the session's acknowledged sequence (kept only because a late resolution retains its ask). `Adapter.observe` applies such an event to its caches but calls no sink and no event handler, so a fresh dashboard replaying from 0 neither drains held messages nor completes routines on a turn end it already handled. Prompt records keep their metadata. `TestRetainedIdleDoesNotDrainQueueIntoNextTurn` (late resolution after A's acknowledged idle, B running, C held in `queuesvc` with `forceQueue`; a fresh `provider.Adapter` replays from 0 into the real `queuesvc.Service.Flush`; C is not sent until B's own idle). `lateResolution` helper extracted from `TestLateResolutionAfterIdleAndReconnect`, whose assertions are unchanged.
  - **R4, unknown reply outcomes.** `PreviousVersion` now equals `CurrentVersion` (3) and `CheckVersion` accepts the range `PreviousVersion..CurrentVersion`; version 1 (raw patterns, generic 503 after a delivered reply) and version 2 (unmarked acknowledged lifecycle) supervisors drain and are replaced once idle. New `ReplyOutcomeVersion` = 2, new optional interface `platforms.ReplyOutcomeReporter{ReplyOutcomes(ctx, sessionID) bool}`, implemented by `provider.Adapter` (false below version 2) and `claudePlatform` (the adapter answering that session's prompts; false when none resolves). `respondAndPersistSafeApproval` (judge and safe-command cache) does not reply for such a session: the prompt is flagged for the user with "auto-approve is unavailable until this session's provider is replaced". A generic 503 is never read as success. `TestLegacySupervisorLeavesApprovalToUser` (a v1-style reply: delivered while the spool is down, failed generically; no user `once` row, prompt left pending), `TestLegacySupervisorPatternsAreExact` (now connects the version-1 supervisor with `NewDraining` and checks `ReplyOutcomes`), `TestVersionsAndHandshakeErrors`.
  - **R5, replayed AI approval.** New `state.DB.ApprovedPermission(ctx, platform, sessionID, permissionID)`, read through an optional `approvalReader` interface (the `SettingsStore` interface is unchanged). `HandlePermissionReplied`, with no in-memory AI response for the prompt, reads the recorded row first: an `ai` row makes the observed reply a replay of that approval, so the pipeline persists the `auto_approved` lifecycle at the row's time, emits `ocman.permission.auto-approved` built from the row (actor, reply, reasoning) and broadcasts the resolution as `auto-approved`, and records no user lifecycle, row or `ocman.permission.approved`. The frontend is unchanged: its notice is keyed by permission id, and the replay and the persisted notice now agree. `TestDelayedAIApprovalKeepsProvenance` (restarted pipeline: emitted events and lifecycle updates checked), `TestApprovedPermissionLookups`, reducer test *shows a replayed AI approval as the AI before or after the persisted notice*.
- **Why:** Verification 1.5 at `fdcda97c` (comment #41) found R1-R5 by static review.
- **Evidence:** Every test above failed on the unfixed code: R1 the bypass command and any URL allowed in child and grandchild; R2 B killed, status `error`; R3 C sent while B ran; R4 a `user`/`once` row for the AI's delivered approval; R5 `ocman.permission.approved` with `approvedBy: user`.
- **Limits:** A Claude session on a remote ocman is not covered by `ReplyOutcomes` (the remote's own pipeline answers its prompts). While an old supervisor drains, its sessions get no automatic approvals. An unreadable approval row (store error) still attributes an observed reply to the user, as before, and is logged. `TestSendRacingLimitResetKeepsTurnOpen` timed out once in a loaded full-package run and passed 5/5 in isolation; it uses a one-second reset and is unchanged here.
- **Follow-up:** Overstated at `b03e27cc` (verification 1.5, findings R1-R6) and deferred unfixed by graph revision 32: R2 here guards only the `settle` path, not a failed terminal message (delivery R1); R4 reuses no previous version (delivery R4); R4/R5 do not cover a reply whose HTTP response is lost, a restart before recording, or an unreadable row (delivery R2). See *Delivery deferrals at `b03e27cc` (1.3.124)*.

### Recovery check run (1.3.120)
- **Task:** claude-code-provider.1.3.120
- **Spec:** NFR-7 coverage ratchet; task 1.3.120 criterion 6.
- **Environment:** macOS arm64; `GOMAXPROCS=4 GOFLAGS=-p=4`. Fake CLI, temporary directories and databases only; the live ocman was never contacted. No thresholds, exclusions, skips or retries changed.
- **Results:** `make lint` pass; `make test` pass (Go packages; vitest 322 files / 3,211 tests); `go test -count=1 .` pass; `go test -race -count=1` on `internal/claudeprovider`, `internal/platforms/provider`, `internal/providersupervisor`, `internal/permissions`, `internal/providerprotocol`, `internal/sessionsvc`, `internal/autoapprove`, `internal/state`, `internal/queuesvc` and the root package's Claude, drain, browser and provider tests pass; `vite build`, `go build -o /dev/null .`, `go mod tidy -diff` pass; `make proto` leaves the tree unchanged; `make docs-build` pass; Chromium E2E 207/207, one worker, 0 retries, on `vite preview` 127.0.0.1:18353 with `/api` and `/mcp` proxied to the closed port 1 (temporary config, removed). `make coverage SUITE=all` pass; against the gh-pages baseline (`ce615927`, data from `4434a228`): Go 86.7 vs 86.7, **PASS** (+0.00); frontend 86.28 vs 89.33, **FAIL** (-3.05), repeated at delivery after the main merge (gate 1.3.67).
- **Why:** Record of the required checks.
- **Follow-up:** Frontend official baseline at delivery (gate 1.3.67).

### Verification 1.5 recovery at `251290f6` (1.3.108): pattern upgrade, settlement generations, late resolutions, exit after delivery, drain subscription order, rules-store outages, delayed AI provenance
- **Task:** claude-code-provider.1.3.108 (verification claude-code-provider.1.5 at `251290f6`, comment #34, findings V1-V7)
- **Spec:** FR-4/5, AD-4/5/7/7a/8/10; task 1.3.105 criteria 1-5 and 7.
- **What we did instead:** The 1.3.105 entry below overstated these paths; each now has a test that failed on the unfixed code. Exact behaviour and the structural departures they add:
  - **V1, upgrade to exact patterns.** `providerprotocol.CurrentVersion` is now 2 and `PreviousVersion` 1 (new `ExactPatternsVersion` = 2). A version-1 supervisor that survived the upgrade stays in range and is reused, but its prompt `patterns`/`always` are raw tool arguments: the adapter escapes them with `ExactPattern` on arrival (headless and browser streams, snapshots, `GET /permissions` reconciliation; `provider/patterns.go`), before rules, judge, audit capture or a browser see them, so it cannot create a glob grant. Version 0 is now out of range (drained). `claude-rules.json` gains `exactPatterns`: a store without it is converted once on open, escaping every `approved` and `tentative` pattern (each was a prompt's literal argument; `*` for a whole tool still matches whole-tool prompts), while `rules` (explicit YOLO/custom/inherited rulesets) keep their globs. `state.DB.EscapeLegacyApprovalPatterns`, run once at startup before anything inherits (marker setting `claudecode.exact_approval_patterns`, one transaction, fatal on failure), escapes the patterns of `claudecode` and `r-<id>:claudecode` rows in `auto_approved_permission`; OpenCode rows keep OpenCode's globs. Departures: an approval or audit row written by the intermediate `e2726ae2`-`251290f6` builds (already escaped, also under protocol 1) is escaped again, which only narrows it (the user is asked again); an inherited rule that a pre-upgrade child already holds in its explicit ruleset cannot be told apart from a user glob and is not converted; a remote ocman still running a pre-`e2726ae2` branch build reports raw patterns under the unchanged remote protocol 7 (not bumped: main's remotes have no Claude provider). `TestLegacySupervisorPatternsAreExact`, `TestLegacyRulesStoreApprovalsBecomeExact`, `TestLegacyClaudeApprovalsInheritExact`, `TestVersionsAndHandshakeErrors`.
  - **V2, settlement generations.** One `settleLater` worker per session (`live.settling`) retries whatever generation `live.unsettled` holds until none is left; a superseded retry no longer ends the worker, and it removes only the entry it settled. `settle` removes or replaces an entry only for its own or a newer generation, and a retry publishes only while its entry is still the unsettled one, so no generation is abandoned and none is published twice. New `testHook("retry")` point. `TestSettlementRetryOwnsEveryGeneration` (A's worker reads A, B is accepted and fails publication, A's retry is superseded; after restoring the spool, one `error` idle edge, supervisor released, drainable).
  - **V3, late resolution after idle.** A headless reconnect's re-delivery of a pending prompt (`Supervisor.append(..., replay)`) no longer sets the session active, so once the prompt is resolved `/drain` and `Active` report idle. `prune` keeps, from the first one on, an ask whose resolution was spooled after the acknowledged sequence, so a consumer replaying the resolution after a restart still reads the ask's patterns and metadata; the next acknowledged turn prunes both. New `testHook("record")` point. `TestLateResolutionKeepsAskAndDoesNotReopenTurn` (with and without reconnect), `TestLateResolutionAfterIdleAndReconnect` (resolution retry held while the idle edge is published and acknowledged and the watcher reconnects).
  - **V4, exit after delivery.** An exiting child's cleanup (`cancel(p, "")`) no longer cancels a prompt whose `control_response` write is in flight: it marks it `exited`, and the write's result decides, the delivered decision when the write succeeded (recorded, reported as success, Allow-always kept), a cancellation only when it failed. Claude's own `control_cancel_request` during the write still resolves as cancelled. `TestDecisionDeliveredBeforeExitStands` (writer forwards the bytes, then holds its return while the fake consumes them and exits). The race in `TestSpoolOutageAcrossContinuationAndExitSettles/exit` (kill before the fake logged the decision it read) now waits for that log; its assertions are unchanged.
  - **V5, drain subscription order.** `claudePlatform.ProxyEvents` captures `projects.attached` before reading drain or route state, so a drain discovered at any point afterwards closes the captured channel; one discovered before is seen by `drainingFor`. New `claudeTestHook("routed")` point. `TestBrowserConnectedBeforeDrainDiscoveryReceivesPrompts` runs with discovery once browsing and between the route lookup and the subscription; no polling.
  - **V6, rules-store outage after the tentative save.** `Adapter.settleAlways` returns its error. The headless stream settles a tentative Allow-always from `permission.replied` before advancing its cursor and stops there on failure, so the event is replayed and its turn left unacknowledged (spool retained) until the store records the outcome, including across a restart. `RespondPermission` still returns success once Claude has the reply (its failed settle is logged; the replayed event settles it). Departure: while `claude-rules.json` cannot be written, that adapter's headless stream retries at that event every second and delivers nothing after it. `TestAllowAlwaysOutcomeSurvivesStoreFailure`.
  - **V7, delayed AI provenance.** The Claude supervisor answers a reply Claude received but could not record with 503 and code `reply_unrecorded`; the adapter returns `platforms.ErrReplyUnrecorded`, which `respondAndPersistSafeApproval` (judge and safe-command cache) treats as the AI's delivered approval: audit row `ai`/`once`, inherited by children, and the delayed `permission.replied` is not captured as the user's. `RecordApprovedPermission` no longer lets a `user` row replace an `ai` row for the same permission, so a replay to a restarted pipeline keeps the AI provenance. `statusError` now unwraps. `TestDelayedAIApprovalKeepsProvenance` (judge and cache, delivered while the spool is down, delayed publication, then replay to a fresh pipeline), `TestAIApprovalIsNotOverwrittenByUser`.
- **Why:** Verification 1.5 at `251290f6` (comment #34) found V1-V7 by static review.
- **Evidence:** Every test above failed on the unfixed code: V1 handler saw `echo *`, legacy `echo *` allowed `echo harmless; touch ...`; V2 no idle edge after restore; V3 supervisor never released, reply-only spool; V4 delivered decision reported as not found; V5 no prompt on the open stream; V6 stream moved past the unrecorded outcome; V7 no AI row (judge) and `user/once` after replay. `TestLegacyClaudeApprovalsInheritExact` and `TestAIApprovalIsNotOverwrittenByUser` exercise new functions.
- **Follow-up:** Overstated at `fdcda97c`: V1 for inherited approvals already in children's rulesets (left as globs), V2 for a failed old turn end handled after a newer turn was accepted (killed it), V3 for the acknowledged idle edge the late resolution retained (replayed as a new turn end), V7 for surviving version-1 supervisors and for the replayed reply's emitted provenance. See *Verification 1.5 recovery at `fdcda97c` (1.3.120)*.

### Recovery check run (1.3.108)
- **Task:** claude-code-provider.1.3.108
- **Spec:** NFR-7 coverage ratchet; task 1.3.108 criterion 8: lint, Go and frontend tests, targeted race, builds, tidy, proto with no diff, docs, isolated Chromium and the current official coverage comparison.
- **Environment:** macOS arm64; Go with `GOMAXPROCS=4 GOFLAGS=-p=4`. Fake CLI and temporary directories/databases only; the live ocman on :8228/:8229 was never contacted. No thresholds, exclusions, skips or retries changed.
- **Results:** `make lint` pass (0 Go issues; ESLint 0 errors, 88 warnings as before); `make test` pass (Go packages; vitest 322 files / 3,210 tests); `go test -count=1 .` pass; `make coverage SUITE=go` (all `internal/...` tests) and `SUITE=frontend` pass; `go test -race -count=1` on `internal/claudeprovider`, `internal/platforms/provider`, `internal/providersupervisor`, `internal/permissions`, `internal/providerprotocol`, `internal/sessionsvc`, `internal/autoapprove`, `internal/state` and the root package's Claude, drain and browser tests pass. The first race run flagged two test-only races introduced here (the package-level `testHook` read by a background retry that outlived its test, and a test reading the event recorder without its lock); `testHook` is now an atomic pointer and the test copies under the lock; the rerun is clean. `vite build`, `go build -o /dev/null .`, `go mod tidy -diff` pass; `make proto` leaves the tree unchanged; `make docs-build` pass; Chromium E2E 207/207, one worker, 0 retries, against the fresh build on `vite preview` 127.0.0.1:18351 with `/api` and `/mcp` proxied to the closed port 1 (temporary config, removed). Coverage against the gh-pages baseline (`ed07a829`): Go 86.7 vs 86.7, **PASS** (+0.00); frontend 86.27 vs 89.28, **FAIL** (-3.01), unchanged since `bf1548e2` (no frontend code changed) and repeated at delivery after the main merge (gate 1.3.67).
- **Why:** Record of the required checks.
- **Follow-up:** Frontend official baseline at delivery (gate 1.3.67).

### Verification 1.5 recovery at `f279f45c` (1.3.105): exact Allow-always, durable turn ends and cancellations, conflicting retries, drain discovery order
- **Task:** claude-code-provider.1.3.105 (verification claude-code-provider.1.5 at `f279f45c`, comment #30, findings F1-F5, D1-D2)
- **Spec:** FR-4/5, NFR-2, AD-1/4/5/7/7a/10; task 1.3.99 criteria 1-3, 7 and 10.
- **What we did instead:** The 1.3.99 entry below and *Claude "Allow always" approves the exact request* overstated these paths; each now has a test that failed on `f279f45c`. Exact behaviour and departures:
  - **F1, exact Allow-always.** Permission patterns gain one escape (`providerprotocol.ExactPattern` / `PatternValue`): a backslash before `*`, `?` or another backslash makes that character literal; any other backslash is itself. The Claude provider publishes a tool call's command, path, URL, pattern or query as an exact pattern in both `patterns` and `always` (`echo \*`); a tool without such an argument keeps `*` (the whole tool). `provider.Rules` matches rule patterns with the escape and compares them against the prompt's literal values, so an approved `echo *` never allows `echo harmless; touch /tmp/unapproved`. Approvals recorded in `auto_approved_permission` carry the exact form, so inherited rules stay exact. For `claudecode` (local or `r-<id>:claudecode`), `permissions.BuildInheritedRules` inherits the flat OpenCode keys `webfetch`/`websearch` per recorded pattern instead of as one allow-all rule; OpenCode keeps its flat inheritance. Departures: a user-written rule in ocman's Claude rules containing `\*`, `\?` or `\\` now means the literal character, and the permission card shows the escaped form for a call whose argument contains `*` or `?`. `TestAllowAlwaysWithWildcardCharactersIsExact` (Bash `echo *`, Write `/tmp/a*b`, WebFetch `https://example.com/?q=1`: the same call is allowed, a call those characters would glob-match is asked in the session, after reloading `rules.json` and in an inheriting child), `TestExactPatternsDoNotGlob`, `TestExactPatternRoundTrip`, `TestFlatKeysAreExactOnClaude`.
  - **F2, turn end during a spool outage.** A `settle` whose status or idle edge the spool cannot take marks the session `error` (as before) and records it in `live.unsettled`; one retry per session (`settleLater`, 1 s doubling to 1 min) publishes the `error` status and idle edge through the same `PublishIf` check once the spool works, unless a later turn was accepted. The supervisor then releases the turn, the headless watcher acknowledges it, the idle sink drives queue drain and routine completion, and `/drain` reports idle. `TestSpoolOutageAcrossContinuationAndExitSettles` (Claude continues after a delivered but unrecorded decision and is stopped, or exits; no idle edge and the supervisor turn stays held during the outage; after restoring: one `error` idle edge, one recorded reply, one decision on stdin, supervisor released, drainable).
  - **F3, cancellation.** A prompt is registered only in the publish callback of its `permission.asked`/`question.asked` (an unspooled ask is never answerable). A cancellation (interrupt or child exit) no longer deletes the prompt: it sets the prompt's resolution to the cancellation event, which is spooled (retried by `recordLater` like a reply) and only that removes the prompt from the backend and supervisor. A cancelled prompt is unanswerable (404) and is left out of `GET /permissions`/`/questions`. A cancel during an in-flight `control_response` write marks the prompt; when the write returns, the prompt resolves as cancelled and the caller gets 404, so no second decision is written and nothing is recorded twice. `TestCancellationDuringOutageIsRecorded` (interrupt and exit), `TestCancelDuringReplyWriteResolvesOnce`.
  - **F4, browser before drain discovery.** `claudeProjects.onDrain` signals the same `attached` channel an attach closes. A browser stream already reading the browsing supervisor for a launched session then continues in the same response as the draining stream (`proxyDraining`: pending prompts, later prompt events and their resolutions), instead of waiting for a reconnect; an attach still ends the stream so the client reconnects. `TestBrowserConnectedBeforeDrainDiscoveryReceivesPrompts` orders recovery after the stream with the `claudeTestHook` seam and asserts permissions, a question and their resolutions arrive on the open stream, without polling. `TestClaudeSupervisorSurvivesOcmanRestart` now waits on the same seam instead of a 100 ms sleep (it failed on `f279f45c` on a loaded machine when the attach won the race).
  - **F5, conflicting retry.** A reply to a prompt whose earlier decision Claude received but the spool could not record succeeds only when it repeats that decision (same event type and properties; then it is recorded). A different reply, answer or dismissal is refused with 422 ("claude already received a different decision"), so `sessionsvc` runs no success hook for it. The background retry records the decision Claude received. "Allow always" is still persisted before the reply is sent, but as a tentative approval keyed by permission id (`rules.json` `tentative`) that grants nothing: it moves to the session's approvals when the reply succeeds or the replayed `permission.replied` says `always`, and is dropped on any other outcome, across restarts. `TestConflictingReplyRetryIsRefused` (through `sessionsvc.Service` with autoapprove provenance: always/reject, reject/always and once/always; hooks, rules and recorded approvals follow the delivered decision), `TestConflictingQuestionRetryIsRefused`, `TestTentativeAllowAlways`. `TestUnrecordedReplyIsRetriedNotRedelivered` now retries with the same decision; its earlier assertion that a conflicting reject succeeded is removed.
  - **D1, helper budget, approved.** The 4 + 2 Claude processes of *Verification 1.5 recovery (1.3.99)* F7 were approved by the human at recovery gate claude-code-provider.1.3.107 ("Approve both departures as implemented").
  - **D2, EnsureRuntime shape, approved.** *EnsureRuntime returns the existing result struct; adapters plug in through RuntimeEnsurer; the RPC carries JSON* was approved at the same gate.
- **Why:** Verification 1.5 at `f279f45c` (comment #30) found F1-F5 by static review and asked for explicit decisions on D1-D2.
- **Evidence:** Every F test above failed on the unfixed code (F1 different call never asked; F2 no idle edge after restore; F3 prompt dropped before its cancellation was recorded; F4 no prompt on the open stream; F5 conflicting retry acknowledged).
- **Follow-up:** Overstated at `251290f6`: F1 for surviving version-1 supervisors, existing approvals and audit rows; F2 for a second generation failing while a retry ran; F2/F3 for a resolution recorded after the idle edge and a reconnect; F3 for a child exiting after a delivered decision; F4 for discovery before the stream subscribed; F5 for a rules-store failure after the tentative save and for AI approvals recorded late. See *Verification 1.5 recovery at `251290f6` (1.3.108)*.

### Recovery check run (1.3.105)
- **Task:** claude-code-provider.1.3.105
- **Spec:** NFR-7 coverage ratchet; task 1.3.105 criterion 7: lint, Go and frontend tests, targeted race, builds, tidy, proto with no diff, docs, isolated Chromium and the current official coverage comparison.
- **Environment:** macOS arm64; Go with `GOMAXPROCS=4 GOFLAGS=-p=4`. Fake CLI and temporary directories/databases only; the live ocman on :8228/:8229 was never contacted. No thresholds, exclusions, skips or retries changed.
- **Results:** `make lint` pass (0 Go issues; ESLint 0 errors, 88 warnings as before); `make coverage SUITE=go` (all `internal/...` tests) and `go test .` pass; vitest 322 files / 3,210 tests pass; `go test -race` on `internal/claudeprovider`, `internal/platforms/provider`, `internal/providersupervisor`, `internal/permissions`, `internal/providerprotocol`, `internal/sessionsvc` and the root package's drain and Claude tests pass; in `internal/autoapprove` (unchanged) `TestStreamOnceCancellationLeavesNoGoroutines` failed once under race with a context-cancel timing error and passed 5/5 on rerun; `vite build`, `go build -o /dev/null .`, `go mod tidy -diff` pass; `make proto` leaves `internal/remote/proto`, `go.mod` and `go.sum` unchanged; `make docs-build` pass; Chromium E2E 207/207, one worker, 0 retries, against the fresh build on `vite preview` 127.0.0.1:18350 with `/api` and `/mcp` proxied to the closed port 1. Coverage against the gh-pages baseline (main `14219a482`): Go 86.7 vs 86.7, **PASS** (+0.00); frontend 86.27 vs 89.24, **FAIL** (-2.97), unchanged since `bf1548e2` (no frontend code changed) and repeated at delivery after the main merge (gate 1.3.67). Two root-package tests that failed here on `f279f45c` were made deterministic without changing assertions: `TestClaudeSupervisorSurvivesOcmanRestart` (attach raced the browser's routing under load; now ordered by the `claudeTestHook` seam) and `TestProtocolRoundTrip` (listed sessions read before the initial sync indexed the main transcript, 1 in 20 under race; now waits for it). `TestStartupWithoutOpenCode` fails under `-race` only (its 1 s SIGTERM fires before a race-instrumented startup reads its identity) and passes in `go test .`; it is unchanged.
- **Why:** Record of the required checks.
- **Follow-up:** Frontend official baseline at delivery (gate 1.3.67).

### Verification 1.5 recovery (1.3.99): lifecycle, prompt durability, drain delivery, configuration scope, helper budget
- **Task:** claude-code-provider.1.3.99 (verification claude-code-provider.1.5 at `bf1548e2`, comment #27, findings F1-F8)
- **Spec:** FR-3/4/5, NFR-2, AD-3/4/5/7/7a/10; the approved .31 replay contract; task 1.3.97 criteria 1-6.
- **What we did instead:** The 1.3.97 entry below overstated these paths; each now has a test that failed on `bf1548e2`. Exact behaviour and departures:
  - **F1, result before ticket removal.** A `settle` that is held back only because the turn's own input still has a wait ticket (the reader handled the result before the sender removed the ticket) records the outcome in `live.deferred`; the sender's cleanup, when it is the last ticket and no child is busy, publishes that outcome through the same `PublishIf` check. A newer `BeginTurn` clears it. Exactly one final idle edge. `TestResultBeforeTicketRemovalSettles` (new `testHook("written")` point).
  - **F2, reply delivered but not spooled.** `answer` now returns an error (503) when Claude received the `control_response` but the reply event could not be spooled: the prompt stays in the backend and supervisor pending maps holding the recorded reply (`prompt.reply`), is not cancelled by the child's exit, and is never written to Claude again. A later answer (since 1.3.105 only the same decision; a different one is refused with 422) or a background retry (`recordLater`, 1 s doubling to 1 min, until spooled or the prompt is gone) records the decision Claude actually received, which removes it from both maps, so drain and prune proceed. The 1.3.97 claim that persistence failures are never acknowledged was false for this boundary. `TestUnrecordedReplyIsRetriedNotRedelivered` (both recovery paths; one `control_response` reaches the CLI; `/drain` idle afterwards).
  - **F3, out-of-range drain.** `provider.NewDraining` now also reads the headless event stream (`GET /events?consumer=headless`), a version-independent route; of its events only `permission.*`/`question.*` are applied, nothing is acknowledged and `ProxyEvents` is unsupported on it. Its pending lists are the event-fed cache (no `GET /permissions` polling). `claudeProjects.onDrain` wraps it as `drained`: the dashboard's `SetEventHandler` pipeline (rules, judge, Inbox, Allow-always provenance), the session event sink and the shared `claude-rules.json` store are attached as for a normal supervisor, and `claudePlatform.ProxyEvents` serves a draining session's browser stream as the browsing transcript stream plus the draining supervisor's pending prompts and every later prompt event, ending when the drain ends. Departure: those prompt frames carry no SSE `id:` (the draining supervisor's sequence is not the browsing stream's); a browser that stalls past a 64-event buffer misses frames until it reconnects, which relists pending prompts. `TestOutOfRangeSupervisorDeliversPromptsWhileDraining` (real `providersupervisor` speaking protocol 99: initial and later permissions and a question reach pipeline and browser, resolutions visible, Allow-always recorded with `always` provenance and persisted, the next identical prompt answered by the rule and hidden, no `GET` reaches the old supervisor) replaces `TestOutOfRangeSupervisorKeepsPendingPermissionAnswerable`; `TestDrainingAdapterUsesOnlyVersionIndependentRoutes` updated.
  - **F4, completion between REST and snapshot.** `live.snapshot` appends `session.idle` when the session's settled status is `done` or `error`, no input waits and no child is busy; an `error` turn also keeps its `session.status`. A browser that read `busy` over REST and attached after the turn ended receives the terminal state from the snapshot. `TestSnapshotCarriesTerminalState`.
  - **F5, replay-echo ownership failure.** A failed `started` update latches `proc.startErr`, records the session in `live.unrecorded` and kills the child: the turn settles `error` even if storage recovers before the result. The next spawn records `Started` first and fails the turn visibly if it still cannot, so an existing transcript is never relaunched with `--session-id`. Departure: the latched session's in-flight output is lost with the killed child (the transcript keeps what the CLI wrote). `TestStartedPersistenceFailureFailsTurn`.
  - **F6, configuration scope.** `owned` gained `Config`, the Claude config directory the session was created or forked under. Under another `ConfigDir` such a session is listed read-only from the index only (no pending row), and send, command, compact, fork and spawn return 403 "unavailable under <config>"; switching back makes it drivable. Sessions recorded before this change have no `Config` and stay drivable under any configuration (no migration). The fake CLI gained `FAKE_CLAUDE_VALIDATE_RESUME`, which keeps session ids per `CLAUDE_CONFIG_DIR` and fails `--resume` of an id that configuration never created. `TestProjectSupervisorFollowsDashboardConfiguration` (config A session refused under B, a new session runs under B, the A session resumes after switching back, all with resume validation).
  - **F7, helper budget (needs approval).** One-shot `claude` calls (initialize probes for the command catalog and account, `autoapprove.ClaudeJudge`, `WorktreeName`) take one of `claudeprovider.HelperProcs` = 2 machine-wide `helper-N` flock slots under the same `claude-slots` directory, separate from the 4 pooled-children slots, so at most 4 + 2 Claude processes run at once and a judge never waits behind permission-waiting sessions. `--version` and `claude plugin install/uninstall` (user-initiated, once per supervisor or click) are not counted. This is a separate helper budget beside NFR-2's cap of 4, not within it; approved by the human at recovery gate claude-code-provider.1.3.107. `TestHelperSlotsAreBoundedAndSeparate`, `TestClaudeJudge` (judge holds and releases a slot; without one it needs the user and never runs the CLI).
  - **F8, slot I/O errors.** `trySlot` distinguishes contention (`EWOULDBLOCK`) from any other directory, open or flock error; the pool fails the accepted turn visibly with "claude process slots: ..." and helpers return the error instead of waiting. `TestBrokenSlotStoreFailsVisibly`.
  - **Test evidence.** The fake CLI matches a recorded `control_request` row's `request.subtype` exactly (`TestFakeCLIRejectsMalformedInput` "other subtype"), so `TestAbortInterruptsTurn` and `TestInterruptCancelsPrompt` accept only an interrupt, and the abort test asserts exactly one interrupt on stdin. `TestClaudeComposerStart` holds the immediate follow-up's turn open, asserts the queued follow-up does not reach the child mid-turn, then reaches it once, after the immediate one, after the idle edge. With `FAKE_CLAUDE_MCP_LOG` the fake sends an MCP `initialize` to each `--mcp-config` server at start; `TestDashboardKillRestartComposition` asserts the first child and the post-restart child each got an answer from the listener actually bound by that dashboard process, not only the argument.
- **Why:** Verification 1.5 at `bf1548e2` (comment #27) found these paths broken or unevidenced; the 1.3.97 entry and `conformance.md` overstated them.
- **Evidence:** Each test above was run on the unfixed code and failed (F1 idle-edge timeout; F2 reply acknowledged; F3 no prompt delivered while draining; F4 no terminal frame; F5 status `done`; F6 no refusal, the resume went to the other configuration; F8 is new behaviour with a new seam; F7 is a new budget).
- **Follow-up:** F7's separate helper budget was approved at recovery gate 1.3.107. F2's "any reply", the exit/spool paths and cancellation were incomplete at `f279f45c`; see *Verification 1.5 recovery at `f279f45c` (1.3.105)*. The intermittent check failures are recorded in *Intermittent check failures at verification 1.5 (1.3.99)*.

### Recovery check run (1.3.99)
- **Task:** claude-code-provider.1.3.99
- **Spec:** NFR-7 coverage ratchet; task 1.3.99 acceptance: lint, tests, builds, tidy, proto with no diff, docs, isolated Chromium and coverage.
- **Environment:** macOS arm64; Go with `GOMAXPROCS=4 GOFLAGS=-p=2`. Fake CLI and temporary directories/databases only; the live ocman on :8228/:8229 was never contacted. No thresholds, exclusions, skips or retries changed.
- **Results:** `make lint` pass (0 errors, 88 ESLint warnings as before); `go test ./...` pass; `go test -race` on `internal/claudeprovider`, `internal/platforms/provider`, `internal/providersupervisor`, `internal/hostsvc/local`, `internal/autoapprove` and the root package's drain, configuration, kill-restart and composer tests pass; vitest 322 files / 3,210 tests pass; `vite build`, `go build -o /dev/null .`, `go mod tidy -diff` and the desktop build pass; `make proto` leaves `internal/remote/proto` unchanged; `make docs-build` pass; Chromium E2E 207/207, one worker, 0 retries, against the fresh build on `vite preview` 127.0.0.1:18349 with `/api` and `/mcp` proxied to the closed port 1. Coverage against the gh-pages baseline (main `d87abba78`): Go 86.6 vs 86.7, **PASS** (-0.10, tolerance 0.1); frontend 86.27 vs 89.23, **FAIL** (-2.96), unchanged from `bf1548e2` (no frontend code changed) and repeated at delivery after the main merge (gate 1.3.67).
- **Why:** Record of the required checks.
- **Follow-up:** Frontend official baseline at delivery (gate 1.3.67).

### Intermittent check failures at verification 1.5 (1.3.99)
- **Task:** claude-code-provider.1.3.99
- **Spec:** NFR-7; task 1.3.99 criterion 9.
- **What we found:**
  - `TestHandleTmuxStatus_NoServerReportsAvailable` (sessions and clients, both 2 s context deadlines, under `make coverage SUITE=go`). The test writes a fresh `tmux` shell script and its first execution pays macOS executable validation, measured here at 0.14-0.31 s idle versus 0.01 s warm; under coverage load with parallel packages that exceeded `tmux`'s 2 s `commandTimeout`. The same cause was already fixed for the provider runtime tests by warming the binary (`provider_runtime_test.go`, `claude_projects_test.go`). Fix: `installFakeTmux`/`installFakeTmuxWithSessions` execute the script once before returning. The handler deadline and every assertion are unchanged.
  - `NewConversation.lifecycle.test.tsx:173` ("does not let a follow-up overtake a pending first upload": second `sendMessage` expected, one observed). Not reproduced: 8 isolated runs, 6 runs with ten CPU-saturating processes and the full frontend coverage runs of this task all passed. Reading the path: the second call is dropped only if `Composer.submit` sees an empty textarea (`routeComposerSubmit` noop) or the keydown arrives while `disabled || sendingRef.current`; nothing in the branch's diff touches that component, its draft hook or `firstSubmission.ts`. No cause was proven, so nothing was changed and the assertion is kept as is.
- **Why:** Criterion 9 asks for an investigation without weakening assertions or thresholds.
- **Follow-up:** The frontend case stays recorded as unexplained if it recurs.

### Verification 1.5 blockers fixed (1.3.97): durability, drain, launch configuration
- **Task:** claude-code-provider.1.3.97 (verification claude-code-provider.1.5, comment #23)
- **Spec:** FR-4/5/7, AD-3/4/5/7/7a/10, NFR-2; recovery tasks 1.3.54-1.3.60.
- **What we did instead:** Implemented as specified, with these exact departures:
  - Persistence failures are returned, never acknowledged (not true for a prompt reply Claude received whose event could not be spooled, nor for a failed `started` update at the replay echo, which only showed an error message; both fixed by 1.3.99). `provider.OpenRules(path) (*Rules, error)` refuses an unreadable or corrupt `claude-rules.json` (Claude stays unregistered at startup rather than dropping saved denies); `SetPermissionRules` and an "Allow always" reply return the write error and leave memory as on disk. "Allow always" is persisted before the reply is sent, so a failed write keeps the prompt pending and answerable (since 1.3.105 as a tentative approval that grants nothing until the reply Claude received is known). Ownership (`claude-sessions.json`) is never written without its `flock`: a lock, read or write failure fails create, fork, rename and the title flush, and restores memory from disk; a failure to record a session's first transcript (`started`) fails that turn visibly. An event the spool cannot record (`Supervisor.Publish` error) marks the session `error` in the supervisor, kills its running child (which cancels its prompts) and is logged; while the spool cannot write, `BeginTurn` refuses new turns (409). There is no dedicated UI notice beyond the error status, the turn's error message when it can be spooled, and the log.
  - An idle edge is published only while no turn was accepted since the caller decided the turn was over, none waits to be written and no child is busy: `Supervisor.PublishIf` evaluates that under the publish lock `BeginTurn` takes, and the live backend counts accepted turns per session. A send racing a result, a usage-limit reset or an abort therefore owns the next idle edge. Package-level `testHook` is the test seam for those windows.
  - The machine cap counts live pooled children: a stopped child keeps its slot until it is reaped, and no further idle child is evicted while one is exiting (`NFR-2`; `TestSlowExitingChildHoldsItsSlot`). Helper `claude -p`/initialize processes were outside any cap and slot I/O errors read as contention until 1.3.99.
  - A successful turn whose only output is `result.result` shows it as one assistant message in the turn's live view (cleared on the next turn, like the error message). This is synthetic fixture coverage only; whether `/usage` or `/cost` really emit no assistant line is still unmeasured, so no `/usage` defect is claimed.
  - Subagent sessions streamed in a turn get their own `session.idle` when the parent's turn settles, so the headless watcher acknowledges and the spool truncates their events too.
  - An out-of-range supervisor (AD-5) is drained behind a limited adapter, `provider.NewDraining`. Superseded by 1.3.99: as shipped here it had no event stream, so a later prompt in the draining turn never reached the dashboard and Allow-always bypassed the rules store; see *Verification 1.5 recovery (1.3.99)*. Sends and launches for that root fail fast with `ErrBusy`; the supervisor is replaced only after `/drain` reports idle.
  - Supervisors launched with another launch configuration are replaced the same way, with their regular adapter attached while they drain. The configuration is the resolved absolute `claude` path, the dashboard's `CLAUDE_CONFIG_DIR` value and the plugin directory; `ProviderRuntime.Identity` hashes it into the tmux session name (`claudecode-<root>-<identity>-<nonce>`), and `ocman provider` gained `--claude-cli` and `--claude-config-dir` (empty unsets the variable for children). Supervisors launched before this change have no identity segment and are drained and replaced once.
  - ocman's bound MCP address is not part of that identity: the dashboard writes it to `<data>/claude-launch.json` (`WriteLaunchConfig`) and a surviving supervisor reads it for each child it spawns. An idle child launched with an older address is respawned (`--resume`) on its next turn; a running turn keeps its child, whose MCP calls go to the old listener until the turn ends.
  - Fresh list reads overlay the statuses the project supervisors settled (`GET /sessions?live=1`, id, status and `resetsAt` only), and the provider's own overlay now includes `done`, so an aborted turn with an unfinished transcript reads done. The statuses live in the supervisor's memory: a replaced supervisor falls back to the transcript, as before.
  - The directory catalog is served by the browsing supervisor with ocman's plugin loaded. A command is a skill when a session's init listed it or when `SKILL.md` exists in ocman's plugin (`<plugin>:<skill>`), the Claude config directory or the project's `.claude/skills`. Agents come from initialize's `agents` (shape not captured; read as the Agent SDK's `{name, description, model}` or a bare name) and are offered as `mode: "subagent"` entries; there are no primary agents to select, since no measured CLI flag selects one.
  - Observed rate-limit windows are stored per Claude config directory in `claude-usage.json`, with the account email initialize last reported; another configuration never shows them, and a different login of the same configuration drops them. An older, unscoped usage file is ignored. A window observed between a re-login and the next account read is attributed to the new login.
  - The machine selector's local candidate uses the hub's saved default among its own enabled platforms (`Manager.localDefault`), and the single-host resolve path uses `defaultPlatformFor`. The new-conversation composer remounts on a platform change and drops model, agent and reasoning picks whenever machine or platform changes; a remembered project model is used only when the new catalog offers it. The shared draft is kept.
- **Why:** Verification 1.5 (comment #23) found each of these paths broken or unevidenced in the production composition.
- **Evidence:** `TestRulesPersistFailureIsVisible`, `TestAdapterRulesPersistFailure`, `TestOwnershipFailureIsVisible`, `TestSpoolFailureFailsTurnVisibly`, `TestSendRacingResultKeepsTurnOpen`, `TestAcceptedUnwrittenSendKeepsTurnOpen`, `TestSendRacingLimitResetKeepsTurnOpen`, `TestSlowExitingChildHoldsItsSlot`, `TestResultOnlyOutputIsShown`, `TestLiveSubagentStaysUnderTask` (child spool truncation), `TestDirectoryCatalogFromCLI`, `TestUsageWindowsScopedToConfiguration`, `TestDrainingAdapterUsesOnlyVersionIndependentRoutes`, `TestProviderIdentity`, `TestLocalTargetUsesEnabledPlatform`, the NewConversation platform/machine switch test, and in the root package `TestOutOfRangeSupervisorKeepsPendingPermissionAnswerable`, `TestProjectSupervisorFollowsDashboardConfiguration` (stale tmux environment, MCP rebind mid-turn, config change), `TestRestartedDashboardKeepsSettledStatus` and `TestDashboardKillRestartComposition` (SIGKILL of the whole ocman process mid-permission, restart on another MCP port, answer over HTTP). Each behaviour test was run against the unfixed code path and failed; `TestDrainingAdapterUsesOnlyVersionIndependentRoutes` and `TestProviderIdentity` cover new seams.
- **Follow-up:** none

### Fix check run (1.3.97)
- **Task:** claude-code-provider.1.3.97
- **Spec:** NFR-7 coverage ratchet; the 1.3.97 acceptance: lint, tests, builds, proto with no diff, docs, isolated Chromium E2E and coverage checks.
- **Environment:** macOS arm64; Go tests with `GOMAXPROCS=4 GOFLAGS=-p=2`. Temporary directories and the fake CLI only; the live ocman was never used. No thresholds, exclusions, skips or retries changed.
- **Results:** `make lint` pass (0 errors; ESLint warnings unchanged in kind); `go test ./...` pass; `go test -race` on `internal/claudeprovider`, `internal/platforms/provider`, `internal/providersupervisor`, `internal/hostsvc/local` and the root package's Claude tests pass; vitest 322 files / 3,210 tests pass; `vite build`, `go build -o /dev/null .`, `go mod tidy -diff` and the desktop build pass; `make proto` leaves `internal/remote/proto` unchanged; `make docs-build` pass; Chromium E2E 207/207 with 0 retries against `vite build` output on `vite preview` 127.0.0.1:18348 with `/api` and `/mcp` proxied to the closed port 1. Coverage against the gh-pages baseline (main `d87abba78`): Go 86.6 vs 86.7, **PASS** (-0.10, tolerance 0.1); frontend 86.27 vs 89.23, **FAIL** (-2.96). The branch's previous checkpoint measured 86.26 and 86.6, so neither suite dropped; the frontend gap is main's newer tests and is repeated at delivery after the main merge (gate 1.3.67).
- **Why:** Record of the required checks.
- **Follow-up:** Frontend official baseline at delivery (gate 1.3.67).

### EnsureRuntime returns the existing result struct; adapters plug in through RuntimeEnsurer; the RPC carries JSON
- **Task:** claude-code-provider.1.3.2 (recorded by the 1.3.61 re-audit, found by the 1.5 verification)
- **Spec:** AD-1, "`EnsureProjectOpencode` becomes `EnsureRuntime(ctx, platform, dir)` returning an endpoint whose port is optional. The gRPC RPC is renamed; the old name stays as an alias for one release"; Factory task 1.3.2, "Rename the gRPC EnsureProjectOpencode RPC to EnsureRuntime with a platform field".
- **What we did instead:** These exact shapes:
  - `hostsvc.Host.EnsureRuntime(ctx context.Context, platform, dir string) (*hostsvc.EnsureRuntimeResult, error)`, not `(Endpoint, error)`. `EnsureRuntimeResult` is the former `EnsureProjectOpencodeResult` renamed (`Endpoint string`, `RepoRoot string`, `Runtime ocruntime.Instance`, `Launched bool`); `EnsureProjectOpencodeResult` stays as a type alias for the OpenCode-only `RestartProjectOpencode`. The endpoint is a URL string (`http://127.0.0.1:<port>` for OpenCode, `unix://<socket>` for a provider supervisor); the optional port is `EnsureRuntimeResult.Port() string`, empty for a unix socket. There is no `Endpoint` type.
  - `Host.EnsureProjectOpencode` left the `Host` interface. The local host's `EnsureRuntime` (`internal/hostsvc/local/ensure_runtime.go`) refuses a platform not enabled on the machine, calls the adapter's runtime if it has one, and otherwise runs the old OpenCode path only for `opencode`.
  - Adapters plug in through a new optional interface, `hostsvc.RuntimeEnsurer` (`EnsureRuntime(context.Context, string) (*EnsureRuntimeResult, error)`, directory only), collected per platform id into `local.Deps.Runtimes` by `Server.newLocalHost` (`internal/server/host_wiring.go`). The Claude platform implements it (`claude_projects.go`).
  - The gRPC service keeps `EnsureProjectOpencode(JsonReq) returns (JsonResp)` unchanged and adds `EnsureRuntime(JsonReq) returns (JsonResp)`, whose JSON payload is `{"platform","projectDir"}`. There is no typed protobuf message or `platform` field. The hub falls back to `EnsureProjectOpencode` only when the remote answers `Unimplemented` and the platform is `opencode`; a pre-change remote cannot ensure another platform.
- **Why:** Every remote host RPC uses the `JsonReq`/`JsonResp` envelope, so a typed message would be the only one of its kind; callers already used `RepoRoot`, `Runtime` and `Launched`, which a bare endpoint would drop.
- **Follow-up:** Approved by the human at recovery gate claude-code-provider.1.3.107.

### Fork at a message is a separate capability
- **Task:** claude-code-provider.1.3.60
- **Spec:** FR-4 fork; FR-8 capability gating; `ForkSessionRequest.MessageID`.
- **What we did instead:** Added platform capability `forkAtMessage` (OpenCode true; a remote OpenCode is assumed to have it, like `revert`). Without it `/fork` offers only "Full session"; the Claude provider still answers 501 for a message id.
- **Why:** `fork` alone could not tell the UI that Claude forks only whole sessions (`--resume --fork-session`); the CLI has no measured fork-at-message flag.
- **Follow-up:** none

### Live subagent sessions are named from task_started
- **Task:** claude-code-provider.1.3.60
- **Spec:** FR-3 subagents as children of their Task call; AD-9 subagent → parent links.
- **What we did instead:** A live subagent's session id is `agent-<task_id>` from `system/task_started`, the same id the index derives from `subagents/agent-<id>.jsonl`. Forwarded lines that arrive before their `task_started` are not shown live (the transcript renders them once indexed). Child live views are dropped when the parent's next turn begins.
- **Why:** Forwarded child lines carry the parent's `session_id` and the spawning call id only (spike §5); the spike's `agent_id` equalled the `task_id`, and the stored layout names the file after the agent id. That equality is measured once, on 2.1.220.
- **Follow-up:** none

### The fake CLI validates every stdin line
- **Task:** claude-code-provider.1.3.60
- **Spec:** NFR-6 fake executable replaying recorded fixtures.
- **What we did instead:** Besides the existing `control_response` checks, the fake exits 3 on stdin that is not a JSON object, a user message without `role: "user"` content, a `control_request` without `request_id`/`request.subtype`, an `initialize` whose `sdkMcpServers` is not an array (the spike's measured rejection), or an unknown message type. An unknown control subtype is answered with the CLI's measured `Unsupported control request subtype` error (`TestFakeCLIRejectsMalformedInput`).
- **Why:** A malformed request must fail the replay instead of passing silently.
- **Follow-up:** none

### Claude-only workflow recovery adds capability flags
- **Task:** claude-code-provider.1.3.56
- **Spec:** FR-1, OpenCode-only features "are hidden through capabilities"; FR-8, revert "capability-gated"; AD-2's capability table.
- **What we did instead:** Three flags were added rather than reusing existing ones: platform capabilities `revert` and `runtimeRestart`, and host capability `opencode` ("the OpenCode platform is enabled here"). The hub sets `revert`/`runtimeRestart` for a remote OpenCode adapter, so remotes predating the flags keep `/undo`, `/redo` and `/restart-opencode`. `platforms.SessionLifecycle` gained `readOnly`, which the hub reads before queueing. The frontend reads only these flags.
- **Why:** Maintenance, the reviewer model and the MCP config belong to the machine, not to a session's platform; revert and runtime restart had no flag to gate on.
- **Follow-up:** none

### The setup panel blocked a Claude-only ocman on OpenCode's database
- **Task:** claude-code-provider.1.3.56 (found in its browser run; from `5c928e658`)
- **Spec:** FR-1, an OpenCode-less start does not require the OpenCode database.
- **What we did instead:** `/api/doctor` reported `opencode-db` as a required, failing check whenever OpenCode was disabled, so the web UI showed the blocking "ocman needs a few things before it can start" panel over every page of `-platforms claudecode`. API-level tests never saw it.
- **Why:** The check predates `-platforms`.
- **Follow-up:** Resolved by claude-code-provider.1.3.56: the check is listed only when OpenCode is enabled or failed to open; `TestClaudeOnlyStartup` asserts no required check fails.

### The projects index lists every other platform's sessions on refresh
- **Task:** claude-code-provider.1.3.56
- **Spec:** NFR-1, served from an index; FR-2, pickers offer Claude projects.
- **What we did instead:** Each projects-index refresh (session create, five-minute tick) lists every local non-OpenCode adapter's sessions and folds their directories, counts, tokens and cost into the OpenCode rows. A Claude project started in a terminal appears at the next tick.
- **Why:** The provider protocol has no projects operation; the Claude list is served from its index.
- **Follow-up:** none (`ponytail` note in `projects_index.go`: add a per-adapter projects operation if a list gets slow).

### PR/Issue launches use the active session's platform, without a selector
- **Task:** claude-code-provider.1.3.56
- **Spec:** FR-2, PR/Issue launch carries an explicit platform.
- **What we did instead:** The PRs & Issues pane sends the platform of the session it is shown for; the launch menu has no platform choice. The worktree and routine forms do offer one.
- **Why:** The pane always belongs to a session; launching beside it on that session's platform is the explicit context, and a selector in the split button would crowd it.
- **Follow-up:** none

### Existing Claude sessions report no live connection
- **Task:** claude-code-provider.1.3.17 (review; from `eb4a8b3ff`)
- **Spec:** FR-4, send, abort and prompts for sessions ocman launched; FR-8 composer row.
- **What we did instead:** Neither `internal/claudeprovider/scan.go`'s `entry.session()` nor the generic adapter sets `LiveConnection`, so a local Claude session's detail page keeps `portAvailable` false (`useSessionCapabilities.ts:100-103`) and disables the composer, `/undo`/`/redo` and the 3 s prompt refresh. Starting a conversation works because `/start` sends the first prompt server-side. Remote sessions force `LiveConnection` true (`internal/remote/platform.go:134,140`), which enables the composer on remote terminal-started sessions that are read-only.
- **Why:** Not covered by the implementing task's tests, which drive the API, not the session page.
- **Follow-up:** Resolved by claude-code-provider.1.3.56: the provider overlay sets `LiveConnection` for sessions ocman launched only; the hub forwards a remote provider's value (a remote OpenCode is still reported live) and a terminal session's lifecycle carries `readOnly`.

### The default platform falls back to OpenCode when OpenCode is disabled
- **Task:** claude-code-provider.1.3.17 (review; from `5c928e658`)
- **Spec:** FR-1, a create path never fails for lack of OpenCode; FR-2, "OpenCode stays the default when installed".
- **What we did instead:** `state.DefaultPlatform` returns `opencode` whenever the setting is empty, without checking it is enabled. With `-platforms claudecode` every create that omits `platform` (prepare/start, `POST /api/sessions`, MCP create, routines, Factory, plugin conversations, palette `/wt`, worktrees view, PR launch) resolves to `opencode` and fails with "unknown platform"; the setting is hidden with fewer than two platforms, so the user cannot fix it. The hub also resolves the default for remote targets without checking the remote advertises it (`create_platform.go:12-26`).
- **Why:** Not covered by the implementing task; the fallback predates the Claude adapter.
- **Follow-up:** Resolved by claude-code-provider.1.3.56: `state.DefaultPlatform` returns the saved choice only when enabled, else OpenCode when enabled, else the lowest enabled id (`SetEnabledPlatforms`, wired to the local registry). A remote target resolves against that remote's advertised adapters (`Server.defaultPlatformFor`, `routines.Service.defaultPlatform`).

### Claude-only ocman still probes OpenCode
- **Task:** claude-code-provider.1.3.17 (review; from `ab89e3fd6`, `5c928e658`)
- **Spec:** FR-1, an OpenCode-less start "must not run `opencode --version`"; AD-1, `ocv2.InstalledV2` runs only when OpenCode is enabled.
- **What we did instead:** Startup is clean, but the OpenCode judge backend's port discovery (`judge.go` → `opencode.DiscoverOpenCodePort` → `ocv2.InstalledV2` and `lsof`) runs for every judged Claude prompt, as do `GET /api/mcp/config` and the judge-model options. A running OpenCode found that way judges Claude prompts even though OpenCode is disabled. ocman also installs its skills into OpenCode's skill directory on every start (`main.go:154-162`).
- **Why:** These paths were not gated on the enabled platforms.
- **Follow-up:** Resolved by claude-code-provider.1.3.56: without OpenCode the judge has no OpenCode port discovery (`Deps.OpenCodeDisabled`), judge-model options and the MCP config status/install skip OpenCode, and the embedded skills are installed only with OpenCode enabled. `TestClaudeOnlyStartup` asserts opencode never runs through these paths.

### OpenCode-only features are shown, not hidden, without OpenCode
- **Task:** claude-code-provider.1.3.17 (review; from `5c928e658`)
- **Spec:** FR-1, OpenCode-only features "are hidden through capabilities, not errors"; FR-8, maintenance and v2 views OpenCode only.
- **What we did instead:** The Maintenance settings group stays visible with a "needs the OpenCode platform" notice; the MCP-config toast still offers to write OpenCode's config; `/restart-opencode` stays in the slash menu and answers 404; the judge-model setting is shown although the Claude judge always uses haiku. `docs/features/claude-code.md` says these are hidden.
- **Why:** Only the doctor check, host `opencode` lookup and "Launch opencode" action were moved behind capabilities.
- **Follow-up:** Resolved by claude-code-provider.1.3.56: host capability `opencode` hides Settings → Maintenance, the reviewer model row and the MCP-config toast; platform capability `runtimeRestart` hides `/restart-opencode`.

### Project pickers list only OpenCode projects
- **Task:** claude-code-provider.1.3.17 (review; from `5a44bee09`)
- **Spec:** FR-2 / FR-8, routines and plugin conversations support Claude.
- **What we did instead:** The projects index reads only OpenCode's database (`projects_index.go`, `handlers_system.go`), so the routine form and the plugin `project` setting cannot pick a directory that has only Claude sessions, and list nothing with `-platforms claudecode`.
- **Why:** Not covered by the implementing task; its tests use the API with a directory.
- **Follow-up:** Resolved by claude-code-provider.1.3.56: the projects index adds every other local platform's top-level session directories (`mergePlatformProjects`), and host capability `projects` is true without OpenCode.

### Plugin settings catalog is OpenCode-only and the platform is free text
- **Task:** claude-code-provider.1.3.17 (review; from `352c7307e`)
- **Spec:** AD-13, "platform setting per installation next to the required `project`".
- **What we did instead:** The plugin settings agent/model catalog always ensures and reads OpenCode (`plugin_management.go:67-71`), whatever the installation's `platform`, and fails without OpenCode. The Slack example declares `platform` as a free-text string, validated only when a message arrives.
- **Why:** The catalog path predates per-installation platforms; settings types are plugin-declared.
- **Follow-up:** Resolved by claude-code-provider.1.3.56: the project catalog takes the installation's `platform` (default platform when empty) and reads that adapter's `DirectoryCatalog`; OpenCode is ensured only when it is the platform. The Slack settings form renders `platform` as a select of the owner's platforms. The plugin still declares it as a string setting, validated when a message arrives.

### MCP `sessions create` and Factory `create` take an optional platform
- **Task:** claude-code-provider.1.3.17 (review; from `5a44bee09`, `352c7307e`)
- **Spec:** FR-2, MCP `sessions create` and Factory "carry an explicit platform".
- **What we did instead:** `platform` is optional on both; when omitted the default-platform setting applies, also when `sessions create` is called with a parent `session_id` on another platform.
- **Why:** Kept the tools' existing call shape; agents rarely know platform ids.
- **Follow-up:** claude-code-provider.1.3.56: `sessions create` with a calling `session_id` and no `platform` now uses the calling session's platform; only a create with neither uses the default, which is always an enabled platform.

### Revert and unrevert are not capability-gated
- **Task:** claude-code-provider.1.3.17 (review; from `eb4a8b3ff`)
- **Spec:** FR-8, revert/unrevert "best effort, capability-gated".
- **What we did instead:** The provider adapter does not implement `SessionReverter` and there is no revert capability flag, so `/undo` and `/redo` stay listed for Claude and their 501 is only logged. Session changes are gated (`fileChanges: false`).
- **Why:** No revert capability existed to gate on; the task did not add one.
- **Follow-up:** Resolved by claude-code-provider.1.3.56: platform capability `revert` (true for OpenCode) hides `/undo` and `/redo`, and running them without it fails visibly instead of calling the API.

### Live subagent output is merged into the parent session
- **Task:** claude-code-provider.1.3.17 (review; from `eb4a8b3ff`)
- **Spec:** FR-3, subagent transcripts as children of their Task call; AD-6 `--forward-subagent-text`.
- **What we did instead:** `internal/claudeprovider/pool.go` feeds every forwarded `assistant`/`user` line to the parent's converter without checking `parent_tool_use_id`, so during a live turn the subagent's messages appear in the parent and the Task part stays running until the transcript is re-read. Stored transcripts render correctly.
- **Why:** No test asserts live messages for the subagent fixture.
- **Follow-up:** Resolved by claude-code-provider.1.3.60: lines with `parent_tool_use_id` go to a per-call child converter for session `agent-<task_id>` (linked by `system/task_started`), and the parent's Task part gets `metadata.sessionId`; `TestLiveSubagentStaysUnderTask` replays `subagent.jsonl`. See "Live subagent sessions are named from task_started".

### Claude list rows carry no model
- **Task:** claude-code-provider.1.3.17 (review; from `7ac509a7b`)
- **Spec:** FR-3, sessions are listed with model.
- **What we did instead:** The index stores the model but the list row (`db.Session`) has no model field; it shows in the detail view and per message. `docs/features/claude-code.md` says the list shows it.
- **Why:** `db.Session` never carried a model for OpenCode either.
- **Follow-up:** none

### The transcript parser was rewritten, not salvaged
- **Task:** claude-code-provider.1.3.17 (review; from `7ac509a7b`)
- **Spec:** AD-9, "Salvage the parser and scanner from `0fe96f0a1^:internal/platforms/claudecode/`".
- **What we did instead:** `internal/claudeprovider/scan.go`, `transcript.go` and `convert.go` are new code; only conventions carried over (120-char titles, `<local-command-` skip, tool-result flattening).
- **Why:** The single Claude→v1 converter (AD-2) and byte-offset paging needed a different shape than the old hook-era parser.
- **Follow-up:** none

### Launched sessions get the hook plugin and an unverified MCP address
- **Task:** claude-code-provider.1.3.17 (review; from `1eb87aba0`, `eb4a8b3ff`)
- **Spec:** AD-11, `--plugin-dir` carries "ocman skills (and MCP config)"; hooks are opt-in; hooks never fail Claude.
- **What we did instead:** The `--plugin-dir` copy includes `hooks.json` whenever `-mcp-addr` is set, so every launched session runs `ocman hook` per tool call and the server ignores it. The MCP server is passed as inline `--mcp-config`, built from the raw `-mcp-addr` flag rather than the bound listener (a disabled listener or `:0` yields a dead address). The installed hook hard-codes the ocman executable path, so moving the binary makes the hook exit non-zero (documented in `claude-code.md`). Standalone `ocman provider claude` passes neither MCP config nor plugin dir.
- **Why:** One plugin directory serves both uses (AD-11); the address is known before the listener starts.
- **Follow-up:** Partly resolved by claude-code-provider.1.3.54: launched sessions now run in project supervisors started with `--mcp-addr <bound listener>` (passed by `Server.startMCPListener` through `SetMCPAddr`, never the `:0` or a failed `-mcp-addr`) and `--plugin-dir`, so `ocman provider claudecode` configures MCP and skills. A supervisor launched before the listener binds has no MCP config until it is replaced. Open: hooks are still copied into the `--plugin-dir` plugin.

### Waiting on a prompt is a flag, not a status, for driven sessions
- **Task:** claude-code-provider.1.3.17 (review; from `ab475bc1a`)
- **Spec:** AD-10, status from the process "(turn open, waiting on prompt, rate-limited, exited)".
- **What we did instead:** A driven session stays `busy` with `pendingPermission`/`pendingQuestion` set, as OpenCode sessions do; a hooked terminal session's `Notification` reads `waiting`.
- **Why:** Matches the OpenCode adapter, which the frontend already handles.
- **Follow-up:** none

### `limited` never clears and does not survive a restart
- **Task:** claude-code-provider.1.3.17 (review; from `80e23f1dd`, `eb4a8b3ff`)
- **Spec:** FR-7, limited "with the reset time"; queue and routines treat it as not idle.
- **What we did instead:** Nothing acts on `resetsAt`: the status holds until the next send, so held queue messages and routine runs wait indefinitely, and `/drain` never sees the session idle. The status is in memory only; after a restart it is inferred from the transcript, which can read finished.
- **Why:** Not covered by the implementing tasks; the fixture is synthetic.
- **Follow-up:** Resolved by claude-code-provider.1.3.58: a turn whose result follows a rejection stays `limited` (no idle edge, no acknowledgment, `/drain` not idle) until `resetsAt`, then settles as `error` and publishes the idle edge so held messages drain. The supervisor outlives ocman, so the unacknowledged turn replays as `limited` to a restarted ocman (`TestUsageLimitHoldsUntilReset`). A replaced or crashed supervisor still loses the reset timer, and a send that lands in the instant the timer fires can see one stray idle edge. A missing `resetsAt` settles at once.

### Each stream-json result reads as an idle edge
- **Task:** claude-code-provider.1.3.17 (review; from `eb4a8b3ff`)
- **Spec:** FR-4 hold-for-next-turn; spike §3, held input stays queued "until the actual idle edge".
- **What we did instead:** Every `result` line ends the turn (`busy=false`, idle timer, `session.idle`). Input sent while the model is streaming produces its own later result, so between the two the session reads done, the queue may flush a held message, and the child is evictable at the pool cap.
- **Why:** The implementation assumes one result per turn (`ponytail` note in `internal/claudeprovider/pool.go`).
- **Follow-up:** Resolved by claude-code-provider.1.3.58: input written while a turn is open is pending until the CLI echoes its `uuid` (`--replay-user-messages`); a `result` is the idle edge only when nothing is pending, so the child stays busy (never evicted or idled out) and the queue holds. Inputs reach the child in send order. Departure: the spike did not record when the echo is emitted for a queued input; the code assumes it is emitted when the CLI starts the input (consistent with both measured orderings, `TestMidTurnInputKeepsTurnOpen` uses synthetic fixtures). A mid-turn input the CLI never echoes (unmeasured for local slash commands) would hold the turn open until abort or exit. An abort drops pending inputs without reading the interrupt's `still_queued` reply.

### A failed turn's `error` status is held by the supervisor only
- **Task:** claude-code-provider.1.3.58
- **Spec:** AD-10, status from the process; FR-4 routines and queue on Claude sessions.
- **What we did instead:** A `result` with `is_error` or a non-`success` subtype, a `claude` exit mid-turn and a launch failure publish a visible error message, `session.status` `error` and then the idle edge; the idle edge keeps `error` in the provider and the adapter, and the provider overlays it on list, detail and lifecycle reads, so a routine records a failure. A result whose `terminal_reason` starts with `aborted`, or that follows ocman's interrupt, is not a failure. The status lives in the supervisor's memory until the next turn: a replaced supervisor falls back to the transcript, which records no CLI-side failure.
- **Why:** The CLI writes no failure record ocman can read back; the supervisor outlives ocman restarts.
- **Follow-up:** Corrected by claude-code-provider.1.3.97: the dashboard's list did not overlay it (only busy, waiting and limited, without `resetsAt`); it now reads the project supervisors' settled statuses, and the provider overlay includes `done`. See "Verification 1.5 blockers fixed".

### Read-only terminal sessions and abort rely on server errors
- **Task:** claude-code-provider.1.3.17 (review; from `eb4a8b3ff`)
- **Spec:** FR-4, terminal sessions are read-only; abort.
- **What we did instead:** Capabilities are platform-wide, so a terminal session shows a composer whose sends fail with 403; Ctrl/Cmd+Enter queues a message whose flush then fails. Abort on a terminal session returns 204, and an abort while a turn waits for a pool slot is a no-op after which the turn starts.
- **Why:** There is no per-session read-only flag.
- **Follow-up:** Partly resolved by claude-code-provider.1.3.56: terminal sessions now report no live connection, so the composer is disabled, and a queued follow-up for one is refused (422, lifecycle `readOnly`) locally and from a hub. Resolved by claude-code-provider.1.3.58: an abort cancels a turn waiting for a pool slot, which settles without an error and never starts. Abort on a terminal session still returns 204.

### Hold-for-next-turn uses ocman's queue, not the provider operation
- **Task:** claude-code-provider.1.3.17 (review; from `eb4a8b3ff`)
- **Spec:** AD-2 Turns row, "hold-for-next-turn".
- **What we did instead:** The Claude backend does not advertise `NativeQueue` and answers `/hold` with 501; Ctrl/Cmd+Enter uses ocman's durable queue in `state.db`, drained on the idle edge.
- **Why:** stream-json has no queue; ocman's queue already gives the semantics.
- **Follow-up:** none

### Supervisor spool and previous protocol version are nominal
- **Task:** claude-code-provider.1.3.17 (review; from `7efcacec4`, `9bbe25492`)
- **Spec:** AD-4 replay from the spool; AD-5, "ocman speaks the current and previous versions".
- **What we did instead:** `providersupervisor.New` does not reload existing spool files and restarts sequences at 0, so a restarted supervisor cannot replay. `PreviousVersion` was 0, a version that never existed, with no per-version behaviour (1.3.108 made it 1, whose raw prompt patterns the adapter escapes; since 1.3.120 it equals the current version 3, so versions 1 and 2 drain). A `/hello` without `protocolVersion` (or without `cliVersion`) is rejected by the adapter (`internal/platforms/provider/adapter.go`, `TestHandshakeRequiresExplicitVersionAndCLI`); the 1.3.17 review wrongly said it was accepted, corrected by the 1.3.61 re-audit. Drain-then-replace was reachable only through `ProviderManager`, which Claude did not use until 1.3.54.
- **Why:** Only one protocol version exists, and the supervisor is not restarted independently of ocman today.
- **Follow-up:** Partly resolved by claude-code-provider.1.3.54: Claude's project supervisors run in tmux through `ProviderManager`, so drain-then-replace and replay across ocman restarts now apply to Claude. The spool is still not reloaded when the supervisor process itself restarts (reboot, tmux server killed); see "Claude project supervisors outlive ocman, not the machine". `PreviousVersion` stayed nominal until 1.3.108 (version 1: raw patterns escaped by the adapter); since 1.3.120 only version 3 is reused (AD-5's "current and previous versions" is not kept: older supervisors drain). Unresolved and deferred at delivery (graph revision 32): `delivery-report.md` FU-4 (R4) and FU-10.

### The Claude CLI version is read once at startup
- **Task:** claude-code-provider.1.3.17 (review; from `eb4a8b3ff`)
- **Spec:** NFR-5 / AD-5, outside the pinned range ocman shows an explicit error.
- **What we did instead:** The version is read when a supervisor starts; a CLI auto-update into or out of the accepted versions is noticed only when that supervisor is replaced. Since 1.3.54 the browsing supervisor restarts with ocman, but a project supervisor survives ocman restarts and keeps its version until it is relaunched (reboot, killed tmux server, version-skew replacement). A missing CLI reports `cliVersion: "unavailable"` in `/hello`, which is accepted; driving then fails with the explicit "claude CLI not found" error.
- **Why:** Avoids a `claude --version` per operation.
- **Follow-up:** none

### The Claude process cap is not configurable
- **Task:** claude-code-provider.1.3.17 (review; from `eb4a8b3ff`)
- **Spec:** NFR-2, numbers from the spike; spike §1, "Keep this cap configurable".
- **What we did instead:** Cap 4 and the 600 s idle timeout are `DefaultOptions` values; no flag or setting reaches `MaxProcs`.
- **Why:** Not needed for the first release.
- **Follow-up:** none

### Permission refresh bypasses ocman's rules and is polled
- **Task:** claude-code-provider.1.3.17 (review; from `ab475bc1a`)
- **Spec:** AD-7a, "No component polls" and `RefreshPermissions` "used only for reconciliation"; rule-decided prompts never reach the judge or a browser.
- **What we did instead:** `internal/platforms/provider/prompts.go`'s `RefreshPermissions` copies every supervisor-pending prompt into the cache, including ones rules already decided whose reply is in flight; callers then hand them to the judge or the browser, and the later `permission.replied` does not cancel that work. The session page polls the refresh every 3 s while a prompt is pending (pre-existing, all platforms). The provider runtime's drain and readiness loops also poll, off the prompt path. `permission.replied` is emitted even when writing the reply to the child failed.
- **Why:** The refresh and its poll predate the provider; the rules short-cut was added only on the event path.
- **Follow-up:** Resolved by claude-code-provider.1.3.57, see "Claude prompts are push-only; rule replies retry".

### Cancelled questions are rejected without a reason
- **Task:** claude-code-provider.1.3.17 (review; from `ab475bc1a`)
- **Spec:** AD-7a, an interrupt cancels pending prompts with `permission.replied` and a cancelled reason.
- **What we did instead:** Cancelled permissions emit `permission.replied` with `reason: "cancelled"`; cancelled questions emit `question.rejected` without a reason.
- **Why:** `question.rejected` is the v1 event that clears a question in the frontend.
- **Follow-up:** none

### Claude subagent prompts are attributed by the provider
- **Task:** claude-code-provider.1.3.17 (review; from `ab475bc1a`)
- **Spec:** AD-7, prompts from Claude subagents bubble up "through the same parent resolution".
- **What we did instead:** A subagent's `can_use_tool` arrives on the top-level `claude` process, so the provider attributes the prompt to that session directly and records `metadata.agentID`; `GetSessionParentIDs` is not involved.
- **Why:** One process serves the session and its subagents; the outcome is the same.
- **Follow-up:** none

### Some Claude "Allow always" approvals are not inherited
- **Task:** claude-code-provider.1.3.17 (review; from `ab475bc1a`)
- **Spec:** FR-5, "Allow always" inheritance (#101) with OpenCode semantics.
- **What we did instead:** Tools the provider maps to a lowercased name (`mcp__…`, `exitplanmode`, `killshell`) produce permission keys outside `permissions/inherit.go`'s known keys, so worktree children drop those approvals. OpenCode MCP keys behave the same.
- **Why:** The inheritance key list predates Claude.
- **Follow-up:** Partly resolved by claude-code-provider.1.3.57: `mcp__<server>__<tool>` keys (lowercase letters, digits, `_`, `-`) are inherited. `exitplanmode`, `killshell` and other unmapped Claude tools, and OpenCode MCP keys (`<server>_<tool>`, indistinguishable from arbitrary text), are still dropped.

### The judge sees no recent user intent for Claude sessions
- **Task:** claude-code-provider.1.3.17 (review; from `ab475bc1a`)
- **Spec:** FR-5 / AD-8, the judge works for Claude with the same semantics.
- **What we did instead:** `resolveJudgeSections` reads recent messages through OpenCode's API, which has none for a Claude session, so Claude prompts are judged without the "Recent user intent" section.
- **Why:** The section reader was not moved behind the platform.
- **Follow-up:** Resolved by claude-code-provider.1.3.57: when OpenCode has no messages for the session, the judge reads the latest user texts through the session's platform adapter (`Platform.Session`, the newest 24 messages, at most 6 texts).

### Claude auto-approve is not advertised as a capability
- **Task:** claude-code-provider.1.3.17 (review; from `ab475bc1a`)
- **Spec:** FR-5, auto-approve applies to Claude sessions with the same semantics.
- **What we did instead:** The server judges Claude prompts, but the provider capabilities omit `autoApprove`, so the session page hides the auto-approve toggle and the "Checking…" state.
- **Why:** Not covered by the implementing task.
- **Follow-up:** Resolved by claude-code-provider.1.3.56: the Claude provider advertises `autoApprove`.

### Claude analytics omit tool timings and limit utilisation
- **Task:** claude-code-provider.1.3.17 (review; from `afab6599d`)
- **Spec:** AD-12, existing analytics SQL runs unchanged; FR-8, analytics show limit utilisation.
- **What we did instead:** The mirror gets Claude sessions and messages but no `tool_timing` rows, and Claude rows are absent whenever analytics read `opencode.db` directly (before the first mirror build or after a failed sync). Utilisation is shown in Settings → Provider accounts, not in the analytics views.
- **Why:** Tool timings come from OpenCode parts; utilisation is account state, not per-session data.
- **Follow-up:** Resolved by claude-code-provider.1.3.59; see "Analytics run from a platform-neutral store; the mirror is never bypassed once another platform contributes".

### Account utilisation reads the CLI's OAuth credentials
- **Task:** claude-code-provider.1.3.17 (review; from `afab6599d`)
- **Spec:** requirements Overview, "Ocman never reads, stores or proxies Claude credentials".
- **What we did instead:** `internal/claudeprovider/account.go` reads the access token from `.credentials.json` or the macOS keychain and sends it to `api.anthropic.com/api/oauth/usage`. It is not stored or proxied elsewhere.
- **Why:** The only structured source for FR-7's utilisation (1.3.14 used Anthropic's OAuth usage endpoint; that entry was replaced by "Claude account utilisation comes from observed CLI rate-limit events").
- **Follow-up:** Resolved by claude-code-provider.1.3.55: credential and keychain reads and the OAuth usage call are removed; see "Claude account utilisation comes from observed CLI rate-limit events".

### No real-CLI smoke test and no Claude restart test
- **Task:** claude-code-provider.1.3.17 (review; from `7ac509a7b`, `eb4a8b3ff`)
- **Spec:** NFR-6, "one optional manual smoke test uses the real CLI"; Testing, restart test killing the ocman side mid-turn and mid-prompt.
- **What we did instead:** No smoke test, target or procedure exists. The restart test runs against the fake provider only (`internal/platforms/provider/replay_test.go`); for Claude there is a watcher disconnect/reconnect test (`internal/claudeprovider/prompts_test.go`), since the in-process supervisor ends with ocman.
- **Why:** Restart survival is not implemented for Claude (see the in-process supervisor entry).
- **Follow-up:** Restart test resolved by claude-code-provider.1.3.54: `TestClaudeSupervisorSurvivesOcmanRestart` (`claude_projects_test.go`) stops ocman's side mid-permission and mid-turn against the fake CLI and a real `ocman provider claudecode` process. Open: the real-CLI smoke test.

### README documents the wrong platform id
- **Task:** claude-code-provider.1.3.17 (review; from `544a0084e`)
- **Spec:** FR-1, `-platforms` accepts `claudecode`.
- **What we did instead:** `README.md:90` shows `-platforms opencode,claude-code`, which makes ocman exit with "Unknown platform". `docs/configuration/_index.md` is correct.
- **Why:** Missed in the docs task.
- **Follow-up:** Resolved by claude-code-provider.1.3.61: `README.md` shows `-platforms opencode,claudecode` and `-platforms claudecode`, and links the Claude Code guide.

### Rename and first-turn restarts can race the CLI (unverified)
- **Task:** claude-code-provider.1.3.17 (review; from `eb4a8b3ff`)
- **Spec:** FR-4 rename; AD-6 `--session-id` on create, `--resume` after.
- **What we did instead:** Rename appends a `custom-title` line without checking whether a child is writing the transcript. `Started` is set only on a `result`, so a child that dies during its first turn is respawned with `--session-id` for a transcript that already exists. Neither was tried against the real CLI.
- **Why:** Not covered by fixtures.
- **Follow-up:** Resolved against fake-protocol fixtures by claude-code-provider.1.3.60: `Started` is set at the CLI's replay echo of the input (`TestFirstTurnCrashResumes`), a rename during a turn is held (`owned.titlePending`) and appended after the turn, and ocman's own title is shown meanwhile (`TestRenameDuringTurnWaitsForIdle`). Still unmeasured against the real CLI, and escalated to the optional manual smoke test: whether 2.1.220 has written the transcript by the time it echoes the input, and whether it keeps an appended `custom-title` across its own later writes. A send landing between the busy check and the append can still interleave two single-line `O_APPEND` writes.

### Hook events reach the provider over a new protocol route
- **Task:** claude-code-provider.1.3.9
- **Spec:** AD-11, hooks "posting to the loopback listener"; AD-2's operation list.
- **What we did instead:** `ocman hook <event>` posts `{event, sessionID}` to `POST /hook` on the dedicated MCP listener only (not the main port, whose auth a hook cannot satisfy). The server forwards it to the platform through a new provider-protocol operation, `POST /hooks` (`HookEvent`, gated by the `hooks` capability). Busy comes from `PreToolUse`/`PostToolUse`, `Notification` reads as waiting, and `SessionStart`/`Stop` settle the session; a busy report lapses after the transcript's live window because no hook marks a killed `claude`.
- **Why:** The provider keeps the live view and the event stream; the protocol had no inbound route for sessions it does not drive. Without `UserPromptSubmit` or `SessionEnd` in AD-11's hook list, a turn shows busy from its first tool call and a killed CLI needs a timeout.
- **Follow-up:** none

### Installed plugin state is read from settings.json
- **Task:** claude-code-provider.1.3.9
- **Spec:** FR-6, install and removal "through `claude plugin install`"; AD-11 spike fallback.
- **What we did instead:** Install and uninstall go through the CLI (`plugin marketplace add`, `plugin install --scope user`, `plugin uninstall`, `plugin marketplace remove`); no settings fallback was needed. Whether it is installed is read (never written) from `enabledPlugins["ocman@ocman"]` in the Claude config's `settings.json`, instead of running `claude plugin list` on every Settings load. The one plugin directory now lives in a local marketplace, `<data>/claude-marketplace/ocman`, and is still what launched sessions get with `--plugin-dir`.
- **Why:** Spike §7 showed the CLI route works; a CLI call per status read is slow. The marketplace layout is what `plugin install` requires.
- **Follow-up:** none

### Claude analytics require the OpenCode analytics mirror
- **Task:** claude-code-provider.1.3.14
- **Spec:** AD-12, "An ingester fills `analytics-cache.db`'s OpenCode-shaped tables from the index"; FR-1, OpenCode is optional.
- **What we did instead:** `claude-index.db` gained a `usage` table filled by the single converter, and `db.AddAnalyticsSource` copies it into the mirror on every sync. The mirror exists only when the OpenCode database is open, so with `-platforms claudecode` alone the analytics views stay unavailable, as they were.
- **Why:** The analytics handlers and `db.DB` are built around the OpenCode database; opening an empty stand-in would make every other `s.db` path believe OpenCode is present.
- **Follow-up:** Resolved by claude-code-provider.1.3.59; see "Analytics run from a platform-neutral store; the mirror is never bypassed once another platform contributes".

### Analytics run from a platform-neutral store; the mirror is never bypassed once another platform contributes
- **Task:** claude-code-provider.1.3.59 (recovery brief 7, claude-code-provider.1.5 comment #19)
- **Spec:** AD-12, "An ingester fills `analytics-cache.db`'s OpenCode-shaped tables from the index, so the existing analytics SQL runs unchanged. Subscription sessions show tokens, API-equivalent cost (labelled) and limit utilisation"; FR-1, no OpenCode database required; FR-8 analytics row.
- **What we did instead:** Implemented as specified, with these exact departures:
  - Without OpenCode, `analytics-cache.db` itself is the analytics database (`db.OpenAnalyticsStore`): the same schema and SQL, filled only by `AddAnalyticsSource`. The server reads the usage views from `Server.analytics` (`WithAnalytics`), which is OpenCode's `db.DB` when OpenCode is enabled; `s.db` stays nil, so no OpenCode path believes OpenCode is present. The store is keyed `platform-sources` instead of an OpenCode path, so switching between Claude-only and mixed starts wipes and rebuilds the file.
  - Once any platform contributes rows, analytics never read `opencode.db` alone: the first build is awaited by the first read instead of falling back (on a large OpenCode database the first-ever analytics load waits for the ~14 s build), a failed full rebuild or incremental sync serves the last copy (possibly stale, never missing a platform), and the session-table reads (session/project counts, daily and hourly sessions, session titles and directories) go to the mirror. In that mode `GetStats`' token and cost totals use the existing per-message aggregate instead of OpenCode's denormalised session columns. An OpenCode-only ocman keeps the previous fallbacks.
  - `db.AnalyticsSource` gained a `changedSince` argument and `AnalyticsMessage` a `ToolTimes` field. `claude-index.db` (schema 3, wiped once) stores per usage row the indexing time and the tool parts' `state.time` JSON; the source hands over rows created in the incremental window or re-indexed since the previous sync (less 10 minutes' slack), so transcripts indexed after a sync (first start, terminal sessions run while ocman was down) reach the mirror without waiting for the 6-hourly rebuild.
  - Claude tool timings come from the converter's tool parts, so Claude tool-calls turns get a throughput sample; a tool without a result has an unknown timing and suppresses its sample, as for OpenCode.
  - Limit utilisation is account-level, not per session: Analytics → Models & Cost shows a "Subscription limits" card with the same provider-account data as Settings → Provider accounts (subscription type from the CLI's initialize, utilisation from observed rate-limit events; see "Claude account utilisation comes from observed CLI rate-limit events"), hidden when no enabled platform reports an account. Nothing reads Claude credentials (`TestAccountNeverReadsCredentials`).
  - If the mirror cannot be opened in a mixed start, sources are ignored and analytics read OpenCode only (logged, as before). The OpenTelemetry stats gauges and LLM metrics still read OpenCode only.
- **Evidence:** Failing first, then passing: `TestAnalyticsStoreWithoutOpenCode` and `TestMixedAnalyticsNeverReadOpenCodeAlone` (`internal/db/analytics_store_test.go`, before/after first build, failed rebuild, failed sync, restart) failed with the previous read paths (0 sessions Claude-only; OpenCode-only totals 107 vs 1107 mixed); `TestClaudeOnlyStartup` failed with `/api/stats = 501` before `main.go` opened the store, and now matches the fixture transcripts' tokens through `/api/stats` and `/api/metrics`; `TestUsageViewsCountClaudeTranscriptTokens` (mixed and Claude-only, API-equivalent pricing, tool-calls throughput) failed without the tool timings. Checks: `make lint`, `go test . ./internal/...`, Chromium `dashboard.spec.ts`/`auth.spec.ts`; coverage against the branch tip before this change (Go 86.6 → 86.6, frontend 86.25 → 86.26). The official gh-pages frontend baseline gap recorded under 1.3.53 is unchanged. `internal/db/stats_dashboard.go` and `internal/server/handlers_system.go` were already over 400 lines and got one-line read-path changes; they were not split.
- **Why:** A separate store keeps `s.db` meaning "OpenCode is present"; falling back to OpenCode after a source registers would drop that platform's rows, so correctness wins over first-load latency. A time window alone cannot see rows a platform learns about late.
- **Follow-up:** none

### Claude cost is labelled API-equivalent wherever the platform reported $0
- **Task:** claude-code-provider.1.3.14
- **Spec:** AD-12, subscription sessions show "API-equivalent cost (labelled)".
- **What we did instead:** Claude messages keep cost 0, so analytics' existing effective-cost fallback prices their tokens through `internal/pricing`; the UI labels any $0-reported figure (Claude or an OpenCode subscription) API-equivalent. The Claude composer still shows no per-session estimate because the provider list carries no estimated cost.
- **Why:** The fallback already is the API-equivalent figure; a Claude-only label would branch on platform, which the frontend forbids.
- **Follow-up:** none

### Claude account utilisation comes from observed CLI rate-limit events
- **Task:** claude-code-provider.1.3.55 (replaces 1.3.14's OAuth usage endpoint); approved FR-7 departure, recovery gate claude-code-provider.1.3.68, choice A.
- **Spec:** AD-2's account operation; FR-7 "Settings shows the Claude account's subscription type and current 5-hour and weekly utilisation"; requirements Overview, "Ocman never reads, stores or proxies Claude credentials".
- **What we did instead:** The provider's `GET /account` runs the user's `claude` for a control-protocol `initialize` only (no user message) and reports `account.subscriptionType`, cached for 10 minutes; no account object is 404, a failed CLI call is an error. Utilisation comes only from structured `rate_limit_event`s on sessions ocman drives: `five_hour` and `seven_day` (as `weekly`) with a `utilization` fraction, or a `rejected` status (recorded as 100%), are written to `<data>/claude-usage.json` (flock, atomic rename) so every project supervisor shares them; the latest per window is shown until its reset passes. A window not reported shows "not reported yet" in Settings. Nothing reads `.credentials.json` or the keychain; `TestAccountNeverReadsCredentials` enforces it.
- **Why:** Spike §5: initialize has no utilisation, account-info control requests are unsupported and `/usage` is localized text, so structured 5-hour and weekly figures exist only in rate-limit events, which the CLI emits unsolicited (measured only as a weekly `allowed_warning`). Either window may therefore be missing; the user approved this over parsing `/usage` or keeping the credential read.
- **Follow-up:** none

### Factory platform is set when the Epic is created
- **Task:** claude-code-provider.1.3.13
- **Spec:** AD-13, "Factory: platform per Epic, next to the implementation model."
- **What we did instead:** The Epic's platform is the `platform` argument of the `factory` MCP `create` action (default: the default-platform setting) and is frozen into every Attempt. The implementation model is still picked at plan approval, where no platform selector was added.
- **Why:** Planning already runs on the Epic's platform, and the approval model picker lists that platform's models, so changing platform at approval would contradict the planning session. The Factory UI has no Epic creation form.
- **Follow-up:** none

### Plugin conversation platform is a plugin-declared setting
- **Task:** claude-code-provider.1.3.13
- **Spec:** AD-13, "Plugin conversations: platform setting per installation next to the required `project`."
- **What we did instead:** The host reads an optional `platform` setting value; a plugin exposes it by declaring a `platform` setting (the Slack example does). Plugins that do not declare it use the default platform.
- **Why:** Settings are declared by the plugin description; the host does not inject settings.
- **Follow-up:** See "Plugin settings catalog is OpenCode-only and the platform is free text".

### Factory sessions on Claude also allow `mcp__ocman__factory`
- **Task:** claude-code-provider.1.3.13
- **Spec:** AD-13, Factory sessions run on the Epic's platform with the ocman MCP server.
- **What we did instead:** On a non-OpenCode platform, Factory planning and implementation rulesets add `mcp__ocman__<tool>` (pattern `*`) with the same action as the existing `mcp_<tool>` rule.
- **Why:** Claude asks permission for every MCP call under that name, so without the rule each `complete_attempt` would wait on the judge or the user.
- **Follow-up:** none

### /wt and continued routines take the parent session's platform
- **Task:** claude-code-provider.1.3.12
- **Spec:** FR-2, `/wt`, worktree view, PR/Issue launch and routines carry an explicit platform.
- **What we did instead:** The browser still omits `platform` on these requests. When it is omitted, `/wt` from a session uses the parent session's platform and a routine continuing an existing session uses that session's platform; the command palette's `/wt`, the worktrees view, PR/Issue launch and fresh/reuse routines use the default-platform setting. An explicit `platform` in the request still wins.
- **Why:** The parent or target session already fixes the platform, and the default would put a Claude session's worktree child or continuation on OpenCode, with permission inheritance read from the wrong adapter. No platform selectors were added to those forms.
- **Follow-up:** Resolved by claude-code-provider.1.3.56: the worktree form (palette `/wt` and the worktrees view, without a parent session) and the routine form offer a platform select when the machine runs more than one, and send it; PR/Issue launches send the active session's platform; MCP `create` with a `session_id` uses that session's platform.

### Claude worktree naming follows the session's platform
- **Task:** claude-code-provider.1.3.12
- **Spec:** "Worktree naming without OpenCode uses a short `claude -p` haiku call" (Factory task brief).
- **What we did instead:** An automatic worktree is named by its session's platform: OpenCode's title agent for OpenCode sessions, `claude -p --model haiku` (the judge's flags, prompt on stdin) for Claude sessions, even when OpenCode is also enabled. The `opencodeLaunch` host capability now also reports a provider runtime, because the frontend gates `/wt` and the worktrees view on it.
- **Why:** Naming a Claude worktree through OpenCode would launch an OpenCode instance the session never uses.
- **Follow-up:** none

### Provider sessions cannot be disposed
- **Task:** claude-code-provider.1.3.12
- **Spec:** AD-2 lists no delete operation; configured creates (routines, inherited worktree permissions) require a `SessionDisposer` to roll back.
- **What we did instead:** The generic adapter implements `DisposeSession` by returning an error, so configured creates run on providers, and a session whose ruleset failed to apply stays in place, empty, and is reported as a cleanup error.
- **Why:** Without a disposer every routine run on Claude failed with "capability not supported". Since 1.3.97 applying a ruleset can fail (a `claude-rules.json` write error is returned), so this rollback path is reachable and leaves an empty session reported as a cleanup error.
- **Follow-up:** none

### Claude rulesets live in the ocman-side adapter, not behind the rules RPC
- **Task:** claude-code-provider.1.3.11
- **Spec:** AD-2's Rules row (get/set permission ruleset over the provider protocol); AD-7a, rules are evaluated only in ocman.
- **What we did instead:** The generic adapter takes an optional ocman-owned `provider.Rules` store (`claude-rules.json` beside `state.db`). With it, `PermissionRules`/`SetPermissionRules` are served locally, the adapter answers rule-decided prompts itself (`POST /permissions/{id}/reply`) before they reach the pending map, the judge or a browser, and the Claude supervisor answers the rules RPC with 501. "Allow always" replies are kept as per-session approvals beside the visible ruleset, as OpenCode does.
- **Why:** A supervisor-side store would hold a copy of the ruleset, which AD-7a forbids; the adapter is the ocman-side component that sees every prompt first. Resolves the 1.3.6 follow-up.
- **Follow-up:** The 1.3.17 review found one path where a rule-decided prompt still reaches the judge or a browser ("Permission refresh bypasses ocman's rules and is polled"); resolved by claude-code-provider.1.3.57.

### Claude "Allow always" approves the exact request
- **Task:** claude-code-provider.1.3.11
- **Spec:** FR-5, "Allow always" with the same semantics as OpenCode.
- **What we did instead:** A Claude prompt's `always` patterns equal its patterns (the exact command, path or URL), so "Allow always" approves that exact request for the session and its worktree children. OpenCode widens some always patterns (for example `git status *`).
- **Why:** The CLI's own `permission_suggestions` are exact too, and ocman has no per-tool arity table for Claude commands.
- **Follow-up:** The claim was false until 1.3.105 for arguments containing `*` or `?` (they globbed) and for inherited `webfetch`/`websearch` (inherited as allow-all); both are exact since *Verification 1.5 recovery at `f279f45c` (1.3.105)*.

### The Claude judge is offered only when Claude Code is enabled
- **Task:** claude-code-provider.1.3.11
- **Spec:** AD-8, the Claude judge is used when no OpenCode instance is reachable.
- **What we did instead:** `claude -p --model haiku --no-session-persistence --strict-mcp-config --tools "" --output-format text` judges, with the prompt on stdin, only when `-platforms` includes `claudecode`; an OpenCode-only ocman still leaves an unjudgeable prompt to the user.
- **Why:** An OpenCode-only user did not opt in to ocman spending their Claude account.
- **Follow-up:** none

### Interactive Claude sessions run in the machine-wide in-process supervisor
- **Task:** claude-code-provider.1.3.10
- **Spec:** AD-3, one supervisor per project in tmux; FR-4, a running turn and a pending prompt survive an ocman restart.
- **What we did instead:** The in-process Claude supervisor from 1.3.8 also spawns and pools the `claude` children (pipes, owner-wide cap 4, 600 s idle timeout from turn completion). The platform's `EnsureRuntime` returns without launching anything. Ownership of ocman-launched sessions persists in `claude-sessions.json`, so they stay drivable after a restart, but a turn running at ocman exit ends with it.
- **Why:** One machine-wide supervisor already owns the index and converter; it gives the spike's owner-wide cap without cross-supervisor coordination, and launching tmux per project is not needed to drive pipes.
- **Follow-up:** Resolved by claude-code-provider.1.3.54, see "Claude project supervisors outlive ocman, not the machine". Earlier note (1.3.17 review): pending permission and question prompts are lost at ocman exit too, not only the running turn: the spool lives in a temp directory removed on exit (`claude_platform.go`), so AD-4 replay and AD-7a re-send never happen across an ocman restart, and AD-5 drain-then-replace is unreachable for Claude. Restart survival needs `ocman provider claude` hosted by `ProviderManager` in tmux.

### Claude launch omits the stdio permission prompt tool until permissions land
- **Task:** claude-code-provider.1.3.10
- **Spec:** AD-6's `--permission-prompt-tool stdio`.
- **What we did instead:** Children launch without it, so the CLI applies its own default denials instead of asking ocman.
- **Why:** Without the AD-7a answer path every `can_use_tool` would hang the turn.
- **Follow-up:** resolved by claude-code-provider.1.3.11, which launches with `--permission-mode default --permission-prompt-tool stdio`.

### No shell operation for Claude sessions
- **Task:** claude-code-provider.1.3.10
- **Spec:** FR-4 / AD-2 shell; the spike recommended an explicit owner-local shell path.
- **What we did instead:** `shellExec` is false and `/shell` answers 501, so the composer sends `!cmd` as an ordinary prompt.
- **Why:** stream-json has no shell input and an ocman-run shell would not appear in Claude's transcript or context.
- **Follow-up:** none

### Live replies stream as message and part upserts, not token deltas
- **Task:** claude-code-provider.1.3.10
- **Spec:** AD-2's `message.part.delta` events; AD-6's `--include-partial-messages`.
- **What we did instead:** The flag is passed, but `stream_event` lines are ignored; every assistant or user line is converted and published as `message.updated` / `message.part.updated`.
- **Why:** The committed fixtures omit `message_start` / `content_block_start`, so delta part ids could not be tested against a capture.
- **Follow-up:** Partly resolved by claude-code-provider.1.3.60: `Converter.Stream` turns `message_start` and text/thinking `content_block_start`/`content_block_delta` into the message and a part with its final id (`prt_<message id>_<content_block index>`), republished as `message.part.updated` with the accumulated text, not as `message.part.delta`; the complete assistant line replaces it in place (`TestPartialStreamAgreesWithStoredParts`). Tool input deltas are not streamed. The test fixture is synthetic: the recorded `plain.jsonl` has only one `content_block_delta`, so `message_start`/`content_block_start` follow Anthropic's streaming event shape, not a capture. Thinking that streamed text but is stored signature-only keeps its streamed part until the transcript is re-read.

### Claude composer catalog is static and fork is HEAD-only
- **Task:** claude-code-provider.1.3.10
- **Spec:** AD-2 directory catalog (agents, commands, models); FR-4 slash commands and skills; `ForkSessionRequest.MessageID`.
- **What we did instead:** The catalog lists the `opus`, `sonnet` and `haiku` aliases and no commands; `/name args` and skills are sent as prompt text. Forking from a specific message answers 501. Compact sends `/compact`. Rename appends Claude's own `custom-title` record to the transcript.
- **Why:** The initialize control handshake that returns commands is not in the fixtures, and the fake replay rejects unexpected stdin. The CLI has no measured fork-at-message flag.
- **Follow-up:** Partly resolved by claude-code-provider.1.3.60. Commands come from the CLI's `initialize` response, run in the requested directory with `--plugin-dir` and no user message, cached 10 minutes per directory (`cp/catalog.go`, `TestDirectoryCatalogFromCLI`). The spike recorded only the `commands` key, so entries are read as the Agent SDK's documented `{name, description, argumentHint}` and a bare string as a name. A command is marked `source: "skill"` only once a session's `system/init` in that directory listed it under `skills`; before that skills show as commands. A failed handshake leaves the commands empty. Models stay the three aliases (initialize's `models` shape is not captured). Fork-at-message stays unsupported and is gated by a new `forkAtMessage` capability (see "Fork at a message is a separate capability"). Shell stays gated by `shellExec: false`; revert by `revert: false`. Corrected by claude-code-provider.1.3.97: the production catalog came from the browsing supervisor without ocman's plugin; it now loads it, classifies skills on disk too, and lists initialize's agents as subagents.

### Supported CLI range extends beyond the measured version
- **Task:** claude-code-provider.1.3.10
- **Spec:** AD-5 / NFR-5 pinned and tested range; the spike said not to infer a range from 2.1.220 alone.
- **What we did instead:** `[2.1.220, 2.2.0)` is accepted; anything else makes create, send and fork fail with an explicit `claude CLI <v> is unsupported` error, and no CLI with `claude CLI not found: install Claude Code 2.1.220 or later (below 2.2.0)`.
- **Why:** The installed default CLI was already 2.1.289; pinning one patch release would reject it on every auto-update.
- **Follow-up:** Resolved by claude-code-provider.1.3.60: ocman accepts exactly the versions in `TestedCLI` (`2.1.220`), each of which must have `plain`, `permission` and `subagent` fixtures (`TestUnsupportedCLIVersion`). Consequence: an auto-updated CLI (2.1.289 on the spike machine) is refused for driving until its fixtures are recorded manually; browsing is unaffected.

### Usage limit detection follows the documented rejected status
- **Task:** claude-code-provider.1.3.10
- **Spec:** FR-7 limited status with the reset time.
- **What we did instead:** A `rate_limit_event` with `status: "rejected"` publishes `limited` with `resetsAt` (seconds × 1000); warnings are ignored. The test uses a synthetic fixture.
- **Why:** No real rejection was captured (spike §5).
- **Follow-up:** Replace the synthetic fixture with a natural capture when one exists.

### Claude browsing runs in an in-process supervisor that owns the index
- **Task:** claude-code-provider.1.3.8
- **Spec:** AD-3's one supervisor per project in tmux; the architecture overview draws `claude-index.db` behind the provider adapter.
- **What we did instead:** `-platforms claudecode` starts one machine-wide Claude provider supervisor inside the ocman process, serving the protocol on a private temp-dir unix socket that the generic adapter dials. The Claude provider package owns `claude-index.db` and the converter; `ocman provider claude` serves the same read-only backend.
- **Why:** Browsing must list every project's transcripts, including terminal sessions, before any project supervisor exists, and must not launch tmux to read files. Paging by byte offset needs the converter and the index on the same side.
- **Follow-up:** Interactive sessions first ran in this same supervisor (1.3.10); since claude-code-provider.1.3.54 they run in per-project `ocman provider claudecode` supervisors with MCP config and plugin dir, and this in-process supervisor only browses (see "Claude project supervisors outlive ocman, not the machine"). The departure that remains: the index and browsing stay in a machine-wide in-process supervisor, not in a per-project tmux one.

### Terminal session liveness comes from transcript recency
- **Task:** claude-code-provider.1.3.8
- **Spec:** AD-10, sessions without a live process are settled by `db.SettleSessionStatus` "the same way as OpenCode", where live means a reachable instance.
- **What we did instead:** The settle call passes `TurnUnobserved` and treats a transcript written within the last five minutes as live, so an open turn reads busy while recent and interrupted after.
- **Why:** Without the hook plugin there is no live signal for a terminal session; passing not-live would report every running terminal session as interrupted.
- **Follow-up:** Resolved by claude-code-provider.1.3.58: transcript recency is no longer a lifecycle signal. A transcript is settled as unobserved and not live, as AD-10 specifies, so an open turn reads `interrupted` until the process (owned sessions) or hooks (terminal sessions) report it; a running terminal session without the hook plugin therefore reads interrupted. A busy hook report still lapses after five minutes of no hook, since no hook marks a killed `claude`.

### Legacy root-level sidechain transcripts are not indexed
- **Task:** claude-code-provider.1.3.8
- **Spec:** FR-3, every session under the Claude config directory is listed and subagents render under their Task call.
- **What we did instead:** Subagents are read from the current `<session>/subagents/agent-<id>.jsonl` layout, linked through `agent-<id>.meta.json`'s `toolUseId`. Older `agent-*.jsonl` files beside sessions are skipped, and sidechain lines inside a parent transcript are not split out.
- **Why:** Those layouts carry no link to the spawning call; listing them would show orphan pseudo-sessions.
- **Follow-up:** none

### Explicit acknowledgments and leased snapshots extend the provider protocol
- **Task:** claude-code-provider.1.3.7
- **Spec:** AD-2 lists `/events?after=<seq>` without acknowledgment or snapshot operations; AD-4 requires completed-and-acknowledged spool truncation.
- **What we did instead:** Added capability-gated `/sessions/{id}/ack` and `/sessions/{id}/snapshot` operations, single-use snapshot leases and the version-independent `/drain` operation. Browser streams refresh through current v1 upserts before replaying after their leased cursor; only the headless watcher acknowledges completed turns.
- **Why:** Factory recovery gate claude-code-provider.1.3.31 explicitly approved acknowledgment and snapshot/replay cursors. A headless acknowledgment alone could delete events between a browser's REST snapshot and SSE attachment.
- **Follow-up:** none

### Supervisor backend registration follows the later Claude implementation
- **Task:** claude-code-provider.1.3.7
- **Spec:** AD-3 describes the built-in `ocman provider claude` supervisor spawning CLI children and executing idle-timeout/concurrency policy.
- **What we did instead:** Added the generic supervisor command, tmux runtime, persisted recovery manager and backend publishing/snapshot contract, exercised through a test-only provider executable. Production provider factories remained empty until the Claude backend existed; this issue did not implement Claude process pooling or control messages.
- **Why:** This issue is phase 2 and must be testable before the phase 3 read-only fake CLI and phase 4 interactive Claude backend exist. The task explicitly requires a fake provider and no real Claude invocation.
- **Follow-up:** Partly resolved by claude-code-provider.1.3.10, which registers the Claude backend (`provider_command.go`) and owns pooling and pipe transport. `ProviderManager.Ensure` is wired into Claude's `EnsureRuntime` since claude-code-provider.1.3.54 (see "Claude project supervisors outlive ocman, not the machine").

### Rules transport follows the assigned protocol operations
- **Task:** claude-code-provider.1.3.6
- **Spec:** AD-7a says the supervisor never holds a copy of the ruleset; AD-2 and this assigned issue require provider get/set ruleset operations.
- **What we did instead:** Defined and implemented get/set ruleset RPCs in the generic adapter, matching the assigned endpoint contract. Permission evaluation remains in ocman; this issue does not implement supervisor-side storage or evaluation.
- **Why:** A provider-backed get/set operation cannot itself guarantee ocman-only storage. The approved design specifies both that transport and a prohibition on supervisor-held rules without defining where those operations store their data.
- **Follow-up:** resolved by claude-code-provider.1.3.11, see "Claude rulesets live in the ocman-side adapter".

### Claude judge fallback deferred until the interactive phase
- **Task:** claude-code-provider.1.3.4
- **Spec:** AD-8's two judge backends and FR-5's judge works without OpenCode.
- **What we did instead:** Added a per-permission Judge interface with OpenCode as the only default backend. No available backend returns an explicit needs-user verdict and leaves the permission unanswered.
- **Why:** This assigned seam issue explicitly limits implementation to the existing OpenCode backend; the Claude backend and fake executable belong to later phases.
- **Follow-up:** resolved by claude-code-provider.1.3.11 (`autoapprove.ClaudeJudge` as the fallback backend).

### Claude flag registration waits for the real adapter
- **Task:** claude-code-provider.1.3.2
- **Spec:** AD-1's `knownPlatforms` learns `claudecode`; FR-1's `-platforms` accepts `claudecode`.
- **What we did instead:** Made startup registration a map of adapter constructors and verified an OpenCode-disabled start with a fake second adapter. `claudecode` is not accepted until its adapter exists.
- **Why:** This seam-only issue precedes the Claude read-only adapter. Registering a placeholder would advertise a platform that cannot serve sessions.
- **Follow-up:** resolved by claude-code-provider.1.3.8, which registers `claudecode`.

### Required streaming flags missing from the launch command
- **Task:** claude-code-provider.1.3.1
- **Spec:** AD-6's launch command and FR-3's live subagent transcripts.
- **What we did instead:** Measured with `--verbose` and added `--forward-subagent-text` for the subagent capture. The findings require these flags in the provider launch command; this spike does not change provider code.
- **Why:** 2.1.220 exits without `--verbose` when print output is stream-json. A successful child run without `--forward-subagent-text` forwarded tool events but no child text/thinking; the run with it forwarded both.
- **Follow-up:** Resolved by claude-code-provider.1.3.10: `internal/claudeprovider/pool.go` passes `--verbose` and `--forward-subagent-text`.

### Shell shortcut is not a stream-json shell operation
- **Task:** claude-code-provider.1.3.1
- **Spec:** AD-2's shell operation, AD-6's `!cmd` assumption, FR-4's shell input.
- **What we did instead:** Recorded that `!cmd` reaches the model as ordinary user text and costs an API turn. Recommend an explicit owner-local shell path rather than forwarding the interactive shortcut.
- **Why:** The real CLI interpreted the input through the model and a Bash tool call; it did not execute a native shell shortcut.
- **Follow-up:** Resolved by claude-code-provider.1.3.10, see "No shell operation for Claude sessions".

### Subagent tool name differs from the Task assumption
- **Task:** claude-code-provider.1.3.1
- **Spec:** AD-7 / AD-9 subagent parent links and FR-3's transcripts as children of their Task call.
- **What we did instead:** Captured an `Agent` call and its child transcript, with `agent_id` linked through `task_started.task_id` to the parent tool id. The converter must recognise the measured Agent name as well as Task.
- **Why:** 2.1.220's init advertises Task, but ToolSearch resolves `select:Task` to Agent and the live call uses that name.
- **Follow-up:** resolved by claude-code-provider.1.3.8: the converter maps Task and Agent to `task`, and the index links subagents through their meta file's `toolUseId`.

### Permission wait and hard-limit assumptions remain bounded evidence
- **Task:** claude-code-provider.1.3.1
- **Spec:** AD-7a's pending-prompt timeout assumption, AD-4 reconnect waits, FR-7 / AD-10 hard-limit lifecycle.
- **What we did instead:** Verified a 180.002-second unanswered permission and its successful later reply, plus real `allowed_warning` rate-limit messages. No unbounded-wait claim, restart/reconnect proof or fabricated quota-rejection fixture was made.
- **Why:** A finite manual spike cannot prove that a prompt never times out. The account did not hit a hard limit during the spike; deliberately exhausting it was unnecessary.
- **Follow-up:** Partly resolved. 1.3.10 handles a rejection from a synthetic fixture ("Usage limit detection follows the documented rejected status"); 1.3.7 / 1.3.11 re-send pending prompts on watcher reconnect, and since 1.3.54 across an ocman restart (`TestClaudeSupervisorSurvivesOcmanRestart`). Still unmeasured: a natural hard-limit rejection and a prompt wait longer than 180 s; neither is claimed.

### Complete account utilization is not a measured structured snapshot
- **Task:** claude-code-provider.1.3.1
- **Spec:** AD-2's account operation, AD-12's limit utilization, FR-7's current five-hour and weekly utilization.
- **What we did instead:** Captured subscription type from initialize's account object, a structured weekly warning and both windows as `/usage` result text. Findings identify locale-dependent parsing as a fallback, not an established structured all-window operation.
- **Why:** Account-info control requests were unsupported, initialize had no utilization, and `/usage` returned human-readable localized text.
- **Follow-up:** 1.3.14 read the OAuth usage endpoint with the CLI's credentials, which the requirements forbid; claude-code-provider.1.3.55 replaced it under the approved FR-7 departure, see "Claude account utilisation comes from observed CLI rate-limit events".

### Recovery check run: resource settings, original failures and the frontend baseline gap
- **Task:** claude-code-provider.1.3.53
- **Spec:** Recovery brief 1 (claude-code-provider.1.5 comment #19): Chromium E2E, frontend/Go coverage against the current official baseline, `make lint`.
- **Environment:** macOS arm64, 14 CPUs, 36 GiB, Go 1.26.1, Node 25.9.0, Bitdefender endpoint scanning. Coverage runs used `scripts/coverage-collect.sh` unchanged (default `go test -p` = 14, vitest defaults). Baselines came from `scripts/coverage-fetch-baseline.sh` (gh-pages `75790d1b3`: frontend 87.49, Go 86.3), with tolerance 0.1 unchanged. E2E ran `playwright test --project=chromium` with 0 retries against `vite build` output, served by `vite preview` on :8248 with `/api` and `/mcp` proxied to a closed port. The live ocman on :8228/:8229 was never reached and no database was opened.
- **Original failures:**
  - `auth.spec.ts:131,155`: `getByRole('button', { name: 'Account' })` also matched "Provider accounts", so both locators now use `exact: true`.
  - `TestProviderDeadPersistedRowIsReprobedAndRelaunched` failed with `context deadline exceeded` at its first `ensureRoot` in 2.47 s. It reproduced in coverage runs at load 37–49 and also at load 9. Cause: the fixture writes a new fake `tmux` script for each test, and macOS validates a new executable on its first run. That first exec took 0.12–0.37 s at idle against 0.01 s afterwards, and under parallel package load it passed `tmux`'s fixed 2 s command deadline. The fixture now runs the script once before the test starts.
  - `TestScanDescribe/success` failed with `context deadline exceeded` after 3.01 s in the full coverage run, and took 0.24 s when run alone. The cause is the same first exec, this time of the new per-subtest helper script. The existing warm-up only warmed the test binary. `helperExecutable` now runs each script once with a non-`describe` verb, which exits 2 before any mode side effect. Both fixes only move first-exec latency out of the measured window: deadlines and assertions are unchanged.
  - The coverage runs at load 37–49, caused by unrelated concurrent vitest and VM processes, also produced timing failures in `term`, `tmux`, `toolpath`, plugin process tests and one `remote` in-memory SQLite race. None of these reproduced at load 5–9 and none touch this branch's code, so they were left as they are.
- **Results:** 207/207 Chromium E2E pass. `make lint` passes. Go coverage is 86.4 against 86.3: PASS. Frontend coverage is 86.23 against 87.49: **FAIL**.
- **What we did instead:** Added `frontend/src/lib/api.routes.test.ts`. It pins the route, verb and body for 130 API wrappers, including this branch's provider-account, hook-plugin and default-platform calls, and raised frontend coverage from 85.28 to 86.23. The remaining gap does not come from this branch: its merge-base `ee5e33264` measures 85.25 and the branch 85.28. Main has gained 130+ commits of tests since then. Each check run used whatever baseline main published that day (86.0, then 87.49), so the target keeps moving and can only be compared fairly on the tree merged with main.
- **Follow-up:** Merging current main into this branch has 14 conflicted files (factory, session start, MCP, routes, NewConversation, api.ts). Delivery criterion 2 (.1.2 comment #20) covers bringing the branch up to date. Recovery gate claude-code-provider.1.3.67 approved accepting the no-drop evidence against the merge-base and repeating the frontend official-baseline check at delivery, after its main merge. Re-run by 1.3.61, see "Re-audit check run".

### Re-audit check run (1.3.61)
- **Task:** claude-code-provider.1.3.61
- **Spec:** NFR-7 coverage ratchet; recovery brief 9 (claude-code-provider.1.5 comment #19): `make lint`, `make test`, `go build`, `make proto` without diff, `make docs-build`, Chromium E2E and current-baseline coverage-check.
- **Environment:** macOS arm64, 14 CPUs, load 4-25 from unrelated processes. Commands unchanged, no lowered thresholds, exclusions, skips or retries. Temporary directories only; the live ocman on :8228/:8229 was never used.
- **Results:** `make lint` pass; `make test` pass (53 Go packages, 3,209 vitest tests); `go build -o /dev/null .` pass; `make proto` with protoc 33.4 leaves `internal/remote/proto` unchanged; `make docs-build` pass; Chromium E2E 207/207 pass with 0 retries, against `vite build` output served by `vite preview` on 127.0.0.1:18348 with `/api` and `/mcp` proxied to the closed port 1 (`E2E_NO_WEBSERVER=1`). Coverage (`make coverage SUITE=all`): Go 86.6 against the gh-pages baseline 86.6, **PASS**; frontend 86.26 against 88.81, **FAIL** (-2.55). The baseline is main `5c2cebb92` from the same day; 1.3.53 measured the branch's merge-base `ee5e33264` at 85.25, so the branch raises frontend coverage, and the gap is main's newer tests. Per gate 1.3.67 delivery repeats this check after merging main.
- **What we did instead:** The first two Go coverage runs failed `internal/term`'s `TestTerminalServerIsolation` and `TestAllTermWindowNames_NoTmuxServerReturnsEmpty` with `context deadline exceeded` (both pass alone and in `make test`). Same cause as 1.3.53's fixtures: macOS validates a newly written `tmux` wrapper on its first run, which under parallel package load passed the 2 s tmux deadline. Both fixtures now run their script once before the test. Deadlines and assertions are unchanged; the third run passed.
- **Why:** The acceptance requires every check to pass without lowered deadlines.
- **Follow-up:** Frontend official baseline at delivery (gate 1.3.67).

### Claude project supervisors outlive ocman, not the machine
- **Task:** claude-code-provider.1.3.54
- **Spec:** AD-3, one supervisor per project in tmux, persisted like `managed_opencode` and re-probed; AD-4, replay across ocman restarts; AD-5, drain then replace; FR-4, a running turn and a pending prompt survive an ocman restart; recovery brief 2 (claude-code-provider.1.5 comment #19).
- **What we did instead:** Implemented as specified, with these exact departures:
  - Two kinds of supervisor serve one platform. The in-process supervisor still browses every transcript, owns index syncing and serves terminal sessions read-only; it never launches `claude`. `ocman provider claudecode` (one per repo root, `ProviderManager` + `ProviderRuntime`) drives launched sessions with `NoSync`, reading the shared `claude-index.db`. `claudePlatform` (`claude_projects.go`) routes each call: a session in `claude-sessions.json` goes to its project supervisor's adapter (started on send/create/command/fork/rename/compact, never on reads), anything else to the browsing adapter. There is no protocol-level aggregation of several sockets.
  - `ProviderManager` gained `Recover` (re-probe a persisted row without launching; drain-and-replace on version skew or, since 1.3.97, an earlier launch configuration, keeping a route that answers its pending prompts; a dead row waits for the next ensure, so startup launches nothing) and per-root locks, so one draining supervisor does not block other projects. `ProviderRoot` falls back to the canonical directory outside a git repository, because Claude sessions do not need one.
  - Ownership is shared by every supervisor through `claude-sessions.json`, updated under an `flock` on `claude-sessions.json.lock` and reloaded when the file is replaced.
  - The cap of 4 children is machine-wide through `flock` slot files in `<data>/claude-slots` (held until the child is reaped since 1.3.97; helper calls have a separate budget of 2 and slot I/O errors fail the turn since 1.3.99). A supervisor at the cap with no idle child of its own polls (200 ms) and holds a `wait-*` lock file; supervisors with idle children evict one when they see a live waiter. Within one supervisor the existing eviction is unchanged. The 600 s idle timeout is unchanged.
  - `ocruntime.ProviderRuntime.Args` appends provider flags at each launch; `ocman provider` gained `--data-dir`, `--mcp-addr` and `--plugin-dir` (and, in 1.3.97, `--claude-cli` and `--claude-config-dir`), and accepts `claudecode` (the runtime's id) as well as `claude`.
  - A browser stream opened for a launched session whose supervisor is not attached yet (right after a restart, before `Recover` finishes) reads the browsing supervisor and ends when any project supervisor attaches, so the client reconnects to the right one; since 1.3.105, when recovery instead finds the supervisor out of range, the same stream continues with its draining prompts.
  - The project supervisor's spool lives under `<data>/p/<name>/spool` and is not reloaded by a new supervisor process (see "Supervisor spool and previous protocol version are nominal"): a reboot or a killed tmux server still ends running turns and pending prompts. The session stays drivable; the next send relaunches the supervisor and `--resume`s.
  - Unix socket paths must stay under 104 bytes, so a very long ocman data directory makes project launches fail with an explicit error.
- **Why:** Browsing must list every project's transcripts before any project supervisor exists and must launch nothing (brief 2); the existing `ProviderManager` launches per repo root, so a machine-wide cap and shared ownership need cross-process coordination, and `flock` needs no new dependency or daemon.
- **Follow-up:** none

### Claude prompts are push-only; rule replies retry
- **Task:** claude-code-provider.1.3.57
- **Spec:** AD-7a, no component polls and `RefreshPermissions` is used only for reconciliation; rule-decided prompts never reach the judge or a browser; AD-7 / FR-5 provenance, inheritance and judge semantics.
- **What we did instead:** Implemented as specified, with these exact departures:
  - `platforms.Capabilities` gained `pushPrompts`, advertised by the Claude provider. With it the session page runs neither the 3 s permission-refresh poll nor the question-list poll, and the 30 s Inbox reconciliation skips the platform: its Inbox items resolve from the replayed `permission.replied`. OpenCode keeps both polls (its replies can be missed). An Inbox item whose supervisor died with the prompt (no replied event ever) is not archived by a poll; it stays until answered or archived.
  - `RefreshPermissions` stays on demand (`?refresh=1`, remote gRPC). It drops ids the rules answered whose reply is in flight, and supervisor-pending prompts the stream has not delivered yet when the rules would decide them (that event applies the rules). A prompt already pending for the judge or user stays even if the rules changed since. A local prompt change during the read still fails the refresh with `ErrReconciliationConflict`.
  - Browser streams hide a prompt the headless consumer answered by rule and show one it left pending, re-evaluating the rules only for prompts it has not seen.
  - A failed rule reply is logged at warn level with the session and permission ids and retried with backoff (250 ms doubling to 30 s) until delivered, the provider no longer holds the prompt (404), the provider reports the reply, or the adapter closes. The prompt meanwhile stays pending in the provider only. An ocman restart replays the unacknowledged ask and the rules answer again. "Visible" means the log and the session's open turn; there is no UI notice.
  - The supervisor writes the `control_response` before settling a prompt: a failed write returns the error, keeps the prompt pending (an exiting child still cancels it) and emits no reply event. A reply racing an in-flight write gets 409.
  - The fake CLI exits with status 3 on a `control_response` whose `request_id` is not the open recorded request, or whose decision is malformed (not `success`, allow without `updatedInput`, `AskUserQuestion` allow without `questions` and `answers`, deny without `message`, unknown behavior).
  - The pending-prompt-across-restart acceptance is `TestClaudeSupervisorSurvivesOcmanRestart` from claude-code-provider.1.3.54, unchanged.
- **Why:** The brief required push-only delivery and reliable rule replies; a capability keeps the frontend free of platform identity checks.
- **Follow-up:** none

# Project Settings - Friction with the existing setup

Companion to [`architecture.md`](architecture.md).

1. **The Go tee drops the retry payload.** `Tee.OnSessionStatus` is
   `func(sessionID, statusType string)` (`tee.go:621`,
   `live_status.go:97` stores only `busy bool`). Trigger A needs
   `attempt/next/action`. Change the callback to pass a parsed
   `SessionStatus` struct; two call sites (`watcher_stream.go:129`,
   proxy tee is permission-only). `turnRunning` keeps collapsing
   `retry` to running.

2. **Queue drains into errored sessions.** The idle-edge flush
   (`queue.go:206`, `drainHead trustIdle=true`) and `Sweep`
   (`Status != busy`) will push the next held message — with *its*
   stored model, probably the exhausted one — into a session that just
   failed on quota. Ordering fix: in `onSessionIdle`, run the
   fallthrough check **before** enqueuing the flush; if fallthrough
   sends a continuation, skip the flush (the session is busy again; the
   next real idle drains). Additionally `drainHead` should rewrite
   `req.Model` when that provider is in cooldown. Without this the
   fallthrough and the queue fight.

3. **`sendNow` retries only on `ErrPlatformUnreachable`**
   (`queue.go:141`). Model-level failures come back asynchronously via
   events, never as a send error, so there is no place in the send path
   to hook; the engine has to be event-driven. This is fine but means a
   *Routine* or *Factory* first prompt that hits quota is handled the
   same as an interactive one — the continuation message lands in the
   routine's session. Routines currently mark `StatusError → RunFailure`
   (`routines/service.go:399`); with fallthrough the run stays `running`
   until the fallback turn settles. Verify the settle logic tolerates a
   busy→error→busy→done sequence.

4. **Hub has no event view of remote sessions unless a browser is
   watching.** Forces owner-side execution (AD-1). Multi-remote users
   must upgrade each remote for fallthrough to work there; the hub-side
   settings page will 503 against an old remote (unimplemented RPC).
   Surface that as "remote does not support project settings" in the
   UI rather than a raw 503.

5. **`session.idle` is deprecated upstream** (`session-status-event.ts`
   marks `Idle` deprecated; it is still published by
   `SessionStatus.set` on idle). The queue and `onSessionIdle` hang off
   it. Trigger B should hook `session.status {type:"idle"}` as well so
   we are not the last consumer of a deprecated event. Same for
   `broadcastSessionStatus`.

6. **`Status == busy` while parked.** `turnRunning` treats `retry` as
   running, so a session parked 4 hours shows `busy` in the sidebar and
   `Sweep` will not touch it. After fallthrough aborts it, OpenCode emits
   idle → error (aborted) → then busy again from our continuation. Expect
   one `MessageAbortedError` assistant message in the thread; the
   frontend already skips rendering it (`convertMessages.ts:657`), but
   `InferSessionStatus` sees it as `error` for a moment. Harmless, but
   sidebar flicker tests may need an allowance.

7. **V2 steer path vs model override.** `sendMessageOnPort` uses the V2
   `/api/session/{id}/prompt` (`delivery: steer`, no model in body) only
   when `v2SessionMatches` says the session already has that model
   (`operations.go:325-356`). Our continuation always passes a different
   model, so it takes the legacy `prompt_async` path with an explicit
   model — correct, but it means the OpenCode session-level model is
   updated by OpenCode as a side effect of the prompt, not by us. Fine;
   just don't assume `GET /api/session/{id}.model` reflects our choice
   until the turn starts.

8. **`ExecuteCommand` drops `Reasoning`** (`operations.go:443`). Not our
   bug, but a fallthrough that lands on a model without the previous
   `variant` must clear `Reasoning`; the `/variants` list is per model
   (`models.go:14-29`). The engine sends no `Reasoning` — OpenCode picks
   the model default.

9. **Project identity is a string fold, not git.**
   `ProjectRootForDirectory` only folds `<p>/.worktrees/<repo>/<slug>`.
   A worktree created outside ocman's layout gets its own settings row.
   Accept; matches how archive and sidebar grouping already behave.
   `EnsureProjectOpencode` uses `git rev-parse --show-toplevel`
   (`git/worktree.go:63`), which for a linked worktree returns the
   worktree — also pre-existing.

10. **Provider availability.** `next` must be a connected provider, or
    the continuation fails with `ProviderAuthError`/model-not-found and
    the episode loops through the list burning one turn each. Check via
    the cached `/provider` catalogue (`catalogCache`, 30 s,
    `http.go:71`): skip models whose provider is not in `connected`.

11. **Frontend seed precedence is a policy change.** Users who relied on
    "last picked model in this dir" (`ocman.projectModels.v1`) will see
    the project default instead once they set one. Intended, but call it
    out in the settings row description.

12. **Coverage ratchet.** New package + tee change + composer change all
    need tests in the same PR: table-driven `Decide()` tests (each
    trigger, patience boundary, exhausted list, cooldown, unavailable
    provider), a `Tee` test with the retry fixture already in
    `tee_dispatch_test.go:189`, an `onSessionIdle` ordering test with the
    server `fakePlatform`, and a vitest for the seed chain.


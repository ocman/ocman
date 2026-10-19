# v1map golden fixtures

Each `<platform>/<scenario>.v1.jsonl` is a v1-shaped session stream as
`Platform.ProxyEvents` writes it. `<scenario>.oce.jsonl` is the mapped OCE
stream, framed as envelopes with epoch `golden`, a 1-based `seq`, the
fixture's session and the directory name as platform. `at` and every
mapper-supplied time are `1700000000000`.

Regenerate after an intentional mapping change and review the diff:

```sh
go test ./internal/convevents/v1map -run TestGolden -update
```

## Input format

One JSON object per line:

- `{"session": "<id>"}` (first line): the page session the stream is for.
- `{"event": "<name>", "id": "<sse id>", "data": <payload>}`: one SSE frame.
  `event` is omitted for the default channel; `id` is the provider sequence
  for Claude streams.

No fixture was captured from a live system or a running agent.

## Scenarios and provenance

| Fixture | Scenario | Provenance |
| --- | --- | --- |
| `ocv2/streaming-text-reasoning-tool` | text and reasoning streaming with emoji offsets, tool start → completion, edit tool, step accounting | `ocv2` translator output for the v2 events of `TestEventsAssistantFlow` (`internal/ocv2/events_test.go`), extended with execution edges, a second delta and an edit tool |
| `ocv2/retry-backoff` | retry backoff, heartbeat, server.connected background seed | translator output for `TestEventsLifecycle` inputs |
| `ocv2/permission-question` | permission asked → replied, question form answered and cancelled | translator output for `TestEventsPermissionsAndQuestions` inputs |
| `ocv2/native-queue` | native queue (v2 inbox) enqueue → deliver, re-sent recent messages | translator output for the inbox events of `TestEventsSessionEvents` |
| `ocv2/background-shell` | background shell keeps the session busy, then settles | translator output for `TestBackgroundShellKeepsSessionBusy` inputs |
| `ocv2/revert` | revert staged → cleared (session metadata) | translator output for `session.revert.staged/cleared` with a fake session read |
| `ocv2/subagent-child-prompt` | subagent created, child status, child permission prompt | translator output for `session.created` of a child plus child execution and permission events |
| `ocv2/compaction` | compaction re-sends recent messages | translator output for `session.compaction.started/ended` |
| `ocv2/snapshot-before-step` | v2 snapshot-before-step race (stored reply already projected) | translator output for `TestEventsStartedSnapshotDoesNotDuplicateReply` inputs |
| `opencode/streaming-text-tool-output` | mid-turn attach (unanchored delta withheld), reasoning and text deltas, non-final snapshot, tool output deltas, edit completion, `session.diff`, idle twin, foreign session part | hand-built from OpenCode v1 shapes in `frontend/src/lib/sessionReducer.test.ts`, `sseHelpers.ts` and `internal/ocv2` (`ConvertMessage` part fields) |
| `opencode/retry-backoff` | retry notices, `session.error` | hand-built from the v1 `session.status` retry shape in `sessionSseReducer.ts` |
| `opencode/compaction` | `session.compacted` | hand-built v1 frames |
| `opencode/revert` | revert marker, part and message removal, archive, share URL | hand-built v1 session info shapes |
| `opencode/question-answered-in-tui` | question tool answered outside ocman (resolved by the tool part), string-encoded questions, rejected and malformed questions | hand-built from `answeredQuestionRequestId` / `normalizeQuestionItems` cases in `sseHelpers.ts` |
| `opencode/named-channel-recovery` | named `tool` channel, bare part payloads, global `{directory, payload}` wrapper, default-branch message recovery, unknown and non-object frames | hand-built from `parseEvent` in `sessionSse.ts` |
| `opencode/subagent-child` | child created/updated/deleted, child status twin, child prompt, unrelated session status dropped | hand-built v1 session frames |
| `ocman/autoapprove-judge` | permission → judge scheduled → checking → auto-approved notice; judge flagged → user reply → user-approved notice | `ocman.permission.*` payloads as written by `internal/autoapprove` (`statemachine.go`, `background.go`, `background_judge.go`) through `WriteSSEEvent` |
| `ocman/session-signals` | background tasks (dedupe, invalid entries), queue change, commit observation | `ocman.session.background` from `claudeprovider/background_tasks.go` and `ocv2/activity.go`; `ocman.queue.changed` from `ocv2/events.go` |
| `claudecode/limited` | Claude streaming upserts, cancelled prompt, `limited` with `resetsAt`, withheld idle, error status | provider frames from `internal/platforms/provider/events_test.go` and part ids from `internal/claudeprovider/live_render_test.go` |
| `claudecode/acknowledged-replay` | acknowledged lifecycle replay (`replayed: true`) around a live prompt | `providerprotocol.Event.Acknowledged` as set by `providersupervisor` |
| `claudecode/background-subagent` | Claude task tool with child session metadata, background tasks, write tool completion | Claude converter shapes (`claudeprovider/convert.go`, `live_render_test.go`) |

## Not covered (TODO)

- Judge-approved permission on a live Claude stream (the provider protocol
  has no judge frames; only the ocman synthetics are covered).
- OpenCode v1 tool-input streaming: no v1 source emits `tool.input` deltas.
- v2 `history.rewritten` for compaction and revert commits: the ocv2
  translation carries neither, so the mapper emits only message upserts and
  `session.updated`; the native v2 mapper (design step 7) adds them.
- Real captures through `OCMAN_CAPTURE_EVENTS` (design §13.2), which arrives
  with the hub in step 2.

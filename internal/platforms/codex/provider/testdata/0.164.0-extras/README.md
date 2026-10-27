# Codex 0.164.0 extras fixtures

Recorded from a real `codex app-server` (codex-cli 0.164.0) with:

```sh
go run -tags codexrecord ./internal/platforms/codex/appserver/codextest/record -scenario extras -out internal/platforms/codex/provider/testdata/0.164.0-extras
```

The recorder isolates `CODEX_HOME`, `HOME` and `TMPDIR` in a scratch directory
(only `auth.json` is copied in) and scrubs paths, the hostname, installation,
account and user ids, email addresses and the user agent.

One thread: a plain turn, then `thread/shellCommand`, `thread/compact/start`
and an inline `review/start`, two forks (whole, and through the first turn),
`skills/list`, `account/read` and `account/rateLimits/read`.

- `transcript.jsonl`: every wire message.
- `thread-read.json`, `turns.json`, `items.json`: the stored history afterwards.
- `rollout.jsonl`: the thread's rollout file, for token usage.

Observed behavior the code relies on:

- Shell commands, compaction and reviews each run as a turn with
  `turn/started` and `turn/completed`. A shell command's item is a
  `commandExecution` with `source: "userShell"`; compaction stores a
  `contextCompaction` item.
- `review/start` answers with the outer turn id, while `turn/started`
  announces an inner one; `turn/completed` reports the outer id.
- `thread/fork` without `excludeTurns: true` emits a deprecation notice.
- Per-turn usage exists only in the rollout: `token_usage_record` lines carry
  `turn_id`, the request's `usage` and the turn's running `turn_token_usage`;
  `task_started` carries `model_context_window`.

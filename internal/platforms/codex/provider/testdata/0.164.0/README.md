# Codex 0.164.0 fixtures

Recorded from a real `codex app-server` (codex-cli 0.164.0) with:

```sh
go run -tags codexrecord ./internal/platforms/codex/appserver/codextest/record -out internal/platforms/codex/provider/testdata/0.164.0
```

The recorder uses an isolated `CODEX_HOME` holding only a copy of `auth.json`
and a scratch git repository. It scrubs the scratch path (`/fixture`), the
hostname, the installation id and the user agent before writing.

- `transcript.jsonl`: every wire message, `{"dir":"send"|"recv","msg":...}`.
  One thread, two turns: an approved `ls`, an approved `apply_patch` creating
  `hello.txt`, then a turn interrupted by cancelling the approval for
  `sleep 30`.
- `thread-read.json`, `turns.json`, `items.json`, `thread-list.json`: the
  results of `thread/read` (metadata only), `thread/turns/list`
  (`itemsView: notLoaded`), `thread/items/list` and `thread/list`.

Observed behavior the code relies on:

- `thread/read` with `includeTurns` is deprecated for paginated threads, the
  default; history is paged through `thread/turns/list` and
  `thread/items/list`.
- Items inside turns carry no timestamps; `thread/items/list` entries and live
  `item/started`/`item/completed` params do, with equal values.
- Commands are shell-wrapped (`/bin/zsh -lc ls`).
- Server request ids start at `0`. A cancelled command is not stored.

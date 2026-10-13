# Claude Code 2.1.220 stream-json excerpts

Manual captures for Factory Issue `claude-code-provider.1.3.1`, 2026-10-05.
See [spike findings](../../../../spec/multi-agent-support/spike-findings.md) for
launch flags, measurements and design effects.

| Fixture | Captured behaviour |
|---|---|
| `plain.jsonl` | Plain Haiku turn, user replay, text delta, final message/usage, real weekly rate-limit warning |
| `permission.jsonl` | Bash permission held for 180.002 seconds, one-time approval, echoed response, tool result, completed turn |
| `subagent.jsonl` | Sonnet foreground Agent, ToolSearch's Task alias, task lifecycle, child permission attribution, child tool/result/text, parent completion |

Each JSONL line is a capture envelope:

```json
{"t":3.483,"direction":"stdout","message":{"type":"control_request"}}
```

`t` is monotonic seconds since the process launch. `message` is the actual wire
object to write/read, not the envelope. A fake CLI can consume stdin rows and
emit stdout rows without waiting real seconds. Never run these fixtures through
the real CLI in automated tests; their Bash inputs write files.

These are selected excerpts, not exhaustive event-stream recordings. Omitted:
launch metadata, init catalogs, status/thinking events, thinking-only assistant
messages, command lifecycle events, and partial events except the plain text
delta. The plain file contains the first turn only. No omitted message is
required to correlate the retained permission/tool/subagent exchanges. These
fixtures are suitable for message conversion and permission replay; they do not
claim complete partial-stream, restart or quota-rejection coverage.

Anonymisation consistently replaces UUIDs, message/request/tool identifiers and
agent ids within each file. Paths become `/spike/...`. Original timestamps,
model names, token counts, costs, message field names and parent relationships
remain intact. The scenarios contain only synthetic marker content. Each file
has its own identifier namespace; do not join sessions across files.

Offline syntax/shape check from the repository root:

```sh
for fixture in internal/claudeprovider/testdata/2.1.220/*.jsonl; do
  jq -e -s '
    length > 0 and
    all(.[]; (.t | type) == "number" and
      (.direction == "stdin" or .direction == "stdout") and
      (.message.type | type) == "string") and
    (map(.t) == (map(.t) | sort)) and
    any(.[]; .direction == "stdout" and .message.type == "result" and
      .message.is_error == false)
  ' "$fixture" || exit 1
done
```

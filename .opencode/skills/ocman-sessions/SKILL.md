---
name: ocman-sessions
description: Use when the user asks to inspect, search, start, or message ocman sessions, or to delegate work to an OpenCode subagent with an explicit model.
---

# Ocman Sessions

Include a short, goal-focused `intent` on every ocman MCP call, including help
and read-only calls, so the user sees why you are calling the tool. In Code Mode,
write `intent` as an inline string literal, not a variable or computed value.
This explains the call; it does not grant permission or change the operation.

Use the `sessions` MCP tool. Start with `{"action":"help","intent":"Check how to find or start the requested session"}` for current
schemas, limits, examples, and errors. Its actions:

- `list` — recent sessions, optionally scoped to a `directory`.
- `search` — match a `query` against session IDs, titles, directories,
  platforms, and host names.
- `get` — one session by `platform` and `session_id`, with its latest messages.
- `create` — start a new top-level session and send it `prompt`.
- `send` — send `prompt` to an existing recipient's `session_id`, with optional
  `platform` to disambiguate or target a remote machine.

`list`, `search`, and `get` are read-only. The tool cannot cancel sessions.

Use `send` only when the user requests messaging another session. Unlike
`create`, its `session_id` identifies the recipient, not you. It sends plain text
immediately, including mid-turn, using normal project/session model selection
and the recipient's permissions, without changing its held queue.
A successful result acknowledges delivery, not
completion. Follow up with `get` to inspect replies. Do not blindly retry an
uncertain delivery failure.

`{"action":"send","intent":"Send the test results to the review session","session_id":"ses_recipient","platform":"opencode","prompt":"The tests passed. Continue with the review."}`

Use `create` only when the user asks for a separate session. Pass optional
`model` (`provider/model`), `agent` (e.g. `build`, `plan`), and `title`. Pass
your own `platform` and `session_id` so the new session starts in your project
root on your machine, or pass an absolute `directory`:

`{"action":"create","intent":"Start the requested PR review in a separate session","prompt":"Review the open PR","model":"anthropic/claude-sonnet-4","agent":"plan","platform":"opencode","session_id":"ses_caller"}`

It returns the new `platform` and `session_id`; follow up with `get`. The new
session uses default permissions and runs independently of yours.

For parallel work inside your own turn, use OpenCode's native Task subagents
instead. Choose the appropriate `subagent_type` and give it a complete task.
To choose a model for one Task call, start its prompt with
`[model: provider/model-id]` or `[model: provider/model-id#variant]`.

Without an explicit per-task choice, a subagent's configured `model` wins. A
subagent with no configured model inherits the invoking primary agent's model.
Configure a model on the agent when the choice should persist across tasks.

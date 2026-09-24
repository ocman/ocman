---
name: ocman-sessions
description: Use when the user asks to inspect, search, or start ocman sessions, or to delegate work to an OpenCode subagent with an explicit model.
---

# Ocman Sessions

Use the `sessions` MCP tool. Start with `{"action":"help"}` for current
schemas, limits, examples, and errors. Its actions:

- `list` — recent sessions, optionally scoped to a `directory`.
- `search` — match a `query` against session IDs, titles, directories,
  platforms, and host names.
- `get` — one session by `platform` and `session_id`, with its latest messages.
- `create` — start a new top-level session and send it `prompt`.

`list`, `search`, and `get` are read-only. Nothing in the tool cancels or
messages an existing session.

Use `create` only when the user asks for a separate session. Pass optional
`model` (`provider/model`), `agent` (e.g. `build`, `plan`), and `title`. Pass
your own `platform` and `session_id` so the new session starts in your project
root on your machine, or pass an absolute `directory`:

`{"action":"create","prompt":"Review the open PR","model":"anthropic/claude-sonnet-4","agent":"plan","platform":"opencode","session_id":"ses_caller"}`

It returns the new `platform` and `session_id`; follow up with `get`. The new
session uses default permissions and runs independently of yours.

For parallel work inside your own turn, use OpenCode's native Task subagents
instead. Choose the appropriate `subagent_type` and give it a complete task.
To choose a model for one Task call, start its prompt with
`[model: provider/model-id]` or `[model: provider/model-id#variant]`.

Without an explicit per-task choice, a subagent's configured `model` wins. A
subagent with no configured model inherits the invoking primary agent's model.
Configure a model on the agent when the choice should persist across tasks.

---
name: ocman-sessions
description: Use when the user asks to inspect or search ocman sessions, or to delegate work to an OpenCode subagent with an explicit model.
---

# Ocman Sessions

For session inspection, use only the read-only `sessions` MCP tool. Start with
`{"action":"help"}` for current actions, schemas, limits, and examples. It
does not create, cancel, or message sessions.

For parallel work, use OpenCode's native Task subagents instead of MCP session
splitting. Choose the appropriate `subagent_type` and give it a complete task.
To choose a model for one Task call, start its prompt with
`[model: provider/model-id]` or `[model: provider/model-id#variant]`.

Without an explicit per-task choice, a subagent's configured `model` wins. A
subagent with no configured model inherits the invoking primary agent's model.
Configure a model on the agent when the choice should persist across tasks.

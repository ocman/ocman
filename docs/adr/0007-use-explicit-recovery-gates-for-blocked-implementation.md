# Use explicit Recovery Gates for blocked implementation

An implementation agent creates a Recovery Gate through Factory MCP only when it explicitly cannot safely continue. The Gate pauses and preserves its Attempt/session/worktree without consuming capacity; a human may resume it with a response, retry in a fresh Attempt, or cancel it. Ordinary permission prompts remain direct, while requests outside the selected profile create Authority Escalation Gates.

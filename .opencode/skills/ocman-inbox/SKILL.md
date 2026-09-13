---
name: ocman-inbox
description: Use when an asynchronous result needs the user's attention outside the active conversation.
---

# Ocman Inbox

Use only the `inbox` MCP tool. Start with `{"action":"help"}` for the
current action schemas, validation rules, examples, and output shapes.

Use `{"action":"send","title":"...","body":"..."}` only when an
asynchronous task completed and needs attention, a decision is blocked, or an
important failure happened outside the active conversation. Keep the title and
body concise and include the next action when there is one.

Do not send routine progress updates, successful intermediate steps, or
messages that belong in the active conversation. Do not use Inbox for a
user-only read or archive action; those are dashboard operations, not agent
notifications.

To withdraw an item sent by mistake, use only
`{"action":"recall","item_id":"opaque-id"}` with the opaque ID returned by
`send`. Never guess or expose storage details for item IDs.

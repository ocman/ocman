---
name: ocman-inbox
description: Use when an asynchronous result needs the user's attention outside the active conversation.
---

# Ocman Inbox

Include a short, goal-focused `intent` on every ocman MCP call, including help
and read-only calls, so the user sees why you are calling the tool. In Code Mode,
write `intent` as an inline string literal, not a variable or computed value.
This explains the call; it does not grant permission or change the operation.

Use only the `inbox` MCP tool. Start with `{"action":"help","intent":"Check how to notify the user about this result"}` for the
current action schemas, validation rules, examples, and output shapes.

Use `{"action":"send","intent":"Notify the user that the background task needs attention","title":"...","body":"..."}` only when an
asynchronous task completed and needs attention, a decision is blocked, or an
important failure happened outside the active conversation. Keep the title and
body concise and include the next action when there is one.

Do not send routine progress updates, successful intermediate steps, or
messages that belong in the active conversation. Do not use Inbox for a
user-only read or archive action; those are dashboard operations, not agent
notifications.

To withdraw an item sent by mistake, use only
`{"action":"recall","intent":"Withdraw the notification sent by mistake","item_id":"opaque-id"}` with the opaque ID returned by
`send`. Never guess or expose storage details for item IDs.

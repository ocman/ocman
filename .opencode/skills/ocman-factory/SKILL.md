---
name: ocman-factory
description: Use when the user explicitly asks to send or hand the current conversation's work to ocman Factory.
---

# Ocman Factory

Include a short, goal-focused `intent` on every ocman MCP call, including help
and read-only calls, so the user sees why you are calling the tool. In Code Mode,
write `intent` as an inline string literal, not a variable or computed value.
This explains the call; it does not grant permission or change the operation.

Use only the `factory` MCP tool. Start with `{"action":"help","intent":"Learn how to hand this work to Factory"}` for the
current action schemas, validation rules, examples, and output shapes.

For any action not shown in the initial help response, request detailed help
before acting. Keep Factory requests to the documented action inputs. Report
domain errors directly; do not infer storage, filesystem, or process details.

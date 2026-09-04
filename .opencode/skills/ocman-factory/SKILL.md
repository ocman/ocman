---
name: ocman-factory
description: Use when the user explicitly asks to send or hand the current conversation's work to ocman Factory.
---

# Ocman Factory

Use only the `factory` MCP tool. Start with `{"action":"help"}` for the
current action schemas, validation rules, examples, and output shapes.

For any action not shown in the initial help response, request detailed help
before acting. Keep Factory requests to the documented action inputs. Report
domain errors directly; do not infer storage, filesystem, or process details.

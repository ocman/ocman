---
name: ocman-routines
description: Use when the user asks to create, inspect, update, run, or delete an ocman routine.
---

# Ocman Routines

Use only the `routines` MCP tool. Start with `{"action":"help"}` for the
current action schemas, validation rules, examples, and output shapes.

Actions: `list`, `get`, `create`, `update` (replaces the whole definition),
`delete` (soft-delete), `run` (run now), and `history` (runs, newest first).
To start a one-off session rather than a saved prompt, use the `sessions`
tool's `create` action instead of a routine.

Keep routine requests to the documented action inputs. Before deleting or
running a routine, identify the target unambiguously and report domain errors
directly.

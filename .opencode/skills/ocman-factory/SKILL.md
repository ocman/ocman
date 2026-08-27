---
name: ocman-factory
description: Use when the user explicitly asks to send or hand the current conversation's work to ocman Factory.
---

# Ocman Factory Handoff

Act only on an explicit request to hand work to Factory. Never infer a handoff
because a plan appears complete.

1. Produce a short goal, an implementation-neutral Markdown brief containing
   objectives, constraints, and accepted decisions, and an absolute local
   project path. Do not include the transcript or conversation identifiers.
2. Call `prepare_factory_work` with those exact values.
3. Present one compact confirmation containing the returned goal, brief,
   canonical project path, and Built-in Formula ID and version.
4. When `acknowledgement_required` is true, also warn that project commands run
   as the local ocman user without process isolation. Ask the user to confirm
   the handoff and acknowledge that warning.
5. Only after explicit confirmation, call `acknowledge_factory_execution` when
   required, then call `create_factory_work_epic` with the unchanged prepared
   key, goal, brief, and canonical project path.
6. If preparation becomes stale, prepare again and obtain confirmation again.
   Reuse the same preparation key when retrying an uncertain creation result.
7. On success, report the Work Epic ID, planning state, and Mission Control
   path. The handoff is complete: stop work in the originating conversation.

If a tool reports that Factory is unavailable, direct the user to Mission
Control for diagnostics. Do not inspect or describe Factory internals.

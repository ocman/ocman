---
title: Idle projects
weight: 16
---

Use **Settings → Idle projects → Stop idle project instances after** to release
resources used by projects you have put away. Enter a whole number of minutes;
**0 means Never**, the default. The maximum is 43,200 minutes (30 days).

An instance becomes eligible when all its normal, top-level sessions are
archived and it has no running sessions, pending user prompts, quota-limited
turns, queued messages, or active routine/Factory work. Completed subagents,
branch-naming helpers, routine-created sessions, and Factory attempt sessions
do not need separate archive actions. A routine targeting an existing user
session does not change that session's archive requirement.

Ocman checks once a minute and stops the instance after those conditions have
held for the configured duration. New activity, reopening a normal session,
and uncertain reads reset the timer. Timers also restart when ocman restarts.
Sessions, transcripts, branches, and worktrees remain available.

This applies to **Claude project supervisors**. OpenCode v1 is excluded because
it cannot atomically reject new turns while draining an idle instance. OpenCode
v2 uses one machine-wide server and is also excluded.
Instances started outside ocman are not stopped. Configure each remote
machine's timeout in that machine's own ocman Settings page.

**Routines wake stopped projects automatically**, whether they create a fresh
session, reuse their first session, or continue a selected existing session.
A future scheduled run does not prevent culling; a claimed or active run does.
Sending to a stopped Claude project also wakes its managed supervisor.

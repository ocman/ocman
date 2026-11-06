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

Each sweep reads one session inventory per platform and groups sessions by
resolved project root. Directory membership is cached only for that sweep.
Immediately before stopping, cleanup reads a fresh inventory, resolves membership
again, and checks live lifecycle, archive intent, queued messages and background
work. The shared preliminary inventory never authorizes a stop on its own.

For deleted checkouts, cleanup uses the project identity stored with managed
session ownership. Retained terminal transcripts from unrelated deleted paths
do not block cleanup. Unreadable ownership or legacy managed records without a
known historical project keep cleanup conservative; resumed legacy sessions
record their project identity.

This applies to **Claude project supervisors**. OpenCode uses one machine-wide
server and is excluded.
Instances started outside ocman are not stopped. Configure each remote
machine's timeout in that machine's own ocman Settings page.

**Routines wake stopped projects automatically**, whether they create a fresh
session, reuse their first session, or continue a selected existing session.
A future scheduled run does not prevent culling; a claimed or active run does.
Sending to a stopped Claude project also wakes its managed supervisor.

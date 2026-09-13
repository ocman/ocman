---
title: Session commits
weight: 8
---

## Session commits

Session Info shows commits that ocman observes in live OpenCode bash results.
Capture is best effort and runs in the headless OpenCode event watcher, so it
does not depend on an open browser or permission judging being enabled.

Ocman records only successful `git commit` summary lines printed by completed
or errored bash calls. It does not scan saved transcripts, poll `HEAD`, install
hooks, resolve full SHAs, or reconcile history after reconnects. Commits made
while ocman is stopped, disconnected, or unable to keep up with the live event
stream can therefore be absent.

The displayed SHA is exactly the abbreviated SHA printed by Git. The branch is
the name printed at commit time, or **Detached HEAD** when Git printed a
detached-HEAD summary. Later branch switches, renames, deletion, amendments,
and worktree removal do not rewrite earlier observations. Amendments appear as
additional observations when Git prints a new commit summary.

Each ocman instance stores observations for its local sessions in its own
`state.db`. A hub reads remote observations from the session owner through the
existing Session Info RPC. It never substitutes rows from the hub database, so
the same session and tool IDs on different machines remain isolated.

The owner's watcher keeps capturing while a hub is disconnected. Session Info
is unavailable through that hub until the owner reconnects, then the owner
returns its persisted observations without scanning transcripts. After
persistence, the owner sends a change notice through the active session event
stream so the browser refreshes Session Info. Capture does not depend on that
stream or an open browser. Older owners that do not report commit-capture
support leave the Commits section hidden.

Select a commit in Session Info to jump to the bash call that produced it.
Ocman opens collapsed output and highlights the exact call, including when one
assistant message contains several tool calls. If the message is older than the
loaded transcript window, ocman fetches that session's history from its owner
only after selection. History reads do not create commit observations.

The recorded commit remains in Session Info if its source message or tool call
was deleted. In that case ocman reports that the source is no longer available
instead of jumping to another call.

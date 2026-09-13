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

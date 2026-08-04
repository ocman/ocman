---
title: Features
weight: 2
sidebar:
  open: true
---

## Sessions

**Session browser** — list, search, archive and replay every session.
Sessions are grouped by project with status indicators and a `+` button to
start new ones. Sessions inactive for three days are archived automatically.

A session's status comes from the agent's own turn lifecycle, so it tracks
the work rather than trailing it: **busy** while a turn runs, **waiting**
when it finishes, **error** when it fails, and **interrupted** when the
agent process stopped mid-turn (killed, crashed, machine rebooted) so the
turn can never complete.

**Live composer** — send messages, answer permission prompts, abort and
compact a running session from the browser. Streaming output renders live.
Plain <kbd>Enter</kbd> sends immediately (mid-turn too); <kbd>Ctrl/⌘+Enter</kbd>
queues the message and it is delivered one-per-turn when the session goes
idle.

**Bash mode** — prefix a message with `!` to run a shell command in the
session's working directory and capture the output in the thread.

**Command palette & slash commands** — <kbd>⌘K</kbd> for jumping between
sessions, settings and actions; `/new [title]`, `/clear`, `/wt`, plus
keyboard-driven rename.

**Diffs & changes** — syntax-highlighted diffs inline in the thread, plus a
*Changes* sidebar that combines session edits with the working-tree `git`
diff.

**Stats dashboard** — per-project metrics, wall-clock totals, token and
pricing graphs, system stats.

**Model picker** — per-platform favourites and a refreshable catalog, so new
models appear without a restart.

## Worktrees & parallel work

`/wt` creates a git worktree under `<repo-parent>/.worktrees/<repo>/<slug>/`
and runs the session in it. One managed OpenCode instance per project serves
every worktree, so parallel sessions get isolated files, rebuilds and staging
areas without an extra process each. Child sessions inherit the parent's
"Allow always" permissions and its live permission mode, so a YOLO parent
doesn't spawn children that stall on prompts.

## Forge integration

PRs and issues from the project's upstream forge (GitHub or Forgejo) appear
in a sidebar pane. Expand a row to read the body, then launch a session in
the project — or check the PR branch out into a fresh worktree — with a
prompt rendered from a template you control in **Settings**.

## Guides

{{< cards >}}
  {{< card link="multi-remote" title="Multi-remote" subtitle="Attach other ocman instances and manage every machine from one dashboard." >}}
  {{< card link="mcp" title="MCP server" subtitle="Let your agent split work into parallel child sessions and worktrees." >}}
  {{< card link="workflows" title="Workflows" subtitle="DAG workflows for migration campaigns: agent, command, approval, map, join." >}}
  {{< card link="scheduled-prompts" title="Scheduled prompts" subtitle="Run a stored project prompt later in a fresh session." >}}
{{< /cards >}}

## Also included

- **Terminals** — in-app browser terminals backed by tmux.
- **Voice input** — local transcription through whisper-cpp, no cloud round-trip.
- **Auth** — optional password gate with rate-limited logins and signed cookies. Off by default on localhost.
- **PWA & desktop** — installable as a Progressive Web App, or run the native macOS build.
- **OpenTelemetry** — optional traces and metrics to any OTLP collector.

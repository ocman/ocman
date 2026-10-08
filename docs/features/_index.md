---
title: Features
weight: 2
sidebar:
  open: true
---

## Sessions

**Session browser.** List, search, archive and replay every session. Sessions
are grouped by project with status indicators and a `+` button to start new
ones. Sessions and projects with no activity for seven days are archived by
default. The delay and auto-archive toggle are available in Settings.
Under Settings → Sessions → **Show archived sessions again**, choose when
new activity restores an archived session to the sidebar: **Session halts**
is the default and waits for done, waiting, error, or interrupted status;
**Any activity** restores it even while it is running.

Use **Filter sessions → Project** to select a project or return to **All projects**.
The selection filters flat and grouped views, including opened and pinned sessions,
and includes the project's worktrees and grouped remote checkouts.
If the selected project disappears, the selector shows **Unavailable project**;
choose **All projects** to clear it.

The session sidebar orders rows by their last completed assistant turn,
including errors, newest first. Streaming output, sending a prompt and reading
a session do not move it. Sessions without a completed turn use creation time;
pinned sessions keep their pin order.

**Status you can trust.** A session's status comes from the agent's own turn
lifecycle rather than a guess about stored messages: **busy** while a turn
runs, **waiting** when it finishes, **error** when it fails, and
**interrupted** when the agent process stopped mid-turn (killed, crashed,
machine rebooted) so the turn can never complete.

An interruption makes the session unread even if you already read its latest
output. The sidebar and project indicator highlight it until you view the
interrupted session in a visible tab. That acknowledgement survives a reload.

**Live composer.** Send messages, answer permission prompts, abort and compact
a running session from the browser. Streaming output renders live. Plain
<kbd>Enter</kbd> sends immediately, mid-turn included.
<kbd>Ctrl/⌘+Enter</kbd> holds the message and delivers it one per turn as the
session goes idle. Changing a session's permission mode takes effect at once:
switching to YOLO answers the prompts already waiting, and any the running
turn or its subagents still raise.

**Bash mode.** Prefix a message with `!` to run a shell command in the
session's working directory and capture the output in the thread.

**Command palette and slash commands.** <kbd>⌘K</kbd> jumps between sessions,
settings and actions. `/new [title]`, `/clear` and `/wt` cover the common
actions, and renaming is keyboard-driven.

**Diffs and changes.** Syntax-highlighted diffs inline in the thread, plus a
*Changes* sidebar that combines session edits with the working-tree `git`
diff.

**Stats dashboard.** Per-project metrics, wall-clock totals, token and pricing
graphs, system stats.

Analytics → Performance shows total waiting time per agent, split into agent
response and tool time. It follows the project, time, agent and model filters.
Parallel tools count once within each request. Tool time includes permission
and question waits; incomplete timings appear as unknown. Totals sum request
durations, so they are not elapsed session time when sessions run in parallel.

Analytics → Activity shows active parallel sessions over time, following the
project and date filters. Each point is the peak simultaneous session count
within its bucket, starting at one hour and widening for longer ranges.
Completed assistant timings reconstruct the history on the local machine.
Overlapping messages in one session count once, subagents count separately,
and tool time counts as active. Idle gaps and unfinished messages are excluded.

**Model picker.** Per-platform favourites and a refreshable catalog, so new
models appear without a restart.

Click the project name in the header to open **Project defaults**. Choose a
default model, agent, and whether new conversations use a new worktree or the
current checkout, then Save. These preferences belong to that project on that
machine and are shared by its sibling worktrees. A composer choice overrides
the default for that conversation. **Use inherited default** clears an override.
Worktrees are used only when the project supports them. The startup model
preference does not change the project's model fallback list.

## Worktrees and parallel work

`/wt` creates a git worktree under `<repo-parent>/.worktrees/<repo>/<slug>/`
and runs the session in it. One managed OpenCode instance per project serves
every worktree, so parallel sessions get isolated files, rebuilds and staging
areas without an extra process each. Child sessions inherit the parent's
"Allow always" permissions and its live permission mode, so a YOLO parent
doesn't spawn children that stall on prompts.

## Forge integration

PRs and issues from the project's upstream forge (GitHub or Forgejo) appear
in a sidebar pane. Expand a row to read the body, then launch a session in
the project, or check the PR branch out into a fresh worktree. The prompt
comes from a template you control in **Settings**.

The selected PRs/Issues tab and each tab's state and Mine filters are saved
in your browser across projects, pane reopening, and page reloads. Controls
stay visible during upstream detection and hide when no supported upstream
is found, without clearing your preferences.

Refreshing keeps existing rows, expanded details, and CI badges visible until
the new results arrive. A failed refresh leaves the list available for retry.

Each PR shows its CI status as soon as its row is visible. A running build is
re-checked every 5 seconds while the row stays on screen. Once every check
has finished, the result is remembered for that commit (also across reloads)
and not fetched again. If CI is re-run on the same commit, press the pane's
refresh button: it forgets the remembered results and checks every visible PR
again.

## Guides

{{< cards >}}
  {{< card link="multi-remote" title="Multi-remote" subtitle="Attach other ocman instances and manage every machine from one dashboard." >}}
  {{< card link="mcp" title="MCP server" subtitle="Factory, Inbox, routine, session inspection, and file display tools." >}}
  {{< card link="routines" title="Routines" subtitle="Save a project prompt, run it now, or schedule it in a fresh session." >}}
  {{< card link="link-previews" title="Link previews" subtitle="Rich cards for forge, Slack, Notion, Linear and Jira links, fetched with this machine's tokens." >}}
  {{< card link="slack" title="Slack integration" subtitle="Run durable ocman conversations from channels and direct chats." >}}
  {{< card link="webhooks" title="Encrypted webhooks" subtitle="Queue provider requests safely while owners or relays are offline." >}}
{{< /cards >}}

## Also included

- **Terminals.** In-app browser terminals backed by tmux.
  They use a separate server, `tmux -L ocman-term`, so they do not appear in
  your default server's session list. To attach outside ocman, run
  `tmux -L ocman-term attach-session -t ocman-term` on the owning machine.
  After upgrading from the shared-server version, reopen terminals in ocman.
  Old shells remain accessible with `tmux -L default attach-session -t ocman-term`
  until you finish and close them; running shells are not migrated or killed.
  Copying in tmux copy mode writes to the browser user's clipboard, including
  for remote terminals. If the browser blocks automatic copying, click
  **Copy to clipboard** in the terminal. This requires tmux's `set-clipboard`
  option to be `on` or `external`, not `off`. Existing custom bindings that
  pipe to host tools such as `pbcopy` still run those tools.
- **Voice input.** Local transcription through whisper-cpp, no cloud round-trip.
- **Auth.** Optional password gate with rate-limited logins and signed cookies. Off by default on localhost.
- **PWA and desktop.** Installable as a Progressive Web App, or run the native macOS build.
- **OpenTelemetry.** Optional traces and metrics to any OTLP collector.

---
title: Claude Code
weight: 2
---

Ocman can show and drive [Claude Code](https://docs.anthropic.com/en/docs/claude-code)
sessions next to, or instead of, OpenCode sessions. Claude sessions use the same
list, transcript view, composer, permission prompts, worktrees, routines,
Factory and multi-remote support as OpenCode sessions.

## Terms of use

Ocman does not talk to Anthropic's model API on your behalf. It runs **your own
`claude` binary**, which signs in with **your own Claude account** (a Claude
subscription or an API key, whatever `claude` is logged in with). Every turn,
judge call and worktree name ocman asks for counts against that account.
**You are responsible for using it within your plan's terms.** Ocman never
reads, stores, copies or refreshes your Claude credentials.

## Setup

1. Install Claude Code and log in once in a terminal (`claude`). Ocman requires
   an installed CLI to drive sessions, but automatic Claude updates are not
   blocked by an exact version pin. Recorded protocol fixtures use **2.1.220**;
   browsing transcripts also works when the CLI is absent.
2. Start ocman. OpenCode and Claude Code are both enabled by default:

   ```sh
   ocman -platforms claudecode            # Claude Code only
   ocman                                # both
   ```

   Without `opencode` in the list ocman does not need OpenCode, its binary or
   its database, and never runs or looks for it. OpenCode-only features are
   hidden: Settings → Maintenance, the reviewer model, the OpenCode MCP config
   prompt and `/restart-opencode`.
3. Pick a model when starting a new conversation: the model list holds every
   platform's models on that machine, each tagged with its platform, and the
   model you pick decides the platform. A model reference names its platform as
   a prefix, `claudecode:anthropic/sonnet`; an unprefixed `provider/model` is
   OpenCode, so existing references keep working. Without a model the
   conversation starts on OpenCode when it is enabled, otherwise on a platform
   the machine runs. Routines, Factory models, plugin conversations (`model`)
   and MCP `sessions create` (`model`) take the same references; `/wt` from a
   session, PR/Issue launches and MCP `sessions create` from a session use that
   session's platform. Project pickers list directories from both platforms.

Ocman reads transcripts from `~/.claude/projects`, or from
`$CLAUDE_CONFIG_DIR/projects` when ocman inherits that variable. Sessions ocman
starts use the `claude` binary and `CLAUDE_CONFIG_DIR` ocman itself was started
with, even though their background supervisor runs under tmux. Restarting ocman
with another `claude` or another `CLAUDE_CONFIG_DIR` replaces each supervisor
once its sessions are idle and no prompt is waiting; until then its permission
and question prompts still reach you and can be answered. A session ocman
started is tied to the `CLAUDE_CONFIG_DIR` it was started under, because its
history lives there: under another configuration it is read-only and sending
to it reports that it is unavailable, and switching back makes it drivable
again.

## Agent SDK compatibility

Ocman's Go integration mirrors Anthropic's Python Agent SDK transport revision
**0.2.166**, using your installed CLI rather than a bundled SDK executable.
Claude children, catalog/account probes, judges and naming helpers receive
`CLAUDE_CODE_ENTRYPOINT=sdk-py` and `CLAUDE_AGENT_SDK_VERSION=0.2.166`.
The CLI therefore generates the Agent SDK client identification and API headers
itself. Ocman removes the inherited `CLAUDECODE` nesting marker and sets `PWD`
to the child's working directory, while preserving the active Claude configuration
and authentication environment.
Existing supervisors from before this transport revision drain and are replaced
once idle, preserving active turns and pending prompts during the upgrade.

Each live child completes an `initialize` control handshake before receiving
its first prompt. Model changes and interrupts wait for the CLI's acknowledgment;
an interrupt preserves inputs Claude reports as still queued. Subagent text is
enabled through initialization. Ocman keeps Claude Code's native system prompt
and user/project settings, corresponding to the SDK's Claude Code preset rather
than its empty system-prompt default.

A result ends one turn, but background tasks can owe another turn. Ocman reads
Claude's session-state events and delegated-task lifecycle before publishing idle.
Older CLIs that emit no session-state events use the task ledger and result frame.
The SDK's `CLAUDE_CODE_PRINT_BG_WAIT_CEILING_MS` bounds the wait between turns to
10 minutes by default; `0` disables that ceiling. A running delegated task or a
permission wait is never cut short by it. Corrupt or oversized stream messages
fail visibly, and process shutdown allows stdin EOF, then SIGTERM, then SIGKILL.

## What you get

- **Automatic session titles.** After the first accepted prompt, ocman runs a
  separate, tool-free Haiku helper using Claude Code's native title prompt,
  recorded from CLI 2.1.289. The helper has no MCP servers and saves no transcript;
  its prompt and reply never appear in your conversation. It uses the separate
  helper budget, so naming does not interrupt your running turn. Explicit titles
  and manual renames win, including a rename while generation is in progress.
  A naming failure keeps the first-prompt title. This runs on the session's owner,
  including remote machines. OpenCode keeps its own native automatic titling.

- **Browsing.** Every Claude session, including ones you started in a terminal,
  is listed with its title, directory, tokens, last activity and status; the
  model shows in the session view and on each reply.
  Subagents appear as children of the tool call that started them, also
  while they run: a live subagent's text, thinking and tool calls stream into
  its own session under the spawning Task call, never into the parent. Archive,
  seen state, pinning and auto-archive work as for OpenCode. Ocman keeps a
  disposable index of your transcripts (`claude-index.db` beside `state.db`);
  delete it and it is rebuilt.
- **Sessions ocman starts.** Send, hold for the next turn (Ctrl/Cmd+Enter),
  abort, rename, fork, compact, slash commands and skills, and attachments.
  Ocman runs `claude` with pipes, at most 4 processes alive at a time per
  machine (a stopping process keeps its place until it has exited); an idle
  process exits after 10 minutes and is resumed on the next message. Short
  one-shot `claude` calls (the command and account probes, the auto-approve
  judge, worktree naming) have their own limit of 2 at a time, so a judge never
  waits behind sessions that wait for its verdict. If the slot directory
  (`claude-slots` beside `state.db`) cannot be used, the turn fails with that
  error instead of waiting. A command
  whose only output is its result text (for example `/cost`) shows that text as
  the reply.
- **Reasoning effort.** The composer offers the effort levels your Claude CLI
  reports for the selected model. Selecting an effort passes `--effort` to Claude;
  selecting default leaves the CLI's configured default in effect. Changing it
  resumes an idle session with the new setting. Messages sent during a running
  turn keep that turn's model and effort.
- **Turn outcome.** A turn ends when every message you sent during it has
  finished: a message sent mid-turn keeps the session busy until Claude has run
  it, and background tasks (agents, workflows, background shells and monitors)
  keep it busy until they end and their continuation finishes, as in Claude
  Code's own session runner. Abort stops them. A failed result, a crashed `claude` or a failed launch ends the turn as
  `error` (a routine run records a failure); an abort ends it without an error,
  including a turn still waiting for one of the 4 process slots, which then
  never starts.
- **Saving state.** If ocman cannot save a permission rule, an "Allow always"
  reply or which sessions it started, the action fails with an error instead of
  pretending to succeed, and an unanswered "Allow always" prompt stays open. If
  the supervisor cannot record a turn's events, the turn ends as `error`; once
  it can write again, it records the turn's end, so queued messages, routines
  and supervisor replacement continue. If Claude already received your answer
  but ocman could not record it, the reply fails; repeating the same answer
  records it, and a different answer is refused because Claude's decision
  stands. An auto-approval Claude received is still recorded as the judge's,
  and an "Allow always" whose outcome ocman could not save is applied once it
  can save again, also after a restart.
- **Usage limits.** Session status follows the turn's outcome: `error` if the
  turn failed, `done` if it completed. A usage limit does not keep a finished
  session running until the quota resets. Usage windows remain available in
  Subscription Usage.

Sessions you started in a terminal can be **continued in ocman**, including
through a hub. Sending the next message adopts the existing conversation and
resumes its history on the owning machine. Opening it launches nothing.
After an upgrade, an older project supervisor drains and is replaced once idle
before it can accept these continuation requests.
Subagent transcripts cannot be continued as independent conversations, and
sessions recorded under another Claude configuration remain read-only.

When the optional hooks report another Claude instance, a small, dismissible
warning appears. It does not block sending or queueing. Detection is best effort:
ocman checks the reported process, or uses a five-minute activity hint when its
PID cannot be identified. The warning is checked when session details refresh;
no warning does not prove another instance is absent. Both instances can modify
the session and its files. Ocman's Stop button and prompt controls only affect
the instance ocman starts, not the original terminal.

## Permissions

Sessions ocman starts run `claude` in its `default` permission mode, and every
tool call the CLI wants approved is asked of ocman. Ocman decides it the same way
as for OpenCode: the session's permission mode (Default, Plan only, Auto-accept
edits, YOLO or custom rules), rules a worktree session inherited from its parent,
"Allow always" replies, then the auto-approve judge or you. Rulesets are stored by
ocman in `~/.local/share/ocman/claude-rules.json`; nothing is written to your
Claude settings. `AskUserQuestion` appears as an ocman question, and prompts from
Claude subagents appear on the session that started them. "Allow always"
approves that exact command, path or URL, not a wider pattern: a `*` or `?` in
it is a literal character (shown as `\*` or `\?` in the prompt), and in your
own custom rules a backslash before `*`, `?` or `\` makes it literal too.
Worktree sessions inherit it with the same scope, including approvals of MCP
tools. Approvals saved by earlier ocman builds, and prompts from a Claude
supervisor started by one, are upgraded to exact approvals automatically. The judge sees your recent messages
in the session. Prompts and their answers arrive on the session's event stream,
so nothing polls for them.

**Your own Claude allow rules apply first.** Rules in `~/.claude/settings.json`,
a project's `.claude/settings.json` or `.claude/settings.local.json` are
evaluated by the CLI before ocman is asked. A tool call they allow never reaches
ocman, so ocman's rules, the judge and your permission mode do not see it.
Remove those rules if ocman should decide.

When no OpenCode instance is reachable, the auto-approve judge runs
`claude -p --model haiku --no-session-persistence --strict-mcp-config` with no
tools. An OpenCode-only ocman never uses Claude for judging.

## Live status for terminal sessions (opt-in)

Without the hooks, a terminal session's status comes from its transcript alone,
which cannot tell a running turn from an abandoned one: a transcript that ends
mid-turn reads as interrupted, however recently it was written. To see real busy/idle status, accept the toast or use
Settings → Provider accounts → **Install**. Ocman then runs
`claude plugin marketplace add ~/.local/share/ocman/claude-marketplace` and
`claude plugin install ocman@ocman --scope user`; it never edits `~/.claude`
itself. **Uninstall** runs `claude plugin uninstall` and removes the marketplace.

The plugin's `SessionStart`, `SessionEnd`, `Stop`, `Notification`, `PreToolUse` and
`PostToolUse` hooks run `ocman hook <event>`, which posts the session id to
ocman's MCP listener (`-mcp-addr`) and gives up after one second, always exiting
0, so Claude is never failed when ocman is not running. Hooks also report a
best-effort Claude process ID for the external-instance warning. The
install is unavailable when `-mcp-addr` is empty. The hook command holds the
ocman binary path and listener address at install time: reinstall after moving
either. Reinstall an older hook plugin to add `SessionEnd`. Hooks from Claude
processes ocman starts are excluded from external-instance detection.

## Usage and subscription

Claude sessions appear in the analytics views with their transcript token
counts. Claude reports no price, so their cost is the token count priced at
public API rates and is labelled **API-equivalent**; a subscription does not
bill it. A Claude-only ocman (`-platforms claudecode`) keeps the analytics
views in `analytics-cache.db` beside `state.db`, without an OpenCode database.
Analytics → Models & Cost also shows the subscription limits described below.

Settings → Provider accounts shows the Claude account's subscription type and
its 5-hour and weekly utilisation. Ocman asks your `claude` binary for both: the
plan comes from the CLI's startup handshake (no prompt is sent, re-checked at
most every 10 minutes), and utilisation from the rate-limit reports the CLI
emits during sessions ocman runs. A window the CLI has not reported, or whose
reset time has passed, shows **not reported yet**; terminal-started sessions
do not contribute. Reports are kept per Claude configuration directory and per
login: after switching `CLAUDE_CONFIG_DIR` or logging another account in, the
previous account's utilisation is not shown. Ocman never reads your Claude login or calls Anthropic
itself. Sign-in stays with the CLI: run `claude` to log in or renew it.

## Limits

- Continuing a terminal-started session does not stop or take over its original
  process. External-instance warnings require hooks and are best effort.
- Sessions ocman launches run in one background supervisor per repository
  (a session on ocman's `tmux -L ocman-agents` server, shared with OpenCode), so a running turn and a pending
  permission survive an ocman restart, including a crash. A reboot ends them;
  the session stays drivable and the next send starts a new supervisor. A
  supervisor left by an incompatible ocman version keeps its waiting
  permission answerable and is replaced once it settles; until then new
  messages to that repository are refused with a retry hint, and auto-approve
  leaves that supervisor's permissions to you when it cannot tell whether an
  approval reached Claude. When ocman
  restarts on another MCP listener address, a running turn finishes on its
  current `claude` process and the next one uses the new address.
- No shell operation: the composer has no `!` shell mode for Claude, so `!cmd`
  is sent to Claude as an ordinary prompt.
- The model picker offers the `opus`, `sonnet` and `haiku` aliases. Slash
  commands are the ones your `claude` reports for the directory (its startup
  handshake, run there with ocman's plugin and cached for 10 minutes). ocman's
  own skills, your `~/.claude/skills` and the project's `.claude/skills` are
  marked as skills, as is any skill a session in that directory reported.
  Claude's composer has no agent selector or agent-cycling shortcut because
  ocman cannot change the main agent of a running Claude process. Claude's
  native `--agent` option selects an agent at startup or resume; subagent
  definitions are not selectable main agents in ocman. Without a working CLI
  the command list is empty and `/name` is still sent as prompt text.
- Forking copies the whole session: `/fork` offers only **Full session**,
  because the CLI has no measured way to fork at a chosen message.
- Revert, unrevert and the session changes view are not available; `/undo`
  and `/redo` are not offered.
- Replies stream token by token for text and thinking; a tool call appears
  once its input is complete.
- Renaming during a running turn shows the new title at once and writes it to
  the transcript after the turn, so ocman never writes while `claude` does.
- One Claude account per machine.
- Measured only through recorded fixtures, not yet checked against a live
  `claude`: the exact usage-limit message (no real limit was hit, so detection
  follows the documented `rejected` rate-limit report), whether the CLI keeps a
  title ocman appended after its own later writes, and when it reports a
  message sent mid-turn as started. There is no automated real-CLI test.
- Known issues, not yet fixed: if writing the end of one turn fails just as
  you send the next message, that new turn can be stopped and shown as an
  error (send it again); an auto-approval whose reply is interrupted (a lost
  response or an ocman crash at that moment) can be shown as yours or lose its
  audit entry; renaming works on a session started under another
  `CLAUDE_CONFIG_DIR`, which should be read-only; and after an upgrade that
  changes ocman's provider protocol, a repository's running supervisor is
  replaced once idle, so new messages there wait until its turns finish.
- Remote machines: see [Multi-remote](multi-remote.md); Claude runs on, and
  signs in as, the machine that owns the session.

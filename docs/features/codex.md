---
title: Codex
weight: 2
---

Ocman can show and drive [Codex CLI](https://github.com/openai/codex) sessions
next to OpenCode and Claude Code. Codex threads use the same session list,
transcript view, composer and permission prompts as the other platforms.

## Terms of use

Ocman does not call OpenAI's API on your behalf. It runs **your own `codex`
binary**, signed in with **your own account** (ChatGPT sign-in or an API key,
whatever `codex` uses). Every turn ocman starts counts against that account.
**You are responsible for using it within your plan's terms.** Ocman never
reads, stores or refreshes your Codex credentials.

## Setup

1. Install the Codex CLI and sign in once in a terminal (`codex login`).
   Recorded protocol fixtures use **0.164.0**.
2. Enable Codex in Settings → Platforms, or for one launch:

   ```sh
   ocman -platforms opencode,claudecode,codex
   ocman -platforms codex                  # Codex only
   ```

   Codex is not enabled by default. Without `opencode` in the list, ocman does
   not need OpenCode, its binary or its database.
3. Pick a Codex model when starting a new conversation. The model list holds
   the models `codex` reports (`model/list`), tagged with their platform; a
   reference names the platform as a prefix, for example
   `codex:openai/gpt-5.5`. The list is cached for ten minutes.

## How it works

Ocman talks to Codex only through `codex app-server`, Codex's own JSON-RPC
interface, rather than its terminal UI. Rollout files supply token accounting
as described below.

- **History.** A read-only `codex app-server` lists and reads the threads in
  `$CODEX_HOME` (default `~/.codex`), archived and subagent threads included.
  Threads started in a terminal or an editor appear too, within about 30
  seconds. Reading a thread never loads it or starts a turn. Token usage is
  the one thing the app-server does not serve: ocman reads each turn's usage
  from the thread's rollout file (`token_usage_record` lines), including the
  zstd-compressed rollouts Codex writes for older threads.
- **Sessions ocman drives.** One background supervisor per machine runs on
  ocman's `tmux -L ocman-agents` server and hosts one `codex app-server` for
  every thread it drives. It starts with the first session operation, not
  with ocman. A running turn and a waiting permission survive an ocman
  restart; a reboot ends them, and the next send starts a new supervisor. If
  the supervisor stops, the next action starts a new one; an action that was
  already on its way to the old one fails and can be repeated.
- **Composer.** Send, image attachments, abort, rename, model and reasoning
  effort. A message sent while a turn runs is added to that turn (Codex's
  `turn/steer`) rather than starting another; Ctrl/Cmd+Enter holds it for the
  next turn instead. Replies, reasoning summaries and command output stream
  as they arrive. File edits show as patches.
- **Slash commands.** `/review` reviews your uncommitted changes, or follows
  the instructions you type after it; `/compact` summarizes the conversation
  to free context. Every enabled skill Codex reports for the session's
  directory is offered too: `/<skill> <prompt>` sends the prompt with that
  skill attached. Compaction and reviews run as turns, so the session shows as
  busy until they finish.
- **Shell (`!`).** `!command` runs the command in the session's directory
  without involving the model, and its output appears in the conversation.
  Codex runs it **unsandboxed with full access**, like a terminal, regardless
  of the session's sandbox.
- **Fork.** `/fork` copies the whole thread, or forks from a prompt you pick:
  the fork keeps the turns before it, so you can take the conversation in
  another direction from there. Forking from the first prompt gives an empty
  conversation in the same directory.
- **Panels and usage.** The session changes panel lists every file Codex
  edited with its diff, and the session info panel shows token totals and how
  full the context was after the latest turn. Analytics counts Codex tokens.
  Settings → Provider accounts shows the Codex plan and its usage windows.
- **Ocman's tools.** When ocman's MCP listener is enabled, Codex sessions ocman
  starts get it as an MCP server named `ocman`.

## Thread ownership conflicts

Codex allows only one process to write a thread at a time, even when it is idle.
Ocman automatically reuses an idle thread already loaded in its current
app-server. If an older ocman supervisor in the same managed runtime inventory
owns it, ocman verifies the thread and Codex home, drains the supervisor only
when it has no active turns or pending prompts, and stops it before retrying.
Retries are bounded and happen only before Codex accepts a turn.

An external, active or unverifiable owner is never stopped. The failed message
shows recovery instructions. Close the thread in the owning Codex terminal or
app, then retry. Ocman never deletes Codex's writer-lock files.

## Permissions

Codex's own approval policy and sandbox, from your `config.toml` and project
trust, decide which actions Codex asks about; ocman does not override them.
Each request Codex makes is decided by ocman like any other platform's: the
session's permission mode, inherited rules, "Allow always" replies, then the
auto-approve reviewer or you. Command approvals match the exact command and
file approvals the exact paths. Rulesets live in
`~/.local/share/ocman/codex-rules.json`; nothing is written to Codex's
configuration. Questions Codex asks (`request_user_input`) appear as ocman
questions. A request Codex withdraws, for example on interrupt, disappears.

The auto-approve reviewer uses a configured reviewer endpoint, a reachable
OpenCode instance, or Claude Code's judge when that platform is enabled. With
none of these, prompts are left to you.

## Limits

- No `/undo` or `/redo`: Codex can drop turns from a thread's history, but it
  does not restore the files those turns changed and cannot redo, so ocman
  does not offer it.
- No agent selector: Codex has no main agents to switch between.
- The session info panel lists no MCP or LSP servers, and Codex reports no
  price, so cost is estimated from token counts where a price is known.
- Usage windows the plan does not have show as "not reported yet".
- One Codex home per machine: ocman reads the `CODEX_HOME` it was started with.
- Measured only through recorded fixtures and fakes. There is no automated
  real-CLI test; re-record fixtures with the tagged recorder described in
  `internal/platforms/codex/provider/testdata/0.164.0/README.md`.

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
2. Install Node.js 22 or newer and the ACP adapter on every machine that will
   execute Codex sessions:

   ```sh
   pnpm add -g @agentclientprotocol/codex-acp@2.5.0
   ```

   `codex-acp` must be on Ocman's PATH. Execution uses ACP automatically; there
   is no separate ACP toggle and no silent native execution fallback. Ocman
   sets `CODEX_PATH` to the same Codex executable used for native history and
   passes the same canonical `CODEX_HOME` to both.
   Selecting Codex in Settings checks the CLI, Node.js version and adapter before
   enabling its toggle. Missing dependencies open a setup modal with a copyable
   command, **Install**, **Check again** and **Close**. Install runs the pinned
   adapter command as Ocman's user on the machine running Ocman; it requires
   Node.js 22+, pnpm and the Codex CLI already installed. Every modal dismissal
   checks again and leaves Codex off unless dependencies are available. Click
   **Save** to apply the selection. Login is separate and is not verified by
   the dependency check. If pnpm has no global bin directory,
   run `pnpm setup`, reopen your terminal and ensure `PNPM_HOME` is on Ocman's
   PATH. Restart Ocman if its PATH changed, then click **Check again**. Nothing
   is installed without clicking **Install**.
3. Enable Codex in Settings → Platforms, or for one launch:

   ```sh
   ocman -platforms opencode,claudecode,codex
   ocman -platforms codex                  # Codex only
   ```

   Codex is not enabled by default. Without `opencode` in the list, ocman does
   not need OpenCode, its binary or its database.
4. Pick a Codex model when starting a new conversation. The model list holds
   the models `codex` reports (`model/list`), tagged with their platform; a
   reference names the platform as a prefix, for example
   `codex:openai/gpt-5.5`. The list is cached for ten minutes.

## How it works

Ocman executes Codex through the
[Codex ACP adapter](https://github.com/agentclientprotocol/codex-acp).
Native `codex app-server` reads and rollout files still supply history,
account metadata and token accounting.

- **History.** A read-only `codex app-server` lists and reads the threads in
  `$CODEX_HOME` (default `~/.codex`), archived and subagent threads included.
  Threads started in a terminal or an editor appear too, within about 30
  seconds. Reading a thread never loads it or starts a turn. Token usage is
  the one thing the app-server does not serve: ocman reads each turn's usage
  from the thread's rollout file (`token_usage_record` lines), including the
  zstd-compressed rollouts Codex writes for older threads.
- **Sessions ocman drives.** One background supervisor per machine runs on
  ocman's `tmux -L ocman-agents` server and hosts one shared `codex-acp` process
  for all threads it drives. The adapter owns the execution app-server; native
  readers never resume its threads for execution. It starts with the first session operation, not
  with ocman. A running turn and a waiting permission survive an ocman
  restart; a reboot ends them, and the next send starts a new supervisor. If
  the supervisor stops, the next action starts a new one; an action that was
  already on its way to the old one fails and can be repeated.
- **Composer.** Send, image attachments, abort, rename, model and reasoning
  effort. A message sent while a turn runs is added to that turn (Codex's
  ACP steering extension) rather than starting another; Ctrl/Cmd+Enter holds it for the
  next turn instead. Replies, reasoning summaries and command output stream
  as they arrive. Native turn reconciliation publishes complete tool payloads
  and accounting before idle. File edits show as patches.
- **Slash commands.** `/review` reviews your uncommitted changes, or follows
  the instructions you type after it; `/compact` summarizes the conversation
  to free context. Every enabled skill Codex reports for the session's
  directory is offered too: `/<skill> <prompt>` sends the prompt with that
  skill attached. Compaction and reviews run as turns, so the session shows as
  busy until they finish.
- **Fork.** `/fork` copies the whole thread through ACP. Forking at a selected
  prompt is unavailable.
- **Panels and usage.** The session changes panel lists every file Codex
  edited with its diff, and the session info panel shows token totals and how
  full the context was after the latest turn. Analytics counts Codex tokens.
  Settings → Provider accounts shows the Codex plan and its usage windows.
- **Ocman's tools.** When ocman's MCP listener is enabled, Codex sessions ocman
  starts get it as an MCP server named `ocman`.

## Thread ownership conflicts

Codex allows only one process to write a thread at a time, even when it is idle.
Ocman reuses sessions already loaded in its ACP connection. ACP does not expose
a native loaded-writer probe, so Ocman does not stop another process or retry a
send through a native execution path. This applies to older Ocman supervisors
too; close their thread before sending it through ACP.

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
- No direct `!` shell execution or fork-at-message through ACP. Raw shell
  requests fail as unsupported; they are never sent to the model as prompts.
- Form questions support strings, choices and booleans. Secret and unsupported
  structured forms are declined rather than guessed or routed to another session.
- ACP notices without native message IDs go to diagnostics rather than creating
  conversation messages that cannot be reconciled with native history.
- The session info panel lists no MCP or LSP servers, and Codex reports no
  price, so cost is estimated from token counts where a price is known.
- Usage windows the plan does not have show as "not reported yet".
- One Codex home per machine: ocman reads the `CODEX_HOME` it was started with.
- Measured only through recorded fixtures and fakes. There is no automated
  real-CLI test; re-record fixtures with the tagged recorder described in
  `internal/platforms/codex/provider/testdata/0.164.0/README.md`.

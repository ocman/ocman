# Multi-Agent Support - Requirements

**Status:** design agreed 2026-10-05; supersedes the 2026 hook-based
Claude Code design that shipped and was removed in `0fe96f0a1`
("feat: drop claude-code platform support"). That commit's parent still
holds a JSONL parser and directory scanner worth salvaging.

## Terminology

- **Platform**: the coding-agent tool that produces a session
  (`platforms.Platform`, `Session.Platform`). OpenCode, Claude Code, later
  Codex.
- **Agent**: a composer-level role inside one session (OpenCode's `build`,
  `plan`, Claude subagents). Never a synonym for platform.
- **Provider**: a platform implemented out of process behind ocman's
  provider protocol. Claude Code is the first provider. OpenCode is not a
  provider; it keeps its native adapter.
- **Supervisor**: the per-project process (`ocman provider claude`) that
  runs inside tmux, owns the `claude` child processes for that project and
  serves the provider protocol on a unix socket.

## Overview

Ocman becomes usable without OpenCode. Claude Code (the `claude` CLI) is
added as a second platform through a generic provider layer, so another
CLI agent (Codex) is a new provider rather than a new adapter.

Ocman runs the user's own installed `claude` binary. The binary owns login,
token refresh and billing; this is how a Claude Pro/Max subscription is
used. Ocman never reads, stores or proxies Claude credentials.

## Goals

1. A user without OpenCode installed can use ocman end to end with Claude
   Code.
2. A project can hold OpenCode and Claude sessions side by side; the
   platform is chosen per conversation.
3. Every ocman entry point that creates or drives sessions supports Claude
   in the first release (see FR-8).
4. Adding the next CLI agent means writing a provider, not touching
   handlers or the frontend.
5. OpenCode users see no regression in behaviour or performance.

## Target users

- Claude-Code-only developers, including subscription users (primary).
- Developers who use both OpenCode and Claude Code.
- Existing OpenCode users (must not regress).

## Functional requirements

### FR-1: Runs without OpenCode

- `-platforms` accepts `opencode`, `claudecode` or both; an OpenCode-less
  start must not run `opencode --version`, require the OpenCode database,
  or fail any create path on a missing `opencode` binary.
- OpenCode-only features (maintenance, v2 views, OpenCode runtime
  controls) are hidden through capabilities, not errors.

### FR-2: Platform choice on every create path

- New conversation composer: platform selector next to the machine
  selector. Default from a setting; OpenCode stays the default when
  installed.
- `/wt`, worktree view, PR/Issue launch, routines, MCP `sessions create`,
  Factory (per Epic), plugin conversations (per installation) carry an
  explicit platform. Nothing infers the platform from a model name.
- Moving an existing session to another platform is out of scope.

### FR-3: Claude session browsing

- Sessions under the active Claude config directory (`~/.claude` or an
  inherited `CLAUDE_CONFIG_DIR`) are listed with title, directory, model,
  tokens, last activity and status, including sessions started from a
  terminal.
- Session detail renders text, thinking, tool calls with results, and
  subagent transcripts as children of their Task call, using the same UI
  as OpenCode sessions.
- Archive, seen state, pinning and auto-archive work as for OpenCode.

### FR-4: Interactive Claude sessions

For sessions ocman launched:

- Send (Enter), hold-for-next-turn (Ctrl/Cmd+Enter), abort, rename, fork,
  compact, slash commands and skills, image and file attachments.
- Permission prompts and `AskUserQuestion` are answered in ocman.
- Worktree sessions get their own working directory.
- A running turn and a pending permission prompt survive an ocman restart.

Sessions started in a terminal are read-only in ocman (live status if the
user opted in to hooks, FR-6).

### FR-5: Permissions and auto-approve

- Ocman's per-session ruleset, "Allow always" inheritance (#101), YOLO and
  custom permission modes apply to Claude sessions with the same semantics
  as OpenCode.
- The auto-approve judge works without OpenCode.
- Documented limitation: allow rules in the user's own Claude settings are
  applied by the CLI before ocman is asked.

### FR-6: Live status for terminal-started sessions (opt-in)

- An opt-in action (toast plus Settings button) installs an ocman Claude
  plugin at user scope through `claude plugin install`; it carries the
  hooks. Removal goes through the CLI as well.
- Hooks never block or fail Claude when ocman is down.
- Without the opt-in, terminal sessions still list, with status derived
  from the transcript.

### FR-7: Subscription limits

- When Claude reports a usage limit mid-turn the session shows a
  `limited` status with the reset time. Queue drains, routines and plugin
  replies treat it as not idle.
- Settings shows the Claude account's subscription type and current 5-hour
  and weekly utilisation.

### FR-8: Feature coverage in the first release

| Feature | Claude support |
|---|---|
| Composer, queue, attachments | yes |
| `/wt`, worktrees view, PR/Issue launch | yes |
| Routines, MCP `sessions` | yes |
| Factory | yes, platform chosen per Epic |
| Plugin conversations | yes, platform per installation |
| Multi-remote | yes |
| Analytics / usage | yes: tokens, API-equivalent cost, limit utilisation |
| Revert / unrevert, session changes | best effort, capability-gated |
| Sharing, artifacts | yes (platform-independent) |
| OpenCode maintenance, v2 views | OpenCode only |

### FR-9: Terms of use

- Docs state that ocman drives the user's own `claude` binary under the
  user's own account and that the user is responsible for their plan's
  terms.

## Non-functional requirements

- **NFR-1 Performance:** with a Claude history of thousands of
  transcripts, the session list is served from an index; a warm start
  does not parse unchanged files. OpenCode list latency does not regress.
- **NFR-2 Memory:** live `claude` processes are pooled with an idle
  timeout and a concurrency cap; numbers come from the spike.
- **NFR-3 Isolation:** caches are per provider instance, never package
  globals.
- **NFR-4 No new runtime dependency:** ocman stays one Go binary; no Node
  sidecar.
- **NFR-5 Compatibility:** a pinned range of `claude` versions is tested;
  outside it ocman shows an explicit error.
- **NFR-6 Tests never run the real `claude`.** A fake executable replays
  recorded fixtures; one optional manual smoke test uses the real CLI.
- **NFR-7 Coverage ratchet** holds for every phase.

## Out of scope

- Moving a session between platforms.
- A `platform.v1` plugin capability (third-party providers). The provider
  protocol stays internal and unversioned-for-outsiders until a second
  provider has validated it.
- Multiple Claude accounts per machine (`claudecode@<name>` instances);
  the platform id is shaped to allow it later.
- Controlling (sending to, aborting, answering prompts in) sessions that
  were started in a terminal.

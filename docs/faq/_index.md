---
title: FAQ
weight: 4
---

## Does ocman replace the OpenCode CLI?

No. It reads the same session database and drives running instances over
their HTTP API, so the CLI and ocman can be used on the same sessions at the
same time.

## Can it corrupt my OpenCode data?

Ocman opens OpenCode's database read-only
(`~/.local/share/opencode/opencode.db` by default). Its own state, meaning
archived flags, favourites, settings and queued messages, lives in a separate
writable database at `~/.local/share/ocman/state.db`. Retained conversations,
search and analytics live in `state_conv.db` beside it. The explicit
[OpenCode maintenance tool](../features/maintenance.md) is the exception to
read-only source access: it modifies the source database only when you start it.

## Why is the composer disabled on some sessions?

For OpenCode, interactive features need a reachable managed HTTP API. Launch
sessions from ocman to get the composer, permission replies and abort. Ocman
does not discover or adopt instances you start yourself, regardless of their
port. OpenCode v2’s registered background service is a supported exception;
see [Configuration](../configuration/#opencode-v2).

Claude Code and Codex use their own managed supervisors. Availability also
depends on the owning machine being connected and the platform’s capabilities.

## Why did several sessions stop together?

Worktree sessions share their project's managed OpenCode server. With OpenCode
v2, all projects on a machine share one server. Replacing that server can
interrupt several turns at once.

Ocman logs failed health checks and restarts at warning level, including the
endpoint and restart reason. A timeout or temporary API error fails the request
without stopping the server. Replacement prepares durable history before stopping
the server and exposes the interruption notices only after a successful stop.
A failed stop keeps the existing instance and aborts replacement. A process that
disappears without saving an error gets a notice when its interrupted history is
opened. Notices stay in
ocman's state database and are restored after refreshes and follow-ups.

Check the warning logs to distinguish a restart from a model-provider failure.
Once the server is available, send a follow-up to continue. Ocman does not replay
an interrupted prompt automatically.

## Do I need tmux?

Saved history can be read without tmux. In-app terminals and managed OpenCode,
Claude Code and Codex launches require it on the owning machine.

## Is it safe to expose on my network?

Not without auth. Ocman binds `127.0.0.1:8228` and serves unauthenticated by
default. Set `OCMAN_AUTH_PASSWORD` before exposing it over a tunnel,
Tailscale, or any non-loopback listener, and set `OCMAN_PUBLIC_BASE_URL` to
the external origin so the Host allowlist accepts it. See
[Authentication](../configuration/#authentication).

## Which platforms are supported?

OpenCode, Claude Code and Codex. Platforms are wired through an adapter interface, so support for
other agents is a new adapter rather than a rewrite.

## Which operating systems?

macOS and Linux. Managed sessions use tmux; Windows is not supported.

## Do sessions pile up forever?

By default, sessions and projects with no activity for seven days are archived
automatically. You can change the delay or disable auto-archive in Settings,
and archive or unarchive anything by hand.

## Does the agent integration send my code anywhere?

The [MCP server](../features/mcp/) is localhost-only and exposes Factory,
routine, session inspection, creation and messaging, and file-embedding tools.
Model traffic uses the account and provider configured for the selected platform.
A custom hosted permission reviewer receives tool input and review context;
see [Reviewer configuration](../configuration/#custom-permission-reviewer).

## Can I run it on several machines?

Yes. One hub attaches to other ocman instances over gRPC and shows every
machine's sessions in one list. It is off by default. See
[Multi-remote](../features/multi-remote/).

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
writable database at `~/.local/share/ocman/state.db`.

## Why is the composer disabled on some sessions?

Interactive features need a reachable OpenCode HTTP API. Sessions from an
OpenCode started without `--port` are readable but not drivable. Launch
sessions from ocman (or start OpenCode with `opencode --port 0`) to get the
composer, permission replies and abort.

## Do I need tmux?

Only for the in-app terminals and for ocman-managed OpenCode instances on the
host. Reading and driving sessions works without it.

## Is it safe to expose on my network?

Not without auth. Ocman binds `127.0.0.1:8228` and serves unauthenticated by
default. Set `OCMAN_AUTH_PASSWORD` before exposing it over a tunnel,
Tailscale, or any non-loopback listener, and set `OCMAN_PUBLIC_BASE_URL` to
the external origin so the Host allowlist accepts it. See
[Authentication](../configuration/#authentication).

## Which platforms are supported?

OpenCode. Platforms are wired through an adapter interface, so support for
other agents is a new adapter rather than a rewrite.

## Which operating systems?

macOS and Linux. Port discovery for externally launched OpenCode instances
uses `lsof`, so Windows is not supported.

## Do sessions pile up forever?

No. Sessions with no activity for three days are archived automatically, and
you can archive or unarchive anything by hand.

## Does the agent integration send my code anywhere?

The [MCP server](../features/mcp/) is localhost-only and exposes workflow
control tools and file embedding. Model traffic stays between OpenCode and
whatever provider you configured there.

## Can I run it on several machines?

Yes. One hub attaches to other ocman instances over gRPC and shows every
machine's sessions in one list. It is off by default. See
[Multi-remote](../features/multi-remote/).

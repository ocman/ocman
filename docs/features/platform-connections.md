---
title: Named platform connections
weight: 18
---

Use named connections to keep separate coding-tool configurations, such as
"Claude Work" and "Claude Personal", on the same machine. Multiple connections
can use the same tool and project without sharing execution state.

## Add a connection

1. Open **Settings** and select the machine that owns the configuration.
2. Choose **Add platform**, select the tool and enter a name.
3. Choose an existing configuration or a separate configuration, when supported.
4. Run the displayed native sign-in command on that machine if sign-in is needed.
   Ocman produces the command for the exact configuration you selected. It does
   not copy credentials or sign in for you.
5. Check the configuration again, then save it. Choose **Make default** if you
   want new conversations to use it when no connection is explicitly selected.

Setup can be resumed after a browser reload. Retrying a check or Save reuses the
same setup receipt; it does not allocate another account directory. Canceling
setup leaves possible native credentials in place.

An account check confirms the native tool's local login and configuration. It
does not guarantee that a model request will succeed or that quota remains.

## Select an account

Open the model picker in a new conversation. Its tabs use the names of the
platforms configured on the chosen machine, such as "Claude Work" and
"Claude Personal". Choose a tab, then a model from that account's catalog.
Unavailable accounts cannot be selected. Model references keep their normal
native tool/provider namespace.

Every created conversation and fork keeps its connection. Changing the default,
renaming an account or running out of quota cannot move that conversation to a
different account. An unavailable selection reports an error instead.

Defaults are explicit. A disabled or removed default remains the saved
preference until you choose another one; Ocman does not silently select a sibling.

## Disable or remove

Disabling prevents new work without deleting credentials or history. Removal
also detaches execution but retains history and routing evidence. Active work
must settle before the change completes. Prompt replies and abort remain
available while draining.

Removing a connection is not a native account logout. Ocman does not delete its
credential store or configuration directory. Adding a similarly named account
does not acquire the removed connection's conversations.

## Supported separate configurations

Isolation support is checked on the owning machine. Unknown versions or
authentication overrides are rejected rather than borrowing another account.

| Tool | Currently verified named-account mode |
| --- | --- |
| Claude Code | 2.1.268+ within the 2.1 series, initialized configuration and subscription login |
| Codex | 0.164.x or 0.165.x, native file-stored ChatGPT login |
| OpenCode | Verified 2.1.x API, saved API key or OAuth credential |

Named Claude sessions use user-only native settings. Authentication helpers and
ambiguous managed/remote policies are unsupported. Named Codex rejects profiles,
custom providers, keyring storage and project `.codex/config.toml` overrides.
Named OpenCode rejects environment/external authentication and uses its own
database, configuration, native server and API authentication.

These are conservative verification gates. A rejected native version may support
isolation upstream but has not been verified by this integration.

## Existing installations and command-line overrides

Existing machine integrations migrate without forcing onboarding. Their native
history/session identities, enabled intent and default preference are preserved.

An explicit `-platforms` argument keeps the legacy command-line composition for
that run. It does not rewrite the saved named collection, and named-connection
onboarding is not offered while that override is active.

Remote setup always targets the selected owner. A disconnected or unsupported
remote never falls back to the local machine's accounts.

See [connection architecture](/docs/other/architecture-platform-connections/)
for identity, persistence and runtime details.

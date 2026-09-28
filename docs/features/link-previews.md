---
title: Link previews
weight: 18
---

Links in a conversation can render as preview cards. Ocman supports GitHub,
Forgejo, GitLab, Slack, Notion, Linear and Jira Cloud. Every card is resolved
on the owner machine for **one browser**. A preview shows only what that
viewer's own provider account can see.

## Fallback link or rich card

A recognized link always renders in one of two ways:

- **Clickable fallback link.** The resource ID (for example `ABC-42`) links to
  the original URL. Ocman fetched nothing, so the card shows no title, status or
  author. You see this when the viewer has not connected the provider, the
  grant expired or was revoked, or the resource is private, missing or rate
  limited. Where the viewer can fix it, a **Connect** or **Reconnect** button
  appears beside the link.
- **Authenticated rich card.** Title, status, author or assignee, and last
  update, fetched with the viewer's own grant (or, for public forge
  repositories, with no login). Cards are kept in memory only and are dropped
  when the viewer connects, disconnects or signs out.

A custom link rule without a provider always produces a fallback-style card
that points at its replacement URL.

## Supported links

| Provider | Links | Ticket IDs via a link rule |
| --- | --- | --- |
| GitHub | `https://github.com/<owner>/<repo>/pull/<n>`, `/issues/<n>`, `/commit/<sha>` | — |
| Forgejo | `https://<host>/<owner>/<repo>/pulls/<n>`, `/issues/<n>`, `/commit/<sha>` on an allowlisted host | — |
| GitLab | `https://<host>/<group>/<project>/-/merge_requests/<n>`, `/-/issues/<n>`, `/-/commit/<sha>`. Nested groups are supported. | — |
| Slack | `https://<workspace>.slack.com/archives/<channel>/p<ts>`, and thread replies with `?thread_ts=` | — |
| Notion | Pages on `notion.so`, `notion.com`, `app.notion.com` and `*.notion.site`, including `?p=<page>` peeks | Page title search |
| Linear | `https://linear.app/<workspace>/issue/<KEY-n>[/slug]` | Issue identifier |
| Jira Cloud | `https://<site>.atlassian.net/browse/<KEY-n>` | Issue key, searched on every connected site |

Only `https` links on the exact hosts listed here or in your configuration are
recognized. A link on any other host stays plain text, and ocman never
contacts that host. One message previews at most 20 resources.

### Ticket IDs

A custom link rule under **Settings → Link previews → Custom link rules** turns
text such as `ABC-42` into a link. Give the rule a `provider` (`notion`,
`linear` or `jira`) and ocman resolves each match with the viewer's grant
instead. The rule's own URL remains the fallback link. The settings form edits
the pattern and link. Set `provider` through the same API it uses:

```sh
curl -X POST http://127.0.0.1:8228/api/settings/link-preview-rules \
  -H 'Content-Type: application/json' \
  -d '{"rules":[{"pattern":"ABC-\\d+","replacement":"https://tracker.example.com/browse/$&","provider":"jira"}]}'
```

When several resources match (two Jira sites, several Notion pages), the card
lists them so you can choose. When nothing matches, only the fallback link
shows.

## Setup

Every provider uses the same redirect URI:

```text
<public base URL>/api/previews/oauth/callback
```

Set the public base URL with `-public-base-url` or `OCMAN_PUBLIC_BASE_URL`, for
example `https://ocman.example.ts.net`. Without it the redirect URI falls back
to the listen address. That only works when the browser runs on the same
machine. Register the URI exactly, including the scheme and any port.

Add each app under **Settings → Link previews → Add a sign-in app**, which also
shows the redirect URI to copy. Or set environment variables on the machine
running ocman and restart it. A saved app overrides the environment's app for
the same provider (and host). **Use environment** on a saved app removes it and
falls back to the environment's. Saved client secrets are sealed in `state.db`
and are never shown again: leave the secret blank when editing to keep it.
Changes apply immediately, with no restart.

| Provider | Register | Scopes or permissions | Environment |
| --- | --- | --- | --- |
| GitHub | A GitHub App with expiring user tokens | Read-only Metadata, Pull requests, Issues, Contents | `OCMAN_GITHUB_PREVIEW_CLIENT_ID`, `OCMAN_GITHUB_PREVIEW_CLIENT_SECRET` |
| Forgejo | One OAuth2 application per host | None: Forgejo has no granular scopes | `OCMAN_FORGEJO_PREVIEW_APPS=host=client_id:client_secret[,…]` |
| GitLab | One OAuth application per host, `gitlab.com` or self-managed | `read_api` | `OCMAN_GITLAB_PREVIEW_APPS=host=client_id[:client_secret][,…]` |
| Slack | A dedicated Slack app, not the conversation plugin's app | User token scopes `channels:history`, `channels:read`, `users:read` | `OCMAN_SLACK_PREVIEW_CLIENT_ID`, `OCMAN_SLACK_PREVIEW_CLIENT_SECRET` |
| Notion | A public connection | Read content only | `OCMAN_NOTION_PREVIEW_CLIENT_ID`, `OCMAN_NOTION_PREVIEW_CLIENT_SECRET` |
| Linear | An OAuth2 application | `read`, with PKCE (the secret is optional) | `OCMAN_LINEAR_PREVIEW_CLIENT_ID`, `OCMAN_LINEAR_PREVIEW_CLIENT_SECRET` |
| Jira Cloud | An Atlassian OAuth 2.0 (3LO) app | `read:jira-work`, `read:me`; ocman adds `offline_access` | `OCMAN_JIRA_PREVIEW_CLIENT_ID`, `OCMAN_JIRA_PREVIEW_CLIENT_SECRET` |

The [configuration reference](../../configuration/#environment-variables)
covers each variable in detail, and
[forge link previews](../../configuration/#forge-link-previews-and-the-owner-wide-token)
explains the owner-token fallback.

### Host configuration

- **GitHub and Forgejo previews of public repositories need no app.** GitHub
  links, and links on the https hosts from your `tea` logins, preview public
  repositories with the owner machine's token. The viewer app only adds
  private repositories. Settings lists these forges as using this machine's
  token.
- **Forgejo and GitLab hosts are exact.** Add one app per host (with a port if
  it has one), in Settings or in its `*_PREVIEW_APPS` variable. Only https is
  supported.
- **GitLab refuses loopback and link-local addresses**, and private addresses
  for `gitlab.com`. A self-managed GitLab on `localhost` cannot be previewed.
- **Jira always goes through `api.atlassian.com`.** Site links preview only for
  sites the viewer picked at consent.

## Connecting

Each viewer connects their own accounts, either from **Connect** on a card or
from **Settings → Link previews**. The provider's consent screen opens, then
redirects back to the page you started from. Settings lists each connection by
account and workspace name. From there you can connect another workspace or
disconnect one.

Access tokens that expire are refreshed on the server before use. When a
refresh fails or the provider rejects a token, ocman forgets that grant and the
card offers **Connect** again. A grant without a refresh token shows
**Reconnect** once it expires. **Disconnect** deletes the grant and revokes it
at the provider when the provider has a revocation endpoint (Slack, Notion,
Linear, GitLab). GitHub, Forgejo and Jira grants must also be removed from
your account settings at the provider.

## Privacy boundaries

- **Viewer, not password.** A shared ocman password does not identify a
  person, so grants belong to a browser: a random `HttpOnly` cookie. Another
  browser using the same password sees only fallback links. Signing out
  deletes the viewer and all of its grants.
- **Tokens stay on the server.** Tokens are sealed at rest in `state.db`. The
  browser receives only display names and preview fields, never tokens or
  client secrets.
- **Private previews need app access.** With authentication off, only direct
  loopback requests can connect providers or see private previews. Proxied and
  remote requests get public forge cards at most. With authentication on, a
  valid login cookie is required.
- **One owner per grant.** In a multi-remote setup the hub holds viewer grants,
  keyed by the owner machine of the conversation. A grant made for one machine
  never answers for another. A disconnected remote fails closed and its cached
  previews are dropped.
- **Private stays private.** Slack private channels and DMs always show as
  denied. Notion previews only the pages chosen at consent. The owner machine's
  forge token previews private repositories only for a direct, un-proxied
  request from that machine itself.
- **Fixed hosts.** Preview requests go only to each provider's API hosts. They
  never follow redirects, and link text is never fetched as a URL.

## Testing

`internal/linkpreview/lifecycle_test.go` runs every provider through connect,
callback, preview, token refresh, restart, expiry, provider-side revocation
and disconnect against mock provider APIs, each with a disposable state
database. `frontend/e2e/link-previews.spec.ts` covers the browser flow: the
fallback link, Connect, the returned rich card, reload, and disconnect.

Mocks do not prove a live provider accepts the app. Before relying on a
provider, run this checklist against the real service with a test account:

1. Register the app with the exact redirect URI and scopes above, set the
   variables, restart ocman and confirm the provider appears under
   **Settings → Link previews**.
2. Post a link to a resource only your test account can see. Confirm the
   fallback link and **Connect** button.
3. Click **Connect**, approve consent, and confirm you land back on the
   conversation with a rich card.
4. Reload the page and restart ocman. The card must still render.
5. Open the same conversation in a private window through the public base
   URL, not loopback. Only the fallback link may show. (A direct loopback
   request is the machine's own user and may see private forge repositories
   through the owner token.)
6. For providers with expiring tokens (GitHub App, GitLab, Slack with token
   rotation, Linear, Jira), wait past the expiry or shorten it in the app
   settings. The card must still render after an automatic refresh.
7. Revoke the app from your account at the provider, then wait a minute for
   the cache to expire. The card must fall back to the link with **Connect**.
8. Reconnect, then **Disconnect** in Settings. The card must fall back to the
   link, and for Slack, Notion, Linear and GitLab the grant must disappear from
   the provider's authorized apps.
9. For GitHub, Forgejo and GitLab, confirm a public repository link still
   previews in that private window with nothing connected.

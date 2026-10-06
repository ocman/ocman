---
title: Link previews
weight: 18
---

Links in a conversation can render as preview cards. Ocman supports GitHub,
Forgejo, GitLab, Slack, Notion, Linear and Jira Cloud. Previews are fetched on
this machine with this machine's credentials: your CLI logins, tokens you paste
in Settings, or a sign-in. The browser only learns which providers are set up
and which link hosts they cover, so text that cannot produce a preview is never
sent to the server.

## Fallback link or rich card

A recognized link renders in one of two ways:

- **Clickable fallback link.** The resource ID (for example `ABC-42`) links to
  the original URL, with no title or status. You see this when the provider has
  no token yet, the token expired or was rejected, or the resource is private,
  missing or rate limited. A private resource says to add a token under
  **Settings → Link previews**.
- **Rich card.** Title, status, author or assignee, and last update. Cards are
  kept in memory for a minute and dropped whenever a token or sign-in changes.

A custom link rule without a provider always produces a fallback-style card
that points at its replacement URL.

GitHub and Forgejo PR cards also show CI status for the PR's head commit.
They share the sidebar's repository-and-SHA cache, so either view can reuse a
finished result fetched by the other. Visible cards check every 5 seconds
until every check finishes. Rate-limited results never become final cached
results. The sidebar's refresh button clears this shared cache and refreshes
visible conversation cards too. Refresh reloads PR metadata first, so checks
follow the current head commit after a new push.

GitHub tokens and Apps need read-only Checks access to fetch check runs.
Cards distinguish unavailable checks from a commit with no CI status.

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
`linear` or `jira`) and ocman resolves each match with that provider's token
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

Most providers need one personal token, pasted once under **Settings → Link
previews → Providers**. Ocman checks the token with the provider, then seals it
in `state.db`; it is never shown again. Any browser you use with ocman gets the
same previews.

| Provider | Without setup | Token |
| --- | --- | --- |
| GitHub | Public repositories, or everything your `gh` login / `GITHUB_TOKEN` / `GH_TOKEN` can see | A fine-grained personal access token with read-only Contents, Issues and Pull requests |
| Forgejo | Everything your `tea` login (https hosts) or `FORGEJO_TOKEN` / `GITEA_TOKEN` can see | An access token with read access to repositories and issues. Add other hosts under **Add a host** |
| GitLab | Public projects on `gitlab.com` | A personal access token with `read_api`. Add self-managed hosts under **Add a host** |
| Linear | Nothing | A personal API key, one per workspace |
| Notion | Nothing | An internal integration secret. Share the pages to preview with the integration |
| Slack, Jira Cloud | Nothing | No personal token: register a sign-in app (below) and click **Sign in** |

With several tokens for one provider (two Linear workspaces), ocman tries each
and shows the first that can see the resource.

### Sign-in apps

Slack and Jira have no usable personal token, so they need an OAuth app. The
other providers accept one too, but a token is simpler. Add an app under
**Sign-in apps**, which shows the redirect URI to register:

```text
<public base URL>/api/previews/oauth/callback
```

Set the public base URL with `-public-base-url` or `OCMAN_PUBLIC_BASE_URL`, for
example `https://ocman.example.ts.net`. Without it the redirect URI falls back
to the listen address, which only works when the browser runs on the same
machine. Apps can also come from environment variables; a saved app overrides
the environment's for the same provider and host. Client secrets are sealed and
write-only.

| Provider | Register | Scopes or permissions | Environment |
| --- | --- | --- | --- |
| Slack | A dedicated Slack app, not the conversation plugin's app | User token scopes `channels:history`, `channels:read`, `users:read` | `OCMAN_SLACK_PREVIEW_CLIENT_ID`, `OCMAN_SLACK_PREVIEW_CLIENT_SECRET` |
| Jira Cloud | An Atlassian OAuth 2.0 (3LO) app | `read:jira-work`, `read:me`; ocman adds `offline_access` | `OCMAN_JIRA_PREVIEW_CLIENT_ID`, `OCMAN_JIRA_PREVIEW_CLIENT_SECRET` |
| GitHub | A GitHub App with expiring user tokens | Read-only Metadata, Pull requests, Issues, Contents | `OCMAN_GITHUB_PREVIEW_CLIENT_ID`, `OCMAN_GITHUB_PREVIEW_CLIENT_SECRET` |
| Forgejo | One OAuth2 application per host | None: Forgejo has no granular scopes | `OCMAN_FORGEJO_PREVIEW_APPS=host=client_id:client_secret[,…]` |
| GitLab | One OAuth application per host | `read_api` | `OCMAN_GITLAB_PREVIEW_APPS=host=client_id[:client_secret][,…]` |
| Notion | A public connection | Read content only | `OCMAN_NOTION_PREVIEW_CLIENT_ID`, `OCMAN_NOTION_PREVIEW_CLIENT_SECRET` |
| Linear | An OAuth2 application | `read`, with PKCE (the secret is optional) | `OCMAN_LINEAR_PREVIEW_CLIENT_ID`, `OCMAN_LINEAR_PREVIEW_CLIENT_SECRET` |

### Host configuration

- **Forgejo and GitLab hosts are exact.** A host is previewed once it has a
  token, a `tea` login or an app. Only https is supported.
- **GitLab refuses loopback and link-local addresses**, and private addresses
  for `gitlab.com`. A self-managed GitLab on `localhost` cannot be previewed.
- **Jira always goes through `api.atlassian.com`.** Site links preview only for
  sites picked at sign-in.

## Tokens and sign-ins

Sign-in tokens that expire are refreshed on the server before use. When the
provider rejects a token (401), ocman forgets it and the provider shows as not
set up again. **Remove** deletes a token or sign-in; sign-ins are also revoked
at the provider when it has a revocation endpoint (Slack, Notion, Linear,
GitLab). Revoke personal tokens at the provider yourself.

## Privacy boundaries

- **The machine, not the browser.** Ocman is a personal tool: tokens and
  sign-ins belong to the machine, and every request with app access uses them.
  Signing out ends the browser session and keeps them.
- **Tokens stay on the server.** They are sealed at rest in `state.db`. The
  browser receives provider names, link hosts, account names and preview
  fields, never tokens or client secrets.
- **App access is required.** With authentication on, a valid login cookie is
  needed. With it off, only direct loopback requests count. A request without
  app access gets public forge cards at most and cannot manage tokens.
- **One hub.** In a multi-remote setup the hub resolves every preview with its
  own credentials, including for a remote's sessions. A disconnected remote
  fails closed.
- **Private stays private at the provider.** Slack private channels and DMs
  always show as denied. Notion previews only pages shared with the integration
  or chosen at sign-in.
- **Fixed hosts.** Preview requests go only to each provider's API hosts. They
  never follow redirects, and link text is never fetched as a URL.

## Testing

`internal/linkpreview/lifecycle_test.go` runs every provider through sign-in,
callback, preview, token refresh, restart, expiry, provider-side revocation
and removal against mock provider APIs. `internal/server/preview_catalog_test.go`
covers the provider catalog and token saving, including adding a Forgejo host.
`frontend/e2e/link-previews.spec.ts` covers the browser flow: a private link,
adding a token in Settings, the rich card, reload and removal.

Mocks do not prove a live provider accepts a token. Before relying on a
provider, check it against the real service:

1. Paste a token under **Settings → Link previews** and confirm the account
   name appears.
2. Post a link to a private resource the token can see and confirm the rich
   card. Reload and restart ocman; it must still render.
3. Open the conversation from another browser with your password. The card
   must render there too.
4. Revoke the token at the provider and wait a minute. The card must fall back
   to the link, and the provider must show as not set up.
5. For Slack or Jira, register the app, **Sign in**, and repeat steps 2–4.

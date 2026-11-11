---
title: Encrypted Webhooks
weight: 5
---

An ocman owner can register an inbox with an `ocman-relay` and expose its
ingestion URL to a provider. The relay accepts requests while the owner is
offline. It stores only age ciphertext and visible size/timing metadata; the
owner later decrypts and queues the request in its local `state.db`.

## Format And Boundaries

The relay encrypts a versioned JSON envelope with age X25519. The recipient is
an `age1...` X25519 public key and the owner keeps the matching
`AGE-SECRET-KEY-` identity in `state.db`. `formatVersion` and `keyVersion` are
required, so a future format can be rejected rather than guessed.

The envelope contains the plaintext body at ingestion, request method, query,
filtered headers, delivery identity, and `receivedAt` milliseconds. Sensitive
headers such as `Authorization`, cookies, and the configured shared-secret
header are excluded. Relay logs, storage metadata, and telemetry must never
contain the body, credentials, age identity, or private keys. Object sizes and
timestamps are intentionally visible to the relay operator.

The relay persists inbox metadata and ciphertext deliveries. The owner
persists the private identity, relay credentials, accepted delivery, inbox
item, retry state, and routine-dispatch claim in its own `state.db`; these are
separate persistence boundaries. Registration returns `202 Accepted` only
after ciphertext is durably stored. The owner acknowledges a delivery only
after decryption, local acceptance, and all matching routine dispatch claims
have been committed. A lost acknowledgment is safe: the next poll finds the
durable delivery record and cannot schedule the same routine occurrence twice.

## Operations

### Connect an owner

Under **Settings → Webhooks**, set the relay URL and select **Connect relay**,
then **Sign in to relay**. The relay lists its configured login providers.
Sign in and explicitly approve the connection, then return to ocman. The
backend retrieves and saves a user-bound enrollment token. Neither the polling
credential nor the enrollment token is returned to the dashboard.

The connection request expires after ten minutes. Enrollment credentials expire
after 90 days; reconnect to obtain a new credential. Existing inbox credentials
are independent, so reconnecting does not reset inbox keys or interrupt polling.
Changing the saved relay URL clears the old enrollment token. SSO credentials
are bound to the relay origin to prevent sending them to a different relay.
**New webhook inbox** also offers the connection flow when no token is saved.

For a relay without SSO, use **Manual enrollment** and paste the token supplied
by its operator. See [relay sign-in configuration](#relay-sign-in-configuration).

### Relay sign-in configuration

Set `OCMAN_RELAY_PUBLIC_URL` to the relay's public HTTPS origin and
`OCMAN_RELAY_SSO_CONFIG` to a JSON file containing the enabled providers:

```json
[
  {
    "id": "github",
    "name": "GitHub",
    "kind": "github",
    "clientId": "your-github-client-id",
    "clientSecretEnv": "RELAY_GITHUB_CLIENT_SECRET"
  },
  {
    "id": "google",
    "name": "Google",
    "kind": "google",
    "clientId": "your-google-client-id",
    "clientSecretEnv": "RELAY_GOOGLE_CLIENT_SECRET"
  }
]
```

Populate the referenced secret environment variables on the relay. Register
`<public origin>/auth/callback/github` in the GitHub OAuth app and
`<public origin>/auth/callback/google` in the Google web OAuth client.
The callback suffix is the configured provider `id`, not its `kind`. A relay
may configure multiple providers of either supported kind. Keep IDs stable;
account identity is the provider ID plus its stable user ID. Accounts from
different providers are not linked by email.

SSO uses authorization codes with PKCE, browser-bound state, and an explicit
approval form. Provider access tokens are discarded after reading the identity.
Only hashed enrollment credentials and hashed account identities persist in the
relay's existing disk store. Each approval issues an independent credential,
so connecting another installation does not invalidate an earlier one.
An authenticated `DELETE /auth/enrollment` revokes the presented enrollment
credential without deleting existing inboxes.

Run one relay process per store. Pending sign-ins are bounded in memory and
are lost on restart; users can connect again. Sign-in routes have per-client
rate limits, and inbox registration uses the relay's creation rate limits.
Set `-trust-proxy` only behind a proxy that overwrites forwarded headers.
HTTP origins are allowed only for loopback development. Provider applications
and secrets must be configured before enabling hosted sign-in; this does not
provision them or choose a default public relay URL.

Without `OCMAN_RELAY_SSO_CONFIG`, SSO routes are disabled. The existing
`OCMAN_RELAY_INBOX_ENROLLMENT_TOKEN` remains an optional manual alternative
even when SSO is enabled.

Configure relay enrollment and limits before registration. Body size, retained
header size, pending delivery count/bytes, ingest rate, inbox TTL, and relay
storage TTL are enforced. Expiry and revocation delete the relay metadata and
deliveries. A malformed or undecryptable delivery is recorded with bounded
backoff and does not block later deliveries; it remains available for retry or
operator investigation until expiry.

Key rotation changes the recipient and increments `keyVersion`. Existing
pending ciphertext remains decryptable only with the old identity if it is
retained. Resetting or losing the private identity makes those deliveries
unreadable; revoke and register a new inbox when recovery is not possible.

The UI exposes only the ingestion URL, key version, owner, and aggregate
delivery states. Management, fetch, acknowledgment, enrollment, and private
identity credentials stay server-side. An inbox, its polling, and the routines
it triggers all stay on the machine that owns the inbox.

## Non-goals

- Provider signature verification or challenge/handshake protocols.
- Provider event deduplication beyond the relay delivery identity.
- Arbitrary user validators, scripts, or regular expressions.
- Exactly-once external side effects. Local routine scheduling is durable and
  deduplicated, but an agent or provider side effect can still be retried.

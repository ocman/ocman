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
identity credentials stay server-side. Local and remote owners use the same
owner API; the hub routes registration and polling to the owning ocman.

## Non-goals

- Provider signature verification or challenge/handshake protocols.
- Provider event deduplication beyond the relay delivery identity.
- Arbitrary user validators, scripts, or regular expressions.
- Exactly-once external side effects. Local routine scheduling is durable and
  deduplicated, but an agent or provider side effect can still be retried.

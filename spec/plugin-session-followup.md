# Plugin session follow-ups

The existing session 1.4 API starts one managed session and delivers completed
turns. Re-running starts another session. Conversation APIs address separately
configured external conversations, not a plugin-owned managed session. Neither
approach can continue an existing session while retaining its pinned permissions
and context.

Session 1.5 adds a generic `send` operation gated by `session.send` and the
existing session grants. Parameters are `after`, the opaque operation ID from a
completed call, and `prompt`. The host resolves the session from its durable
completion outbox, checks plugin ownership and current grants, and queues a new
turn without changing the model, agent, directory or permission rules.

Only the latest completed turn of an idle session can be continued. A durable
receipt fences one send per completed turn across retries and restarts. A failed
or uncertain enqueue is never blindly replayed. Completed calls retain their
existing stable operation IDs, allowing plugins to store successive completions
without replacing or confusing earlier turns. Usage remains per completed turn;
the caller accounts for every turn it requests.

Examples are a document indexer asking for a different output representation and
a dependency inventory asking the same session to clarify an ambiguous manifest.
The host does not interpret prompts, JSON or domain schemas. A caller decides
whether to follow up and how many attempts its workflow permits.

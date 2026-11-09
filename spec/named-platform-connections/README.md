# Named platform connections

## Delivery status

The first implementation checkpoint is a UI review, requested before completing
the feature. `PlatformConnections` and `PlatformConnectionDialog` are reusable
production components, but are **not registered in Settings or onboarding yet**.
Their callbacks currently run only in unit tests and the browser review fixture.
No connection persistence, native authentication, runtime isolation or routing
has changed. Do not describe this checkpoint as working multi-account support.

Run the isolated browser review:

```sh
cd frontend
pnpm exec playwright test -c playwright.connections.config.ts
```

It renders the real components with fixture accounts, without a backend or agent
process. Screenshots go to `.dev/platform-connections-review/` at the repository
root. The fixture is not an entry in the production bundle. Component unit tests
live in `frontend/src/components/PlatformConnections.test.tsx`.

## User experience

- Settings manages a collection of named connections, not platform-type toggles.
  Multiple entries can have the same kind: OpenCode, Claude Code or Codex.
- Each connection has an immutable opaque ID. Renaming is presentation-only.
- Add platform selects a kind, custom name and either an existing configuration
  or a new isolated configuration. Isolation is offered only if the owner reports
  support; account isolation must be verified before enabling that capability.
- Setup reports dependency and account checks. Missing sign-in can be saved for
  later but cannot authorize session creation. Native tools retain credentials.
- Defaults select a connection, never just a platform type. Disabled/unready
  connections cannot become new-session defaults.
- Each named connection has a direct Disable/Enable action. Disabling prevents
  new sessions without removing history or credentials or disabling siblings of
  the same kind. A disabled default retains its preference and is labelled
  "Default (disabled)"; it never silently selects another account. Re-enabling
  restores availability only when the owner also verifies setup readiness.
- Existing sessions keep their original source and connection. No automatic
  switch to another account on quota exhaustion, disconnection or removal.
- Removal keeps history and native credentials, and must drain active work
  before detaching execution. It is not an account logout or directory deletion.
- First-run onboarding reuses exactly the same Add platform dialog, allows
  setup later, and offers project selection once a connection is ready.

The UI receives platform setup metadata from the owner and does not branch on
platform identities. Machine paths and login commands must be produced and
shell-quoted on the owning host, never synthesized by the browser. Sign-in
commands displayed in the browser fixture are examples, not executable setup.

## Backend implementation still required

1. Persist the connection collection and onboarding completion on the owner.
   Distinguish an explicitly empty collection from missing legacy configuration.
   Migrate existing enabled platforms without forcing existing users through
   onboarding. Detect a new installation before startup creates its state.
2. Separate platform kind, connection routing ID and physical source identity.
   Keep existing source keys and canonical session IDs stable. Existing default
   connections need compatibility mappings for platform-keyed settings and
   references; do not reimport history under a new platform identity.
3. Refactor integration constructors to accept explicit connection configuration.
   Never change process-global environment variables while constructing adapters.
   Audit native environment inheritance so an API token cannot override the
   intended subscription account silently.
4. Scope supervisors, runtime records, rules, launch files, sockets, spools,
   discovery and recovery to connections. Claude currently persists supervisors
   by `(provider, repo_root)`; Codex currently has one machine supervisor root.
   These keys must not collide for two accounts in the same repository.
5. Preserve the canonical wrapper's optional capabilities and source checks.
   A shallow interface wrapper that loses optional interfaces is not sufficient.
   Browsing must remain source-scoped: the retained-history fallback must not
   expose another connection's source as executable.
6. Carry named connections through owner Host operations, remote inventory,
   capabilities, session targets, drafts, defaults, routines, Factory, MCP,
   helper/title generation, usage and cooldowns. Explicit unavailable owners or
   connections fail closed; never fall back to a similarly named connection.
7. Add owner-local setup inspection and resumable setup identity. A check that
   prepares an isolated directory needs a durable reservation/receipt so Save,
   Check again and the displayed login command all use the **same** directory.
   The browser's account/status observations are never mutation authority.
8. Replace `PlatformSettings` and the OpenCode-only getting-started empty state
   with an API controller around the approved components, then complete full-app
   end-to-end tests. Keep loading, failed requests and disconnected owners
   distinct from a successful empty connection list.

### Platform-specific isolation

- **Claude Code:** explicitly scoped `CLAUDE_CONFIG_DIR`, child and helper
  environments, indexing, plugin installation and per-project supervisors.
  Verify effective credential isolation, including OS credential stores.
- **Codex:** explicit `CODEX_HOME` for both execution and the read-only
  app-server, with separate supervisor roots and recovery records.
- **OpenCode:** isolated configuration, authentication, data/database and server
  per connection. Existing-service adoption remains only for the connection
  matching that service's database and credentials. A separate database alone
  does not isolate credentials; a new connection must not borrow the registered
  default service. Read the OpenCode skill before changing its runtime behavior.

## Completion gates

After UI approval: implement the backend and application integration; add
regression tests for source collisions and wrong-account fallback; test two
same-kind connections concurrently and across restart with fake agents and
disposable databases; verify remote inventory and legacy migration; run relevant
Go tests, full frontend coverage, application Playwright tests and `make lint`.
Compare coverage against the baseline without lowering it. Update architecture
documentation when the runtime/owner/browser flow changes. Then open a Forgejo
PR using `tea`, link it to the session, inspect CI/review state and activate the
requested persistent PR watch. Do not merge without authorization.

# Named platform connections

## Delivery status

The first implementation checkpoint is a UI review, requested before completing
the feature. `PlatformConnections` and `PlatformConnectionDialog` are reusable
production components, but are **not registered in Settings or onboarding yet**.
Their callbacks currently run only in unit tests and the browser review fixture.
The next backend checkpoint adds `internal/platformconnections`, a tested
owner-local repository with its schema registered in `server.ModuleSchemas`.
It persists immutable connection IDs and configuration metadata, display names,
enabled intent, explicit default preference, removal tombstones and onboarding
completion. Its transactional initializer can record legacy mappings once and
distinguishes missing configuration from an intentionally empty collection.

The setup checkpoint adds durable `stp_<uuid>` reservations and the optional
`hostsvc.ConnectionSetupHost` seam. The local host can prepare private,
owner-allocated configuration directories, resume the same receipt and save its
preassigned connection ID atomically. Setup remains disabled unless composition
explicitly supplies the store and per-integration configuration policy.

The dialog callback contract carries the owner's `reservationId` through Check
again, Back, renaming and Save retries. It rejects a response that changes or
drops a pinned receipt. This is tested with callback mocks, not a live owner API.

**Only the schema is wired into startup.** The repository initializer and
mutations have no production callers yet. Existing platform settings remain
authoritative. Native authentication, runtime isolation and routing have not
changed. Do not describe either checkpoint as working multi-account support.

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

1. Activate the persisted collection on the owner once runtime routing can
   consume it. The repository and atomic legacy initializer are implemented;
   startup still needs to capture installation state before creating native
   state, resolve persisted legacy selections without transient CLI overrides,
   and supply the initial default preference. Legacy configurations, including
   disabled ones with saved references, must migrate without forcing existing
   users through onboarding.
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
7. Wire the implemented owner-local setup reservations into setup inspection,
   login instructions and the dialog's API controller. The dialog retains the
   receipt during its lifetime; the controller still needs to cancel abandoned
   reservations and decide how to resume setup after a browser reload. Add native account checks
   and login commands that use that reserved configuration. The browser's
   account/status observations are never mutation authority. Preparing a private
   directory alone does not establish credential isolation.
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

### Connection repository contract

- Each owner keeps its own collection in its own `state.db`. There is no hub
  mirror or remote mutation API yet.
- `Initialize` takes an explicit owner-supplied installation snapshot. It commits
  metadata and legacy mappings together, or nothing. Once initialized it ignores
  later seeds, including after every connection is removed. Schema migration
  alone never guesses whether this is a new installation.
- Legacy enabled intent and the selected default are preserved even when the
  preferred legacy connection is disabled. Migration does not pick another
  account to make that preference available.
- New IDs use `cnx_<uuid>` and never replace platform IDs or canonical session
  IDs. A migrated connection has a unique `LegacyKind` mapping for existing
  platform-keyed references. Removal retains that mapping permanently; adding
  another connection of the same kind or name cannot claim it.
- Kind, configuration mode and directory cannot be edited through the store.
  Renaming affects only presentation. New directories must be owner-local
  absolute paths. Migrated legacy configurations may have an empty directory
  until the owner resolves their effective configuration. The store neither
  creates directories nor verifies credential isolation.
- Reads exclude removed connections from choices, but lookup by ID and legacy
  kind still returns their tombstones. `Default` returns an error for a disabled
  or removed preference, not another account. Disabling and removal do not
  change the saved default ID. A replacement becomes the default only through
  an explicit selection.
- Enabled state is intent, not readiness. Readiness and account observations are
  not persisted as authorization. The future owner service must verify setup
  before setting a default, recheck admission before execution, and drain work
  before recording removal. Direct repository calls do not stop runtimes.
- The store admits up to 64 nonremoved connections and pending setup reservations
  together, including across separate store instances. Tombstones and canceled
  or saved receipts do not consume additional slots.
- Tests cover restart, multiple same-kind connections, default disable/enable,
  removal and replacement, concurrent initialization and capacity admission,
  failed migration rollback, missing versus empty configuration, and additive
  schema migration preserving legacy settings. They use disposable state stores
  and never launch native agents or inspect real credentials.

### Setup reservation contract

- The owner selects existing configuration paths from integration metadata, or
  allocates a separate path below its explicit state directory. The setup request
  accepts a kind and mode, not a browser-supplied path. No shared-state fallback
  is allowed when the state directory or integration policy is missing.
- The reservation is committed before filesystem preparation. A preparation
  failure returns the receipt as well as the error; retry resumes it. The receipt
  pins kind, mode, directory and the future connection ID across server restart.
- Preparing a separate directory uses rooted filesystem operations and rejects
  symlinks, non-directories and directories with group/other permissions. Newly
  created directories use mode 0700. It copies no existing account files and
  launches no native tools. It is not a security boundary against the same user.
- A missing directory belonging to an already-saved connection is an error, not
  permission to silently create a new blank configuration for that identity.
- Save takes a receipt, display name and enabled intent. It accepts no account
  observations, kind or path. The connection insert and saved receipt commit in
  one transaction. Retrying Save returns the existing connection without
  duplicating it or overwriting subsequent edits. Removal cannot be resurrected
  by retrying an old Save.
- Cancellation releases pending capacity but retains the receipt and directory.
  A partially completed login may already have stored credentials there. A saved
  receipt cannot be canceled to bypass the connection removal/draining path.
- The local Host has no configured setup store or preparation policy by default.
  There are no setup HTTP routes, remote RPCs, login commands or account probes
  for named connections yet. Runtime-level account isolation and admission are
  still separate completion requirements.
- The reusable dialog keeps the receipt out of editable form state and includes
  it in callback requests. Name changes and Back do not start another setup;
  kind, configuration mode and normalized directory changes invalidate it. An
  owner response that changes or drops a receipt clears the successful check
  and blocks Save until a matching check succeeds. Readiness observations and
  returned paths are not copied into authoritative save fields.

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

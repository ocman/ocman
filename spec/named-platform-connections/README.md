# Named platform connections

## Delivery status

The implementation now includes owner-local persistence and setup receipts,
connection-keyed routing, native account constructors, owner/remote APIs and the
mounted Settings/onboarding/composer flow. Full tests, browser regressions and
lint pass with coverage above both baselines. The contracts below describe the
implementation and its supported native modes.

- Immutable `cnx_<uuid>` IDs identify named configurations. Native platform kinds,
  physical history sources and canonical session IDs remain separate identities.
- Durable setup receipts survive retries and browser reload. Save reuses the
  preassigned connection ID; cancellation never removes possible credentials.
- Creation receipts pin retry keys, canonical session IDs and accounts before
  native effects. Existing sessions and forks retain their original connection.
- Shared admission fences prevent new work during disable/removal; prompt
  replies and abort remain available so active work can settle. Retained source
  bindings allow disabled/removed history to stay browsable, not executable.
- Saved-collection startup composes multiple adapters of one kind. Explicit
  `-platforms` remains a legacy CLI override, does not rewrite saved intent, and
  does not advertise editable named-connection onboarding for that run.
- Claude, Codex and OpenCode have explicit account-scoped constructors and native
  metadata inspectors. Unknown authentication/configuration/version combinations
  fail closed rather than borrowing machine credentials or another account.
- Authenticated remote metadata carries connection routing keys and physical
  source proof internally. Public capabilities expose native kind separately;
  composer models keep their native namespace while selection uses a connection.

Tests use disposable databases, fake agents and private fake runtimes. No tests
sign in, copy credentials or issue real model requests. A successful setup check
is a native login/configuration snapshot, not a network, quota or model guarantee.

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

## Implementation requirements

1. Activate the persisted collection on the owner only with connection-aware
   runtime routing. Capture installation state before creating native state,
   resolve persisted legacy selections without transient CLI overrides,
   and preserve the initial default preference. Legacy configurations, including
   disabled ones with saved references, must migrate without forcing existing
   users through onboarding.
2. Separate platform kind, connection routing ID and physical source identity.
   Keep existing source keys and canonical session IDs stable. Existing default
   connections need compatibility mappings for platform-keyed settings and
   references; do not reimport history under a new platform identity.
3. Refactor integration constructors to accept explicit connection configuration.
   Never change process-global environment variables while constructing adapters.
   Audit native environment inheritance so an API token cannot override the
   intended subscription account silently. All three native adapters now expose
   explicit constructors; default-machine constructors retain legacy behavior.
4. Scope supervisors, runtime records, rules, launch files, sockets, spools,
   discovery and recovery to connections. Named provider runtime records include
   connection ID, native platform and repository root. These keys must not collide
   for two accounts in the same repository. Legacy constructors retain their
   original recovery stores and instance identities.
5. Preserve the canonical wrapper's optional capabilities and source checks.
   A shallow interface wrapper that loses optional interfaces is not sufficient.
   Browsing must remain source-scoped: the retained-history fallback must not
   expose another connection's source as executable.
6. Carry named connections through owner Host operations, remote inventory,
   capabilities, session targets, drafts, defaults, routines, Factory, MCP,
   helper/title generation, usage and cooldowns. Explicit unavailable owners or
   connections fail closed; never fall back to a similarly named connection.
7. Wire owner-local setup reservations into inspection, login instructions and
   the dialog's API controller. Retain receipts across reload and allow explicit
   cancellation of abandoned setup. Native account checks and login commands use
   that reserved configuration. The browser's
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
  app-server, with separate supervisor roots and recovery records, effective
  native configuration inspection and backend project-override guards.
- **OpenCode:** isolated configuration, authentication, data/database and server
  per connection. Existing-service adoption remains only for the connection
  matching that service's database and credentials. A separate database alone
  does not isolate credentials; a new connection must not borrow the registered
   default service. Read the OpenCode skill before changing its runtime behavior.

### Connection repository contract

- Each owner keeps its own authoritative collection in its own `state.db`.
  Authenticated remote operations query and mutate that owner, not a hub copy.
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
  not persisted as authorization. The owner service verifies setup
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
- A bare local Host has no configured setup store or preparation policy by
  default. Saved-collection application composition supplies the policy and
  lifecycle hooks; unsupported owners do not advertise an editable setup UI.
- The reusable dialog keeps the receipt out of editable form state and includes
  it in callback requests. Name changes and Back do not start another setup;
  kind, configuration mode and normalized directory changes invalidate it. An
  owner response that changes or drops a receipt clears the successful check
  and blocks Save until a matching check succeeds. Readiness observations and
  returned paths are not copied into authoritative save fields.

### Codex runtime contract

- `codextransport.Config.Connection` requires a canonical `cnx_<uuid>` ID, an
  explicit absolute existing Codex home, and an explicit absolute owner state
  directory. Missing or invalid inputs return an error; they never fall back to
  `CODEX_HOME` or the machine account. A symlink to an account home resolves to
  that home's physical identity, as native history ingestion already expects.
- Rules and the logical supervisor root live under
  `<state>/platform-connections/<connection-id>/runtime/`. Rooted preparation
  refuses symlink aliases, files and nonprivate directories. Runtime keys do not
  fold to a surrounding Git checkout, even when state is stored inside it.
  Ensure and recovery also refuse roots retargeted after construction, before
  looking up or replacing a runtime. This is a preflight, not containment against
  another process running as the same user.
- Socket/spool instance directories retain the short `<state>/p` parent to fit
  Unix socket limits. Their existing instance names include the hash of the
  connection-scoped root and launch identity, so siblings cannot reuse them.
  MCP still uses the owning Ocman's listener and bearer-token file.
- Named child environments replace `CODEX_HOME` and omit known inherited
  authentication, identity, endpoint and billing overrides. The same policy is
  used by the dashboard's history app-server and the supervisor's native reader
  and ACP execution. `HOME` and process-global environment variables do not
  change. Legacy machine construction retains its original environment policy.
- The supervisor's `--connection-account` flag requires an explicit absolute
  home and participates in runtime identity. Recovery cannot mistake a legacy
  unfiltered supervisor for a named-account supervisor.
- Fake-process tests cover two same-kind accounts with the same native thread ID
  in one project, a turn written to only one account, retained runtime recovery
  without replacing the sibling, and authentication-override rejection in both
  native readers and ACP execution. A runtime-root retargeting regression was
  observed failing before the admission guard and passing afterward. Tests do
  not contact real model services,
  read real credentials or establish compatibility with every installed Codex
  version or credential-store configuration.
- Supported named inspection is deliberately narrower than upstream capability:
  Codex 0.164.x/0.165.x with native file-stored ChatGPT authentication. Unknown
  versions/layers, profiles, custom providers and keyring storage are unsupported.
  Inspection reads effective configuration and account metadata without refreshing
  tokens. Actual project `.codex/config.toml` overrides are rejected at backend
  create/resume/send, not merely during setup.

### Shared connection routing contract

- `Reference.ConnectionID` selects an exact owner-local connection.
  `Reference.LegacyKind` selects only the original retained migration mapping.
  Setting both is invalid; setting neither uses the explicit saved default.
  No path chooses the first available connection of a kind.
- `Router.Bind` registers an already configured `platforms.Platform` under its
  connection ID, verifies the stored kind, and refuses duplicate registration.
  The adapter's `ID()` still names the native platform kind, not the connection.
  Disabled entries can be composed, but removed entries cannot be bound.
- `Router.Resolve` rereads persisted selection and enabled/removal intent and
  checks current adapter availability. Failure never falls back to the owner
  default, a sibling account or a display-name match. Returned metadata reflects
  renames without retargeting the binding.
- One router uses one owner's Store. The caller must resolve an explicit owner
  before selecting a connection and must fail closed on disconnected remotes.
  Tests verify that an unknown other-owner ID cannot select the local default.
- The router does not construct, launch, close or prove account isolation for an
  adapter. Composition owns those operations and must drain before replacement.
  Resolution is a snapshot. `Begin` leases native mutations; `Detach` fences new
  admissions and waits for leases before lifecycle draining and persistence.
  Prompt responses/abort bypass the new-turn fence so work can settle.
- Session services, saved-collection startup and remote publication consume this
  seam. Canonical session/source identities must not be rewritten as connection
  IDs. Retained read-only bindings are distinct from executable routing.

### Durable session routing

- `Store.BindSession` records only canonical `ocm_<uuid>` session IDs and refuses
  reassignment to another connection. The caller must first establish canonical
  owner/source authority. Repeating an existing binding is idempotent even after
  removal; creating a new binding requires an enabled, nonremoved connection.
- The `platform_connection_session` table references only this module's
  connection table. It does not change session-catalogue or physical history
  keys. Removal retains routing evidence; native IDs alone are never accepted.
- `Router.AdmitSession` resolves and pins an account before creation/adoption.
  A retry with no explicit reference uses its durable connection, not the
  current default. An explicit different connection returns a conflict.
- `Router.ResolveSession` requires a durable binding and rechecks connection
  availability. An imported or unknown session cannot silently acquire the
  current default. Migration/import wiring must establish provenance first.
- Tests cover concurrent account claims, retry after default changes, disabled
  accounts, retained tombstones, schema upgrade and database reopen. These are
  persistence/admission tests, not application-level routing activation.

### Connection-scoped creation and service dispatch

- `Router.AdmitCreation` reserves a retry key, canonical session ID and selected
  connection atomically. Generated IDs survive retry and router reconstruction;
  an explicitly supplied identity cannot change or acquire a second request key.
  Payload fingerprints and canonical owner/source authority remain the session
  catalogue's responsibility. No credential or native model operation occurs.
- `Service.WithConnections` installs canonical owner-local routing before
  serving. Local creation pins its account before invoking a canonical adapter.
  Existing supplied catalogue identities must already have connection authority.
  The owned adapter still owns source validation, native binding and publication.
- Creation retry uses the receipt, not a changed default. Forks inherit their
  parent's pinned connection and reserve the child's identity before the native
  fork. Subsequent service mutations resolve catalogue authority and then the
  durable connection, ignoring the legacy kind-keyed registry for local accounts.
- Explicit connection scopes cannot override the recorded account. Explicit
  remote scopes and remote-owned catalogue rows retain authenticated remote
  registry/gateway routing; they never acquire the local default. Remote named
   inventory includes exact connection IDs and authenticated physical source
   proof, not an inferred first account of the same kind.
- Catalogue preparation and platform selection return the connection ID while
  native adapters, created-session events and physical history retain their
  existing platform kind. Unknown imported sessions are not assigned to a default.
- Tests exercise two canonical same-kind adapters with distinct native sources,
  pre-native account pinning, creation/fork retry, mutation routing and disabled
  connections. Source-proven imported history can acquire its exact execution
  binding; unknown sources remain unbound, never assigned to the current default.

### Claude account isolation

- `claudeprovider.Options.ConnectionAccount` is opt-in. A missing, relative,
  nonexistent or non-directory `ConfigDir` fails before opening an index or
  probing the CLI. Valid directories resolve to a physical path shared by
  history, ownership checks and child environments. Caller mutation of a
  supplied `ConfigEnv` pointer cannot change that account.
- Session execution and provider-owned account/catalog probes use the same
  environment policy. It strips inherited `ANTHROPIC_*`, `CLAUDE_CODE_USE_*`,
  `CLAUDE_CODE_OAUTH_*` and `CLAUDE_CODE_API_KEY_FILE_DESCRIPTOR` values, pins
  `CLAUDE_CONFIG_DIR`, and retains SDK markers and session `PWD`. This deliberately
  removes inherited model/endpoint overrides too. `HOME`, generic tool
  credentials and unrelated MCP variables stay unchanged. Legacy machine
  options keep their existing inherited environment behavior.
- The inherited-API-key regression test failed before the policy and passed
  afterward. A fake Claude turn also verifies that session spawning uses it.
  These tests do not read real credentials or contact model services.
- Environment filtering alone is not proof of subscription identity. Supported
  inspection requires Claude 2.1.268+ within the 2.1 series, an already initialized
  `.claude.json`, matching native `configDirectory` and subscription auth mode.
  Named launches/helpers pin user-only settings; `apiKeyHelper`, authentication
  overrides and ambiguous managed/remote policies are unsupported. Upstream
  [authentication documentation](https://code.claude.com/docs/en/authentication)
  describes directory-scoped keychain entries for subscription logins, but warns
  that Console sign-ins without API keys are not isolated by config directory.
- The named transport scopes index, rules, session ownership, usage, launch
  configuration and plugin marketplace under the connection runtime directory.
  Supervisor records include connection ID/platform/repository. Helpers, titles,
  judging and plugin commands select the same filtered native account. Owner MCP
  tokens and machine-wide child/helper slot budgets remain shared.

### OpenCode account isolation

- Named OpenCode uses an explicit native database and separate configuration,
  data, state, cache and runtime paths. It never adopts the machine-wide native
  background service. The original migrated machine integration may retain its
  matching registered service, but newly isolated connections cannot borrow it.
- HTTP/SSE authentication, native dialect discovery, catalogue caches and prompt
  request-to-session bindings are account-private. A regression reproduced a
  permission reply crossing accounts when native request IDs collided, before
  the transport cache was isolated.
- Supported inspection uses verified OpenCode 2.1.x `/api/info` and
  `/api/integration` metadata only. It reads the native active-first connection
  ordering and accepts saved key/OAuth modes; environment/external/unknown modes
  fail closed. It does not fetch the credential-secret endpoint.
- Pre-save inspection can initialize the selected private database with a
  uniquely named, no-model metadata server. An ephemeral namespace-filtered
  store and bounded cancellation cleanup stop only that inspection's process.

### Owner, remote and browser integration

- `/api/platform-connections?remoteId=<owner>` resolves the explicit owner.
  Mutation routes are localhost-guarded and use `ConnectionHost`; unsupported or
  disconnected remotes cannot borrow local inventory or credentials.
- `Activate` is idempotent and requests safe composition reload. An activation
  failure retains the saved connection/receipt for retry. Disable/removal closes
  admission and drains before changing persisted intent; failure restores it.
- Authenticated `ConnectionPlatforms` metadata separates native kind and route
  ID and carries physical source proof internally. Legacy peers may fall back
  only for original non-connection platform keys. Physical paths are not exposed
  through public capabilities.
- Settings and onboarding use the same owner-controlled setup dialog. Browser
  recovery is scoped to the owner and preserves the durable receipt. Explicit
  defaults are required; failed requests, empty inventory and unsupported owners
  remain distinct. Canceling after a lost successful Save never deletes the
  saved configuration or possible credentials.
- Composer selection uses owner-qualified connection IDs. Capability metadata
  retains native kind for model references. Model selection never changes to a
  same-kind sibling account automatically.

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

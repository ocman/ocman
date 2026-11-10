---
title: Named platform connection persistence
weight: 15
---

Named connections are under development. The UI components are not mounted in
live Settings or onboarding. Only the connection schema is registered at startup;
no production code initializes or mutates the collection yet. Current platform
selection and runtime behavior are unchanged.

```mermaid
flowchart LR
    Startup[Application startup] --> Schemas[server.ModuleSchemas]
    Schemas --> Migration[state.Open module migrations]
    Migration --> Tables[(Connection tables in owner state.db)]
    Tests[Disposable repository tests] --> Store[internal/platformconnections]
    Tests -->|receipt-aware callback mocks| Dialog[Unmounted setup dialog]
    Tests --> Host[Owner-local ConnectionSetupHost]
    Host -->|durable setup receipt and Save| Store
    Host -->|private empty configuration only| Config[(Explicit owner state directory)]
    Store --> Tables
    Store --> Identity[Stable IDs, legacy mappings<br/>and exact defaults]
```

- `internal/platformconnections` owns the append-only module schema and queries.
  It stores connection kind, immutable configuration metadata, display name,
  enabled intent, removal tombstones and collection onboarding state. It does
  not store credentials, persist setup readiness or launch agents.
- `hostsvc.ConnectionSetupHost` is the optional owner seam for setup preparation,
  Save and cancellation. Its local implementation requires an explicitly
  injected store and integration policy; neither is wired in production yet.
  It allocates private empty configuration directories without copying account
  data. Preparing a directory does not prove credential isolation or readiness.
- Durable setup receipts pin a configuration and preassign the connection ID
  before preparation. Retry uses the same directory after restart. Save consumes
  the receipt transactionally; repeats return the current connection without
  creating a duplicate or undoing later edits. Cancellation retains possible
  credentials and frees only pending capacity.
- The unmounted dialog includes an owner-issued receipt in Check again and Save
  callbacks. It retains the receipt through Back, name edits and request retries,
  but invalidates it for configuration changes. A changed or missing receipt in
  a subsequent owner response blocks Save. There is no live API controller yet;
  abandoned-reservation cleanup and browser-reload recovery remain to be wired.
- The atomic initializer takes a snapshot from the owner. New installations
  start empty with onboarding incomplete; existing installations skip onboarding
  and retain mappings from old platform kinds to opaque connection IDs. An
  initialized empty collection is not reseeded. Installation detection and
  startup activation remain future work.
- Connection IDs do not change platform IDs, physical source keys or canonical
  session IDs. Legacy mappings survive removal so a replacement account cannot
  acquire the old account's references. Renaming cannot retarget configuration.
- Removal records a disabled tombstone. It does not delete native directories,
  credentials or retained history. Disabled and removed defaults stay selected
  in storage, but default lookup returns an error rather than another account.
- Before exposing the store through Settings, owner-local services must enforce
  setup checks and removal draining. Connection-scoped runtime routing, remote
  inventory and account isolation are still required before enabling multiple
  accounts of the same kind.

## Explicit Codex runtime configuration

The Codex constructor accepts an optional named connection with an immutable ID
and an explicit existing home. Application startup does not supply it yet. Nil
keeps the legacy machine account and its existing launch identity unchanged.

```mermaid
flowchart LR
    Config[Explicit connection ID and home] --> Paths[Validate canonical home<br/>and private runtime directories]
    Paths --> Rules[Connection-scoped rules]
    Paths --> Manager[ProviderManager<br/>canonical runtime root, no Git folding]
    Manager --> Record[(Owner state.db<br/>connection-scoped recovery key)]
    Manager --> Supervisor[Short owner socket parent<br/>unique scoped instance name]
    Config --> Env[Filtered named-account environment]
    Env --> History[Dashboard history app-server]
    Env --> Supervisor
    Supervisor --> Native[Native reader and ACP execution]
```

- The account home remains the physical native-history source key. No canonical
  session IDs or platform kinds are rewritten by this constructor.
- Named runtime roots are owner-allocated directories, not project directories.
  `ProviderManager.CanonicalRoots` prevents their accidental collapse to a Git
  checkout and refuses aliases introduced before a later ensure or recovery.
  Default project-supervisor normalization remains unchanged. These checks do
  not contain another process running as the same user.
- Rules and recovery roots are scoped to the connection ID. Socket/spool
  directories keep the short owner-wide parent, with the scoped root hash in
  each unique instance name. The owner's MCP listener and credential path remain
  owner-wide.
- Named readers and execution omit known inherited account-authentication,
  identity, endpoint and billing overrides and receive exactly one pinned
  `CODEX_HOME`. Legacy machine connections retain their environment behavior.
- Fake agents verify simultaneous same-kind accounts and restart without
  cross-account writes or supervisor replacement. Installed-version checks,
  account probes and registry/session routing remain required before exposing
  this configuration in live Settings.

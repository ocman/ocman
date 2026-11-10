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
    Store --> Tables
    Store --> Identity[Immutable IDs and legacy mappings]
    Store --> Preference[Exact saved default or error]
```

- `internal/platformconnections` owns the append-only module schema and queries.
  It stores connection kind, immutable configuration metadata, display name,
  enabled intent, removal tombstones and collection onboarding state. It does
  not store credentials, persist setup readiness or launch agents.
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

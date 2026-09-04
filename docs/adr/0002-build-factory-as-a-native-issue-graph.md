# Build Factory as a native Issue graph

Factory owns Epics, composable Mols, typed Issues, dependencies, attempts, Formulas, immutable Plan manifests, and provenance in ocman's state rather than integrating an external issue tracker. This permits atomic revision-bound materialization and outcome-sensitive scheduling under one authority boundary; the trade-off is that ocman owns these semantics and provides no compatibility or migration for the discarded Factory implementation.

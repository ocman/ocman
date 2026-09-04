# Keep the Factory MCP contract implementation-neutral

The single Factory MCP tool and globally installed Factory skill expose native Epics, Mols, Issues, Plans, Formulas, and domain-level errors. Operational details are excluded from model-facing schemas and results so agents do not couple to storage, filesystem, or process internals; agents use the tool's `help` action for its current contract.

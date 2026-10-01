# Domain Docs

How engineering skills consume this repository's domain documentation.

## Before exploring

- Read `CONTEXT.md` at the repository root.
- Read relevant records under `docs/adr/` when that directory exists.
- If either is absent, proceed silently; domain-modeling creates them only when needed.

## Layout

This is a single-context repository:

```text
/
├── CONTEXT.md
└── docs/adr/
```

Use glossary terms in plans, Issues, tests, and implementation. Surface conflicts with an existing ADR instead of silently overriding it.

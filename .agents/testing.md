# Testing and verification

Tests live alongside code as `*_test.go` and `*.test.ts(x)`. Prefer table-driven
Go tests. The server package has a shared `fakePlatform` for integration tests.
Use temporary databases. Never launch a real agent binary in platform tests.

## Required behavior coverage

- Test new functions, branches, loops, parsers and money/security paths in the
  same PR. Existing tests are not a substitute for new behavior coverage.
- For a bug fix, write a reproducer first and observe it fail on the unpatched
  code. Apply the fix, confirm it passes, and state the red/green result in the
  PR. A fix without the failing-first check is incomplete.
- Coverage must not drop. CI allows only 0.1% slack on total Go or frontend line
  coverage. Never lower the baseline to make a change pass.
- Before committing, run `make coverage-check BASELINE_DIR=<baseline>` for the
  touched side. At minimum, run `go test -cover ./...` for Go or
  `pnpm test -- --coverage` in `frontend/`. Confirm a nonnegative coverage delta;
  add tests if coverage drops.

## Local commands

```sh
make test             # Go and Vitest
make test-backend     # go test ./...
make test-frontend    # vitest run
make test-race        # go test -race ./internal/...
make test-fuzz        # every Fuzz* target for 10 seconds
make test-e2e         # build frontend, then Playwright
make test-all-fast    # backend, frontend and e2e in parallel; each stops at its first failure
make coverage SUITE=go
make coverage SUITE=frontend
make coverage-check SUITE=go BASELINE_DIR=<baseline>
make coverage-check SUITE=frontend BASELINE_DIR=<baseline>
make lint
```

`make lint` runs Go vet, golangci-lint, TypeScript, ESLint and the repository
checks for platform branching, host helpers, settings rows and CSS tokens, plus
the fuzz-script self-test. Install golangci-lint; CI enforces more than vet.
See `.golangci.yml` and `spec/ci-coverage-ratchet/` for the configuration.

## CI

The authoritative workflow is `.forgejo/workflows/ci.yml`. Keep workflows there,
not in `.github/workflows/`, so the GitHub mirror does not run them. Preserve
Forgejo-aware setup and `FORGEJO_API_URL` handling.

- Frontend checks ESLint, `pnpm exec tsc -b`, guard scripts,
  Vitest coverage and the coverage baseline.
- Backend checks golangci-lint, production linking with `go build -o /dev/null
  .`, root tests, internal package coverage and the coverage baseline.
- Playwright runs Chromium against the built Vite bundle.

The weekly `.forgejo/workflows/fuzz.yml` workflow runs `scripts/fuzz_test.sh`
before fuzzing. This self-test uses a fake Go command and does not run in
regular CI. On failure the workflow uploads `internal/**/testdata/fuzz/`, where
Go writes crashing inputs.

## E2e runs

Specs mock `/api` through `frontend/e2e/fixtures.ts` and run fully parallel,
so a spec must not depend on another's state. The Playwright server listens on
a port derived from the checkout path and never reuses a running server; see
`frontend/playwright.config.ts` for `E2E_WORKERS`, `E2E_PORT` and the other
overrides. Wait for the state a user would see, not a fixed delay.

## E2e locators

In `frontend/e2e/`, prefer these locators in order:

1. ARIA roles, labels and visible text through `getByRole`, `getByLabel` or
   `getByText`.
2. `data-testid` for structural or state elements without an accessible name.
3. CSS classes only as a last resort.

When repairing a class-based locator, replace it with a role or test ID rather
than patching the class name. Add test IDs for structural elements that e2e
tests need to target.

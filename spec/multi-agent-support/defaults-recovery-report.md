# Platform-default and runtime-availability recovery

Recovery: restore platform defaults and runtime availability after main integration
(claude-code-provider.1.3.136), shared branch `factory/claude-code-provider`, PR834
targeting main `e243236a3caac87efe4bad0399de39876d1fdab6`. This report belongs to
the new recovery commit; its parent is `e4d4d25bf272f38e2449ef1609dd4b648d86ac5d`.
Verification of the parent failed and is not certified by this work.

## Scope and corrections

Read the approved design revision 2 and the scope revision 32/graph revision 33
evidence, committed conformance/deviations/delivery/integration reports, and both
files in verification artifact `6k5oniscbmjqoor73mfgp5tjtg`. Preserve approved
replay/acknowledgment, observed CLI usage, 4+2 helper budget and runtime/RPC shapes.
R1-R6 and all other logged departures in `delivery-report.md` remain unresolved;
their partial conformance and historical failures are retained.

| Finding at parent | Correction and acceptance evidence |
|---|---|
| B1: omitted platform reaches ensure as an empty key | `handleCreateSession` embeds `newSessionTarget` and resolves its owner/platform before ensure/create. `TestRESTCreateResolvesDefaultBeforeRuntime` covers sole OpenCode, sole Claude, mixed saved defaults and explicit override. `TestRESTCreateAuthoritativeOwner` covers remote defaults/compound IDs, explicit local, mismatch, disconnected owner and unknown platform without launch side effects. |
| B2: unqualified owner/project defaults override the selected catalog | `prepareDraftCatalog` filters both settings and prepare-result project models against available catalog providers, and settings agents against non-subagent catalog entries. Prepared defaults are the fallback. Four `NewConversation.lifecycle.test.tsx` regressions render the real Composer, switch platform or machine, retain draft identity/text/image and submit `anthropic/sonnet` without `plan`. Supported custom defaults, explicit custom selections and primary-agent controls remain tested. |
| C1: runtime interface alone advertises native launch | Optional `hostsvc.RuntimeAvailability.RuntimeAvailable() bool` separates native prerequisites from browsing. Claude checks its resolved CLI and tmux. Host capability tests cover missing/present CLI/tmux, mixed OpenCode/Claude and OpenCode fallback. Root tests exercise the real Claude adapter's prerequisite method. Existing prerequisite-free fake-runtime coverage remains unchanged. |

The small catalog-default extraction was approved through Recovery gate
(claude-code-provider.1.3.137). `NewConversation.tsx` is now under 400 lines.
No frontend platform-identity branch, dependency, protocol/RPC or schema change.
The new optional interface and REST `remoteId` field are additive representation
changes, recorded in `deviations.md` and architecture prose.

## Failing-first and initial failures

- Before product edits, new REST and native-provider capability tests failed:
  ensure received an empty key, mixed defaults could not select a platform,
  owner mismatch/disconnection was mishandled, and native launch was true without
  prerequisites. Root Claude availability test failed because the method was absent.
- Before product edits, all four rendered default-switch regressions failed while
  displaying `gpt-4.1` instead of `sonnet`. Images were rendered; no real Claude ran.
- Initial test-authoring compile errors used a typed platform ID in a string fixture;
  corrected before behavioral red/green. An initial startup-test experiment could
  not force missing tmux because login-shell PATH enrichment found installed tmux;
  replaced with deterministic direct prerequisite tests.
- First targeted frontend rerun found outdated fixtures lacking supported catalog
  agents/models and a leaked fetch mock. Fixtures now advertise their intended
  primary agent/provider and reset project settings per test. The old Claude-switch
  assertion expected foreign `build`; it now asserts no agent. Remaining assertions
  are preserved. Typecheck caught two fixture typing mistakes, corrected.
- First complete Go run failed `TestHandleCreateSession_RemoteEnsuresBeforeCreate`
  and `TestCreateSessionEnsuresForValidRemotePlatform`: their registered remote Host
  fixtures returned `RemoteID() == local`. Fixtures now report the correct owner;
  all assertions are unchanged. The complete rerun passed.
- Initial Chromium run passed 316 and failed one `composer-machine.spec.ts` case
  whose preparation fixture omitted the expected `build` agent/default. The fixture
  now advertises `build`; the assertion is unchanged. Full rerun result below.
- Read-only spec review found another implicit path: a remembered directory model
  could survive a machine switch when the destination retained an unavailable
  historical catalog entry. A new regression failed with `openai/gpt-4.1` instead
  of `anthropic/sonnet`, then passed after filtering unavailable remembered entries.
  The rendered switch tests now include this historical unavailable entry too.

## Fresh local checks

macOS arm64, Go1.26.1; all Go commands use `GOMAXPROCS=4 GOFLAGS=-p=4`.
Tests use disposable temporary databases/directories and fake Claude only.
Chromium serves the built static SPA on loopback with `/api` and `/mcp` proxied
to closed port1. No live/shared database or real CLI was used. No thresholds,
exclusions, skips, suppressions or individual test deadlines were weakened.

| Command | Result |
|---|---|
| `make lint` | PASS, Go0 issues, ESLint0 errors/105 warnings, TypeScript/CSS/guards pass. |
| `go test -count=1 ./...` | Full rerun PASS all suites; server212.247s. Initial failures retained above. |
| `make coverage SUITE=frontend` | Final source PASS418 files/4441 tests; lines90.17%. |
| `make coverage SUITE=go` | PASS; lines87.5%. |
| `go test -race -count=1 -timeout=20m ./internal/hostsvc/local ./internal/sessionsvc ./internal/remote ./internal/ocruntime` | PASS all4 packages. |
| `go test -race -count=1 -timeout=20m ./internal/server -run 'TestRESTCreate\|TestHostCaps\|TestCreateDefault\|TestCreatePaths\|TestHandleCreateSession\|TestCreateSession\|TestStartSession\|TestPrepareSession'` | PASS9.208s. Targeted creation/catalog/capability paths, not a claim of a fresh full-server race run. |
| `go test -race -count=1 -run 'Claude\|Drain\|Browser\|Provider\|DualPlatform' .` | PASS46.193s. |
| `pnpm --dir frontend build`; `go build -o /dev/null .`; `go build -tags desktop,exclude_graphdriver_devicemapper ./...` | PASS, ordinary bundle-size warning retained. |
| `go mod tidy -diff`; `make proto`; `git diff --exit-code -- go.mod go.sum internal/remote/proto` | PASS, no module/generated diff. |
| `make docs-build` | PASS46 pages. |
| `E2E_NO_WEBSERVER=1 E2E_BASE_URL=http://127.0.0.1:18356 pnpm exec playwright test --project=chromium --workers=1 --retries=0` | Full final-source rerun PASS317/317, zero retries/skips; initial316/317 retained above. Preview process stopped and temporary config removed. |
| Separate `make coverage-check BASELINE_DIR=<extracted official>/coverage SUITE=frontend` | PASS90.17 vs89.38, +0.79, unchanged0.1 tolerance. |
| Separate `make coverage-check BASELINE_DIR=<extracted official>/coverage SUITE=go` | PASS87.5 vs86.7, +0.80, unchanged0.1 tolerance. |

Fetched official baseline `origin/gh-pages` SHA
`743b6eb5489bf08194da0466e00c8e6ec3af1217`, data for current main
`e243236a3caac87efe4bad0399de39876d1fdab6`, extracted with `git archive` into
`/var/folders/fw/6n6_9w296qx5qpg6brcjxfmr0000gn/T/opencode/claude-defaults-recovery`.
Recovery gate (claude-code-provider.1.3.67) remains a delivery obligation to
compare its then-current integrated source and official baseline, not a waiver.

## CI and handoff

Parent CI3652 still fails Backend installation of `protoc-gen-go@v1.36.11`
with `could not import os` / `bufio: buffer full`, before tests. Coverage Results
then lacks `backend-coverage`; external `ocman/review` reports error without
diagnostics. Those failures are not test regressions or deferred R6. No runner
cache or infrastructure was changed.

The new pushed checkpoint requires its own CI result. If runner infrastructure
prevents clean CI, request a specific human recovery decision rather than claiming
local passes certify CI. Factory dispatches fresh read-only verify
(claude-code-provider.1.5) after this implementation checkpoint; deliver
(claude-code-provider.1.2) remains pending that verification.

Read-only standards review found no hard breach or actionable smell. Spec review
found the unavailable remembered-model path; its failing-first regression and fix
were reviewed again and passed. This is implementation review, not Factory's fresh
verification or final delivery certification. Exact pushed SHA and post-push CI
evidence are recorded in PR834 and the Factory handoff, avoiding a self-referential
commit hash in this report.

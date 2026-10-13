# Linux backend coverage fault-injection recovery

Recovery: reproduce and resolve Linux backend coverage fault-injection failures
(claude-code-provider.1.3.140), shared branch `factory/claude-code-provider`, PR834.
Starting checkpoint `d5685d04964b0e5188b8e4ae31e5efff2bba15ce`.

## Original evidence

CI3656 passed the Linux root suite in 17.937s, then failed `make coverage SUITE=go`
in `internal/claudeprovider`. Original failures included:

- `TestConflictingReplyRetryIsRefused`, conflict_test.go:107, unrecorded reply acknowledged.
- `TestSpoolFailureFailsTurnVisibly`, durability_test.go:92, child stopped timeout.
- `TestCancellationDuringOutageIsRecorded`, outage_test.go:141, idle edge during outage.
- `TestSettlementRetryOwnsEveryGeneration`, settle_test.go:73, retry worker never ran.
- Other unrecorded question/reply, outage, replay-queue and stale-settlement failures.
- `TestSendRacingLimitResetKeepsTurnOpen`, durability_test.go:224, limited timeout.

The complete original job log is retained with this recovery's raw evidence.
CI used Linux arm64, Go1.26.1, a `node:20-bookworm` container with no user override,
and `/root` home/build/module paths. The prior macOS full Go and coverage passes,
and the two full-root macOS race failures in `TestStartupWithoutOpenCode` at
platform_startup_test.go:70, remain evidence in `supervisor-death-recovery-report.md`.
Those startup failures reported identity initialization context cancellation.

## Reproduction and cause

Disposable Linux arm64 container, Go1.27.1, read-only source, container-local
caches/home and temporary databases. All Claude execution uses the fake binary.
No live data, runner, cache policy or infrastructure was changed.

```sh
docker run -d --name ocman-coverage-recovery-140 -v "$PWD:/src:ro" -w /src \
  -e GOTOOLCHAIN=local -e GOMAXPROCS=4 -e GOFLAGS=-p=4 golang:1.27.1 sleep infinity
docker exec ocman-coverage-recovery-140 sh -c 'id; go version; go test ./internal/claudeprovider -run "^(TestConflictingReplyRetryIsRefused|TestSpoolFailureFailsTurnVisibly|TestCancellationDuringOutageIsRecorded|TestSettlementRetryOwnsEveryGeneration)$" -count=1 -v'
docker exec ocman-coverage-recovery-140 go test -c -o /tmp/claude-red.test ./internal/claudeprovider
docker exec --user 1000:1000 -w /src/internal/claudeprovider -e HOME=/tmp \
  -e GOCACHE=/tmp/nonroot-go-cache ocman-coverage-recovery-140 sh -c 'id; /tmp/claude-red.test -test.run "^(TestConflictingReplyRetryIsRefused|TestSpoolFailureFailsTurnVisibly|TestCancellationDuringOutageIsRecorded|TestSettlementRetryOwnsEveryGeneration)$" -test.v'
```

All four representative failures reproduced as UID0, 23.602s. The identical
unpatched test binary passed as UID1000 and emitted real `permission denied`
spool failures. An initial binary invocation used `/src` instead of the package
directory and failed finding `testdata/projects`; fixing only that invocation's
working directory produced the UID1000 result above.

The fixtures used `chmod 0400` to simulate a spool outage. Root can still append,
so the intended failure was never injected. This demonstrates a test-environment
cause for these assertions, not a production regression. It is distinct from the
earlier export-data/cache failure, supervisor-death failure and deferred R6.
The limit-reset timeout is not explained by chmod and is not claimed fixed.

## Failing-first correction

Added `TestSpoolDownBlocksAppendAndPreservesRecords` before changing the helper.
UID0 failed with `fault injection still allows spool append`; the four original
assertions also failed under cross-package coverage, 24.090s:

```sh
docker exec ocman-coverage-recovery-140 sh -c 'id; go test ./internal/claudeprovider -run "^(TestSpoolDownBlocksAppendAndPreservesRecords|TestConflictingReplyRetryIsRefused|TestSpoolFailureFailsTurnVisibly|TestCancellationDuringOutageIsRecorded|TestSettlementRetryOwnsEveryGeneration)$" -coverpkg=./internal/... -count=1 -v'
```

The shared test helper retains a hard link to the recorded spool and atomically
replaces its path with a symlink to a directory. Restoration atomically renames
the retained file over that symlink. `OpenFile` fails for root and non-root.
Read-only review caught a missing-path window in the initial directory-blocker
implementation; the atomic swaps remove it. Cleanup is registered before the
swap, and the hard link preserves writes through previously opened descriptors.
The two remaining direct chmod injections use that helper. The regression checks
append failure, preserved and in-flight bytes, repeated outages, idempotent
restoration and successful appends after restoration. Production
source and the existing outage, reply, death and settlement assertions/deadlines
are unchanged. No skip, exclusion, suppression, threshold change or retry policy
was added. Scope32 deferrals R1-R6 and all approvals remain unchanged.

## Checks and unresolved gates

macOS arm64 Go1.26.1. Go commands below use `GOMAXPROCS=4 GOFLAGS=-p=4`.
The targeted expression for green checks adds `TestUnrecordedReplyIsRetriedNotRedelivered`
to the failing-first expression above.

Exact final check commands, with the shared expression written once:

```sh
GREEN='^(TestSpoolDownBlocksAppendAndPreservesRecords|TestConflictingReplyRetryIsRefused|TestSpoolFailureFailsTurnVisibly|TestCancellationDuringOutageIsRecorded|TestSettlementRetryOwnsEveryGeneration|TestUnrecordedReplyIsRetriedNotRedelivered)$'
docker exec ocman-coverage-recovery-140 go test ./internal/claudeprovider -run "$GREEN" -coverpkg=./internal/... -covermode=atomic -count=1 -v
docker exec ocman-coverage-recovery-140 go test -c -o /tmp/claude-final.test ./internal/claudeprovider
docker exec --user 1000:1000 -w /src/internal/claudeprovider -e HOME=/tmp -e GOCACHE=/tmp/nonroot-go-cache ocman-coverage-recovery-140 /tmp/claude-final.test -test.run "$GREEN" -test.v
GOMAXPROCS=4 GOFLAGS=-p=4 go test -race ./internal/claudeprovider -run "$GREEN" -count=1
GOMAXPROCS=4 GOFLAGS=-p=4 golangci-lint run ./internal/claudeprovider/...
git diff --check
```

| Command | Result |
|---|---|
| Linux targeted coverage command above | Initial directory blocker PASS8.602s; final atomic implementation PASS8.417s with actual `is a directory` injection. |
| Linux compiled UID1000 binary command above | Initial directory blocker PASS; final atomic implementation PASS. |
| macOS targeted `go test ./internal/claudeprovider -run "$GREEN" -count=1 -v` | Initial directory blocker PASS20.013s. |
| macOS targeted race command above | Initial directory blocker PASS51.736s; final atomic implementation PASS26.865s. |
| Final targeted `golangci-lint` and `git diff --check` commands above | PASS, zero issues. |
| `go test . -count=1` | PASS, 77.961s. |
| `make lint` | PASS, zero Go issues, zero ESLint errors, 105 existing warnings. |
| Linux `go test ./internal/claudeprovider -coverpkg=./internal/... -covermode=atomic -count=1` | FAIL, 81.950s, supervisors_test.go:76, `TestOwnershipSharedAcrossSupervisors`: supervisor a lost an ID. Corrected fault-injection tests pass. |
| Linux same coverage command with `-run '^TestOwnershipSharedAcrossSupervisors$' -v` | Isolated diagnostic PASS, 0.432s. Does not erase the full-package failure. |
| `make coverage SUITE=go` | FAIL. `TestUsageLimitHoldsUntilReset`, lifecycle_test.go:222, limited after restart timeout; tmux command tests hit unchanged 2s deadlines. |
| Same complete Go coverage command, standalone without concurrent heavy jobs | FAIL again with the same limit-reset and tmux deadline categories. No further blanket rerun. |
| `make coverage SUITE=frontend` | PASS, 418 files / 4441 tests, 90.17%. |
| `make coverage-check BASELINE_DIR=<official>/coverage SUITE=frontend` | PASS, 90.17 vs 89.40, +0.77, unchanged 0.1 tolerance. |

Both full coverage failures above occurred with the initial directory blocker.
The final atomic fixture was rechecked with the targeted commands, not another
full-suite rerun. Neither full failure is erased by the targeted passes.
Standards and spec read-only rechecks found no remaining code finding after the
atomic correction; required lifecycle gates remain open.

Current official baseline was fetched with `git fetch origin gh-pages`, then
`git archive origin/gh-pages coverage/go.json coverage/frontend.json | tar -x -C <evidence>`.
Ref `d83d6e89b93cbdf529868b216ed0f11c4b1d7dac` measures current main
`36cdc691f1bd68390ce2757cb834a74956781d21`: Go86.7, frontend89.40.
The current Go comparison is blocked because both complete collections failed.
A pre-existing Go summary from an older run cannot certify this tree and was
not presented as a current comparison. Prior Go87.5 vs86.7 and frontend90.17
vs89.38 passes remain historical results, not replacements for these failures.

Raw logs live in the session's `claude-coverage-recovery-140` evidence directory.
Exact pushed SHA, CI and evidence artifact will be recorded in the Factory issue
comment after push. Clean CI and the current official Go comparison remain
required, or need a specific human recovery decision. The newly observed
ownership/limit-reset/tmux failures need a separate scope decision if required
blockers; no expanded repair is included. The two historical startup-race failures
also need a separate decision if required. Fresh read-only verify
(claude-code-provider.1.5) must assess the new checkpoint before deliver
(claude-code-provider.1.2); this implementation does not certify delivery.

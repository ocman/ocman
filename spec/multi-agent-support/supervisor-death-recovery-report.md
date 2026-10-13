# Linux supervisor-death CI recovery

Recovery: reproduce and resolve Linux supervisor-death CI failure
(claude-code-provider.1.3.138), shared branch `factory/claude-code-provider`, PR834
targeting `main`. Starting checkpoint:
`1f57f7069e6f1819c8276f0372478311e9a1bd92`.

## Original evidence and reproduction

- CI3654 checked out that exact checkpoint. Backend passed protoc installation,
  generated-stub verification, builds and lint, then `go test .` failed:
  `TestClaudeSupervisorSurvivesOcmanRestart (21.39s)`;
  `claude_projects_test.go:315: timed out waiting for supervisor gone`.
  Backend coverage never ran; upload found no files, so Coverage Results lacked
  `backend-coverage`. Frontend and Playwright passed. External `ocman/review`
  reported `error`, "Review failed", without a diagnostic URL.
- CI3603 at `794fc9e95f636c1b57c2660310d9b28740d04e5a` recorded the same
  assertion after 21.52s. These are distinct from the older export-data/cache
  installation failure and deferred R6. Neither proves a product regression.
- Unchanged macOS source: `go test . -run
  '^TestClaudeSupervisorSurvivesOcmanRestart$' -count=1 -v` passed, 2.09s.
  Previous complete and targeted root-race passes remain recorded in
  `defaults-recovery-report.md`.
- Disposable Linux reproduction, read-only source mount, container-local home,
  caches and temporary databases, fake Claude only:

  ```sh
  docker run -d --name ocman-supervisor-recovery -v "$PWD:/src:ro" -w /src \
    -e GOTOOLCHAIN=local -e GOMAXPROCS=4 -e GOFLAGS=-p=4 \
    golang:1.27.1 sleep infinity
  docker exec ocman-supervisor-recovery go test . \
    -run '^TestClaudeSupervisorSurvivesOcmanRestart$' -count=1 -v
  docker exec ocman-supervisor-recovery ps -eo pid,ppid,stat,args
  ```

  Linux arm64, Go1.27.1: identical timeout at line315, 21.23s. PID7006,
  `ocman.test`, was `Z` with PPID1, whose command was `sleep infinity`.
  `kill -0 7006` returned0. The supervisor had exited, but container PID1 had
  not reaped it. A first container setup using a read-only module-cache mount
  failed during compilation with `mkdir /go/pkg/mod/cache: read-only file
  system`; the disposable container above corrected only that local setup.

## Cause and bounded correction

The test-only `provider-tmux` launches an orphan and equates `kill -0` success
with tmux session existence. A zombie still accepts that signal check. This
misclassifies an exited supervisor as alive under a non-reaping container PID1.
Production `ProviderRuntime` asks real tmux for session existence; it does not
use this PID heuristic. In the same disposable Linux container, real tmux3.3a
passed `new-session`/`has-session`/`kill-session`, then `has-session` returned1.
Commands used `tmux -L recovery-proof`, session `supervisor`, command `sleep 60`.
Only this local container installed tmux; repository runners were not changed.

The fixture now reads `ps -o stat= -p <pid>` and reports zombies or absent
processes as dead. `TestFakeTmuxAliveDistinguishesZombie` owns an exited child
without waiting, verifies `Z`, and calls the real runtime's Alive method against
the fixture. It also verifies running and reaped processes. The test failed
before the fixture correction on macOS and Linux with
`exited unreaped process: alive=true err=<nil>`, then passed on both.

Failing-first command on each platform was `go test . -run
'^TestFakeTmuxAliveDistinguishesZombie$' -count=1 -v`, with the new regression
added and `provider-tmux` still byte-identical to starting checkpoint1f57f706.
Both initial failures are in the session tool transcript. A separate audit
reproduction compiled the new test with `go test -c`, extracted only the original
fixture with `git archive 1f57f7069e6f1819c8276f0372478311e9a1bd92
internal/hostsvc/local/testdata/provider-tmux`, and ran the test binary from that
fixture directory. `mac-red.log` and `linux-red.log` retain the same failing
assertion. The Linux audit command also attempted the restart test, but that
attempt stopped at fake-CLI compilation because the fixture-only working
directory has no go.mod. It is not the original restart reproduction above.

The original supervisor restart/death/relaunch/resume assertions and all test
deadlines are unchanged. No production source, coverage policy, skip, exclusion,
suppression, retry policy, runner or cache was changed. Scope32 deferrals R1-R6
remain open. Approved replay/ack, observed CLI usage, official coverage obligation
and 4+2/runtime representations remain as recorded in `delivery-report.md`.

## Local reruns

macOS arm64, Go1.26.1. Go gates below use `GOMAXPROCS=4 GOFLAGS=-p=4`.
Tests use disposable data and fake Claude. Logs are under the session's temporary
`claude-supervisor-recovery` directory, outside the repository.

| Command | Result |
|---|---|
| `go test . -run '^(TestFakeTmuxAliveDistinguishesZombie\|TestClaudeSupervisorSurvivesOcmanRestart)$' -count=1 -v` | PASS on macOS, 3.380s; same Docker exec command PASS Linux, 1.594s. |
| `docker exec ocman-supervisor-recovery go test . -count=1 -v` | Full Linux root suite PASS, 16.576s. |
| Same Linux targeted command with `-race` | PASS, 6.377s. |
| `make lint` | PASS; Go0 issues, TypeScript/guards pass, ESLint0 errors/105 existing warnings. |
| `go test -count=1 ./...` | Full rerun PASS, root27.468s/server230.261s. First invocation was interrupted by the tool's 240s limit before the server result; rerun used a 600s tool limit. No test deadline changed. |
| `go test -race -count=1 . ./internal/hostsvc/local ./internal/ocruntime` | Host/runtime PASS49.294s/2.473s. Root FAIL61.418s at `TestStartupWithoutOpenCode`, `platform_startup_test.go:70`, "Failed to ensure instance identity: reading instance identity: context canceled". |
| `go test -race -count=1 .` | Standalone full-root rerun also FAIL51.202s with the same startup error. The test sends SIGTERM after1s, before identity initialization finishes in these runs. No repair or deadline change made; causality beyond this observation is unproven. |
| `go test -race -count=1 -run 'Claude\|Drain\|Browser\|Provider\|DualPlatform\|FakeTmux' .` | Relevant Claude root-race gate PASS33.474s. This does not erase or certify the two broader failures above. |
| `go build -o /dev/null .`; `git diff --check` | PASS. |
| `make coverage SUITE=frontend` | PASS418 files/4441 tests;90.17%. |
| `make coverage SUITE=go` | PASS49 internal test packages;87.5%. |
| Separate `make coverage-check BASELINE_DIR=<official>/coverage SUITE=frontend` | PASS90.17 vs89.38, +0.79, unchanged0.1 tolerance. |
| Separate `make coverage-check BASELINE_DIR=<official>/coverage SUITE=go` | PASS87.5 vs86.7, +0.80, unchanged0.1 tolerance. |

Current official baseline was fetched with `git fetch origin gh-pages` and
extracted with `git archive origin/gh-pages coverage/go.json
coverage/frontend.json`. Baseline ref `743b6eb5489bf08194da0466e00c8e6ec3af1217`
contains data for main `e243236a3caac87efe4bad0399de39876d1fdab6`.

## Checkpoint CI and handoff

Exact pushed SHA, new CI run and backend coverage results are pending until
push. They will be recorded in the Factory issue comment and PR834, avoiding a
self-referential commit hash here. Local results do not certify repository CI
or external review.
Any unresolved required gate needs a specific human recovery decision.
Factory must schedule fresh read-only verification before delivery.

# Docker Sandboxes as an ocman runtime: initial research

Date and source retrieval: **2026-09-21**.

## Recommendation

**No-go as a drop-in replacement for approved Factory revision 2. Go for a small, separately authorized compatibility probe.** Current `sbx` supplies local microVMs, same-path workspace mounts, arbitrary command execution, loopback port forwarding, and stop/restart persistence. It does not establish the required independent local data lifetime, 512-process limit, capability dropping, or no-new-privileges contract. Its documented agent environment deliberately permits sudo. Those are qualification/design gaps, not details to silently relax. [1][2][5][8][10][11]

The strongest reason to investigate is stronger host isolation plus managed networking. It would replace part of the runtime machinery, not most of the epic. Identity, API-fed history, scoped Factory authority, mise policy, and lifecycle orchestration remain ocman's responsibility.

## Scope and evidence

Research only. No Docker installation, sandbox execution, test-provider calls, Factory mutations, branch changes, or implementation changes were performed. This is the only report added. Location follows the existing `spec/beads-status-sidebar/research.md` convention. A research subagent inspected the primary sources; no runtime probes were executed.

Evidence labels used below:

- **D: documented** in current official Docker documentation or official release metadata. Not runtime-tested here.
- **C: code-confirmed** by reading public source. A parser capability is not proof of runtime enforcement.
- **I: inference** or proposed ocman integration derived from those facts.
- **P: probe required**, including facts not established by accessible sources.

Context7 was queried with `library "Docker Sandboxes"`, then `docs /docker/docs`, two commands total. Current official pages and public source were then read directly. `main` links are retrieval-date snapshots, not guarantees that all fields exist in release 0.43.0. The published CLI reference and prose already differ in places, so discovery against a pinned binary is a prerequisite.

Ocman baseline: the supplied Factory status says contracts/probes completed at `ca047aecb`, identity foundation at `213294d8b`, image work active, and sandbox dispatch disabled. No fresh Factory status query was made. Read-only inspection of the provided worktree's `docs/adr/0009-persist-worktree-sandboxes-with-api-fed-history.md` and `scripts/sandbox-contract/README.md` confirms the revision-2 contracts and records macOS Docker Desktop probe evidence, but no native Linux qualification. Those existing container probes do not qualify `sbx`.

## 1. Product generations, licensing, and supported hosts

| Question | Finding |
| --- | --- |
| Current product | **D:** Standalone `sbx`, requiring neither host Docker Desktop nor Docker Engine. Official latest release metadata reports **v0.43.0**, published September 15, 2026, `prerelease: false`. Stable, RC, and nightly channels exist. This is a stable release channel, not evidence of an API stability or production-support guarantee. Kits remain explicitly experimental. [1][3][10] |
| Older `docker sandbox` | **D, historical:** Docker's February 16 docs called it experimental, described microVMs on macOS/Windows, and directed Linux users to legacy container-based sandboxes in Desktop 4.57. Those microVM docs explicitly described bidirectional file copying, not mounts. Do not transfer those restrictions or sync semantics to current `sbx`, or assume all `docker sandbox` versions were container-based. [4] |
| Free/commercial | **D:** Current FAQ explicitly permits commercial and professional local `sbx` use without a per-seat fee, using a free Docker account. The product is proprietary, not an open-source runtime. Organization network/filesystem/MCP governance, sign-in enforcement, and audit logs require a separate paid subscription. [6][7] |
| Accounts and headless use | **D:** Docker sign-in is required. Browser OAuth is the normal path; automation supports a Docker PAT with at least Read scope through stdin. Linux without Secret Service automatically uses a permission-protected credential file. Offline operation and token-expiry grace were not established; unattended renewal needs a probe. [1][6][12] |
| Desktop licensing | **D:** Separate from `sbx`. Desktop is free for personal/education/noncommercial OSS and businesses with fewer than 250 employees **and** under $10M revenue; larger professional use and government require a paid subscription. Free commercial `sbx` does not waive Desktop terms if Desktop is also used. [13] |
| Cloud | **D/P:** `--cloud` dispatches to a separate Docker Cloud Sandboxes API. Some operations require account entitlements; named persistent volumes are cloud-only and experimental. No authoritative cloud compute price/free allowance was established from the inspected sources. Do not extrapolate free local use to cloud compute, or confuse Cloud Sandboxes with Docker Build Cloud/Offload. Cloud is outside the owner-local requirement. [5][8][14] |

Current installation requirements, **D**, from the installation page rather than generic Engine support: [1]

| Host | Official requirement | Consequence for ocman |
| --- | --- | --- |
| macOS | Sonoma 14+, Apple silicon | Intel Mac support is not listed. Desktop is unnecessary for running `sbx`. |
| Linux | Ubuntu 24.04+, amd64 or arm64, CPU KVM support enabled, user in `kvm` group; nested virtualization when itself virtualized | Having Docker Engine alone is insufficient. Ubuntu derivatives are explicitly not tested/supported. Release README offers Rocky Linux RPM instructions, but installation docs warn that artifact availability is not distribution qualification. |
| Windows | Windows 11, Intel/AMD 64-bit, Windows Hypervisor Platform | Windows Arm is not listed. This does not establish ocman Windows support; Windows is outside the epic's required release matrix. |

**I:** Replacing the runtime changes the approved dependency/qualification contract from Linux Engine/macOS Desktop to Ubuntu KVM/macOS Apple-silicon `sbx`. Testing on the same two machines is not evidence that the original Engine/Desktop runtime contract still holds. That change requires an explicit design decision.

## 2. Headless server and `ocruntime` fit

**D:** Docker's OpenCode integration launches `opencode` with no implicit flags and passes arguments after `--`. `sbx exec` supports arbitrary argv, explicit working directory/user/environment, stdin, and detached execution locally. It starts a stopped sandbox first. `sbx create` does not keep an unused local sandbox running indefinitely; it stops when no sessions keep it alive. The `run` description mentions detached mode, although the inspected generated option table omits that flag. [5][9][15]

**D:** Local published ports default to loopback. Explicit host IP and ephemeral host-port allocation are supported; `sbx ports --json` reports bindings. The service must listen on `0.0.0.0` or the appropriate guest interface. Mappings return on restart; an ephemeral host port changes, and a conflicting fixed port can trigger a prompt. Port forwarding is transport, not OpenCode HTTP authentication. [8][16]

**C:** Ocman's current `internal/ocruntime/ocruntime.go:23-59` has `Launch`, `Probe`, and `Stop`, a caller-selected host port, and an opaque runtime ID/endpoint. Its health/identity helpers use HTTP, not container presence.

**I:** A plausible adapter creates/reuses one explicitly named sandbox per canonical worktree, starts one pinned OpenCode HTTP server through direct exec, publishes only that API to loopback, and probes authenticated HTTP plus durable generation ownership. Sessions and nested subagents share that server; the main checkout can have its own sandbox. Native stays per-project/default. `Stop` maps to `sbx stop`, never `rm`. Keep the existing OpenCode platform adapter.

**P:** Prove that a detached server survives CLI exit and that restart, daemon restart, and host reboot recover exactly one writer without an interactive attachment. Reconcile guest port versus caller-selected host port; do not silently adopt ephemeral allocation without adapting the contract. Prove HTTP/SSE authentication and secret bootstrap, fail closed on name collisions, and inspect exit/log behavior. Built-in OpenCode TUI support alone proves none of these.

## 3. Checkout mounts and linked Git worktrees

**D:** Current direct workspaces use filesystem passthrough with Virtiofs caching, with no copy/sync process. Multiple explicitly supplied directories appear at identical absolute host paths. External symlinks are blocked; hard links inside an admitted workspace still expose their underlying file. [2][17]

**D:** Docker's documented host-worktree recipe mounts only the checkout and therefore has **no Git access**: the `.git` pointer cannot resolve. Clone mode is a private clone, rejects a linked-worktree source, changes the path/data model, and adds a host Git remote. It is not the approved worktree design. [9][18]

**I/P:** Supply exactly the canonical selected checkout and canonical common Git directory as two direct workspaces. This should let linked `.git`/`commondir` references resolve, but that combination is not qualified by Docker's worktree recipe. Test it. Never mount the main checkout merely to expose its `.git`, or either checkout's parent. Retain the accepted shared-Git-metadata trust limitation and host Git hardening. Test sibling/main checkout secret exclusion through direct paths and symlinks, host-visible commits, ownership, file locks, and concurrent host Git visibility. Docker specifically documents disabling Virtiofs caching if Git index corruption or unexpected contents occur. [9][18][19]

**I:** `--skills=off` is necessary to avoid the additional shared host skills mount. Excluding host home and sockets also requires dealing with automatic SSH forwarding, not just choosing workspace paths. [20][21]

## 4. Private data, SQLite, and replacement

**D:** In-sandbox files persist across stops/restarts. `sbx rm` deletes the VM and all private contents. Templates can capture a stopped sandbox, but capture filesystem secrets too, and agent configuration files are regenerated on creation. The docs do not promise SQLite-consistent backups or complete backup coverage for every attached block volume. [2][9][11]

**C:** Public kit `MountSpec` contains only `path`, `type`, `size`, and `mode`, with block and tmpfs types. It has no independently named existing-volume attachment. The CLI's separate `sbx volume` facility is explicitly cloud-only, snapshot-on-exit, last-exiter-wins for concurrent mounts. Local kit volumes must not be confused with that feature. [10][14][22]

**I/P, primary blocker:** Keeping an existing VM alive is not the same as durable private data independent of replaceable runtime generations. No supported local detach/reattach data-identity contract was established. Retaining the VM and upgrading a binary in place is possible in principle, but does not solve replacement after VM loss or rebuilding with a new base image. A verified consistent backup/restore route is needed before considering replacement.

Keep OpenCode **and application-development SQLite** in private guest storage, never in the direct-mounted checkout. Relocating only OpenCode's home is insufficient for applications that default to `./app.db`. Do not raw-copy active DB files, copy VM backing files, or treat `sbx cp`/template save as database backup APIs. Determine guest filesystem and WAL/locking/crash behavior with disposable fixtures. Ocman's API-fed host-local read model, offline history, native history source, source-qualified IDs, and sync-gap reporting remain mandatory.

**D:** Docker also documents daemon downgrade failures after internal DB schema upgrades; its documented recovery deletes sandbox data. Therefore pin and qualify the `sbx` version as well as the image, OpenCode, and mise. OpenCode rollback and `sbx` daemon rollback are separate problems. [19]

## 5. Images, setup, and least privilege

**D:** Custom OCI templates and local image-tar loading exist; Sandboxes has a separate image store. Mutable tags can pull updated layers at creation. Existing sandboxes do not automatically receive updated agents. Built-in defaults select `-docker` images, whose agent container is privileged **inside the microVM**, with a private Docker daemon. Non-Docker variants avoid that privileged mode. Template documentation lists Desktop as its build prerequisite even though sandbox execution needs no Desktop. A Linux Engine-only image build/import workflow remains to qualify. [11]

**D:** Kit image requirements explicitly include UID 1000 `agent` with passwordless sudo. Kit install commands default to root. Startup commands run on each start and do not gate the agent entrypoint. Repository environment files can contain host-executed commands. These defaults conflict with revision 2. [10][17]

**C:** The public kit `Security` struct exposes `privileged` only; resource types expose CPU/memory/GPU, not PIDs. The v2 parser constructs custom command argv and explicitly warns that `sandbox.build` is not executed by the runtime. No `cap_drop`, `no-new-privileges`, or PID-limit field exists in those inspected structs. This is a schema finding, not proof that no internal mechanism could ever provide them. [22][23]

**I/P:** Use an ocman-owned, digest-pinned, non-Docker template candidate with pinned OpenCode/mise and an explicit noninteractive command. Verify image digest support against the selected release. Removing sudo may conflict with bootstrap assumptions and is unqualified. Do not claim microVM host isolation satisfies the separately approved in-guest privilege restrictions. Ocman must still enforce opt-in committed mise configuration, explicit named task, setup failure gating, first-launch-only setup, and idle-only refresh. Avoid repository-controlled host lifecycle commands and implicit root package installation.

## 6. Network, credentials, and MCP authority

**D:** Local network rules, per-sandbox allow/deny, policy inspection/logging, credential proxying, mounts, and port selection are available without paid organization governance. TCP passes through the host proxy; external UDP/ICMP remain blocked even with an open preset. Noninteractive use needs a preselected policy. Organization allow rules supersede local allows; local denies can narrow them. [6][7][24]

**I:** Ordinary outbound TCP is feasible, but not byte-for-byte ordinary Engine networking. Keep native ocman web/MCP ports explicitly unreachable. `host.docker.internal` is translated to host localhost by the proxy, so a broad localhost allowance would expose loopback-trusted services through host-originated connections. Permit only the dedicated scoped endpoint. [16]

**D:** Service credentials are global by default; sandbox-scoped values and host-side header injection exist. Custom package/provider secrets are experimental. Built-in OpenCode credentials do not include Zen, which uses the custom-secret path. Proxy-managed values can remain host-side, but this still grants the sandbox credential *use*. [15][21]

**D, important default:** SSH-agent forwarding is on by default and can use either the creating/joining client's `SSH_AUTH_SOCK` or a configured fixed socket. Disabling it is a daemon setting with restart requirements. Shared skills are also mounted by default. [20][21]

**I/P:** Admit only selected provider/package authority, with no global forge credentials or SSH signing/authentication. Prove isolation from pre-existing user `sbx` secrets, fixed SSH sockets, and other clients joining a sandbox. Simply clearing ocman's `SSH_AUTH_SOCK` is insufficient. Do not silently change the user's global daemon settings to make ocman pass.

**D:** The MCP gateway normally allows dynamic discovery/attachment of registered servers; static mode suppresses those discovery tools. Local stdio MCP servers execute on the host. HTTP header secrets are global per registration and existing connections require restart to pick up rotation. Paid MCP governance does not supply ocman-specific authorization. [25]

**I/P:** Retain independent authenticated sandbox/generation/session/Factory-attempt scope, revocation, and fixed host push/PR operations. Never register or proxy ocman's ambient loopback-trusted MCP. A sandbox-wide gateway registration is not a demonstrated per-session/attempt credential boundary. Prefer proving the approved dedicated TLS endpoint and session bridge first; verify generated OpenCode configuration cannot additionally attach unrelated host servers. No application ports should be auto-published by kits or project environment files.

## 7. Limits, lifecycle, and observability

- **D/P:** Explicit local `--cpus 2 --memory 4g` is documented. Local defaults are all host CPUs and roughly half host RAM, not the cloud's 2 CPU/4 GiB default. Verify enforced limits and VM overhead. No 512-PID control was found in inspected CLI/schema. The normative kit spec additionally calls kit-resource enforcement best-effort/pending; use CLI limits as the probe candidate, not parser acceptance as evidence. [5][10][22][26]
- **I:** Capacity two, visible waiting, 30-minute idle stop, persisted Keep running, setup/turn/Factory inhibitors, and no native fallback remain ocman policy. Session attachment lifetime is not application idleness. Cloud TTL is neither a local idle timer nor a substitute. [5][9]
- **D/P:** `sbx ls --json`, `ports --json`, a CPU/memory dashboard, network policy logs, and JSON diagnostics exist. Dedicated stable agent-log/event APIs and graceful stop timing were not established. Capture server logs privately and qualify crash/restart reconciliation. Diagnostics uploads are optional and may include user content. [9][19][24][27]

## 8. What changes in the epic

| Could replace, after qualification | Must remain |
| --- | --- |
| Engine container creation/start/stop mechanics; microVM boot; workspace passthrough; API port-forward plumbing; optional provider proxy | Durable sandbox/data identity separate from runtime/endpoint; single-writer generation ownership and recovery |
| Some network transport and image-loading operations | Authenticated HTTP/SSE; API-fed retained history and native-history preservation |
| Some environment packaging/bootstrap primitives | Ocman image/version policy; mise opt-in/task/failure/refresh state; explicit backup/upgrade/rollback |
| Some host-boundary isolation mechanisms | Scoped MCP and typed host delivery; no forge/SSH authority; capacity/idle/Keep running; unsupported Routine and cross-runtime fork/move gates |

**I:** Keep the completed contracts and identity foundation. Do not discard active image work on the strength of this research. A switch would need explicit revision of runtime qualification and any accepted privilege/data guarantees, followed by new evidence. Nothing here authorizes that revision or enables dispatch.

## Smallest next proof of concept, not executed

1. **Contract gate first.** Ask Docker for supported local data replacement/reattachment, cap-drop/no-new-privileges, and 512-PID controls for the pinned release. If strict revision 2 cannot be met, stop or request a design decision before integration. Do not build an extra nested container layer merely to rescue the comparison.
2. **One disposable fixture on each required host.** Use an authorized Ubuntu/KVM host and Apple-silicon macOS host, pinned `sbx`, synthetic repo with a linked worktree and sibling secret, and a pinned ocman image candidate. No real provider/package credentials, live databases, or forge access. Record actual CLI flags and bootstrap privilege requirements.
3. **Server/mount test.** Mount only checkout plus common Git metadata, disable extra host authority, run one authenticated OpenCode HTTP server detached, and expose only its loopback API. Test nested session ownership using provider-free fixtures, wrong/missing auth, sibling/home/socket/SSH exclusion, host-visible commits, actual CPU/RAM/PID limits, stop/resume, daemon restart, and CLI disappearance. If keeping it running requires an interactive session, record that as a failure of the intended adapter contract.
4. **Data and authority test.** Create private OpenCode/app SQLite fixtures, exercise a consistent backup and replacement/rollback path without DB bind mounts or raw copying, then confirm API history continuity. Use a fake scoped TLS MCP server to reject other sessions/attempts and native host endpoints. Exercise capacity/idle behavior through a small external driver, not Factory mutations.

Proceed to an adapter proposal only if these tests pass on both hosts. The highest-value early exit is discovering that independent local data retention or strict least privilege cannot be supported without redesign.

## Sources

All URLs retrieved 2026-09-21. Historical sources are explicitly pinned; current `main` sources may advance independently of binaries.

[1]: https://docs.docker.com/ai/sandboxes/install/ "Current installation and platform requirements"
[2]: https://docs.docker.com/ai/sandboxes/architecture/ "Current passthrough, storage, and lifecycle"
[3]: https://api.github.com/repos/docker/sbx-releases/releases/latest "Latest release metadata, v0.43.0 at retrieval"
[4]: https://github.com/docker/docs/blob/87fa1fa09b9b500db0c1f46ddb15a4393d9006b4/content/manuals/ai/sandboxes/architecture.md "Historical docker sandbox architecture; accompanying _index.md documents platform restrictions"
[5]: https://github.com/docker/docs/blob/main/data/sbx_cli/sbx_create.yaml "Create flags, resource limits, cloud-only options"
[6]: https://docs.docker.com/ai/sandboxes/faq/ "Free commercial use, Docker account, telemetry, headless secrets"
[7]: https://docs.docker.com/ai/sandboxes/governance/ "Free local versus paid organization governance"
[8]: https://github.com/docker/docs/blob/main/data/sbx_cli/sbx_ports.yaml "Loopback defaults and distinct cloud port behavior"
[9]: https://docs.docker.com/ai/sandboxes/usage/ "Execution, workspace modes, lifecycle, persistence"
[10]: https://docs.docker.com/ai/sandboxes/customize/kit-reference/ "Experimental kits, agent requirements, setup, block volumes"
[11]: https://docs.docker.com/ai/sandboxes/customize/templates/ "Images, privileges, pinning considerations, save/load limitations"
[12]: https://docs.docker.com/ai/sandboxes/workflows/automation/ "Headless Docker PAT authentication"
[13]: https://docs.docker.com/subscription/desktop-license/ "Separate Docker Desktop commercial licensing"
[14]: https://github.com/docker/docs/blob/main/data/sbx_cli/sbx_volume.yaml "Cloud-only snapshot-based persistent volumes"
[15]: https://docs.docker.com/ai/sandboxes/agents/opencode/ "OpenCode integration, argv passthrough, providers"
[16]: https://docs.docker.com/ai/sandboxes/workflows/development/ "Guest bind address, restart port behavior, host localhost proxy"
[17]: https://docs.docker.com/ai/sandboxes/security/isolation/ "Workspace, symlink/hard-link, host-command and privilege boundaries"
[18]: https://docs.docker.com/ai/sandboxes/workflows/git/ "Host worktree lacks Git metadata; clone semantics"
[19]: https://docs.docker.com/ai/sandboxes/troubleshooting/ "Git cache risks, daemon downgrade data loss, diagnostics"
[20]: https://docs.docker.com/ai/sandboxes/security/defaults/ "Default shared skills, sudo, private Docker"
[21]: https://docs.docker.com/ai/sandboxes/configuration/credentials/ "Global/scoped credentials, SSH forwarding defaults, package secrets"
[22]: https://github.com/docker/sbx-kits-contrib/blob/main/spec/types.go "Read public Security, Resources and MountSpec types"
[23]: https://github.com/docker/sbx-kits-contrib/blob/main/spec/v2.go "Read command construction and unsupported build handling"
[24]: https://docs.docker.com/ai/sandboxes/governance/access-controls/local/ "Local free policy, presets, headless initialization, precedence"
[25]: https://docs.docker.com/ai/sandboxes/mcp-gateway/ "Dynamic/static gateway, host stdio execution, global header secrets"
[26]: https://github.com/docker/sbx-kits-contrib/blob/main/spec/SPEC-v2.md "Normative grammar and runtime-support caveats"
[27]: https://github.com/docker/docs/blob/main/data/sbx_cli/sbx_ls.yaml "Machine-readable runtime inventory"

Additional directly inspected official sources: [release channels and proprietary license](https://github.com/docker/sbx-releases/blob/main/README.md), [copyright license file](https://github.com/docker/sbx-releases/blob/main/LICENSE), [release repository contents](https://api.github.com/repos/docker/sbx-releases/contents/), [historical platform/experimental status](https://github.com/docker/docs/blob/87fa1fa09b9b500db0c1f46ddb15a4393d9006b4/content/manuals/ai/sandboxes/_index.md), [exec reference source](https://github.com/docker/docs/blob/main/data/sbx_cli/sbx_exec.yaml), [run reference source](https://github.com/docker/docs/blob/main/data/sbx_cli/sbx_run.yaml), [stop reference source](https://github.com/docker/docs/blob/main/data/sbx_cli/sbx_stop.yaml).

The public release repository contains release documentation, not the sandbox daemon implementation. Public kit parser code was inspected where it could settle schema questions; VM lifecycle, storage reattachment, proxy enforcement, and guest privilege behavior remain documented claims or explicit probes, not code-confirmed runtime guarantees.

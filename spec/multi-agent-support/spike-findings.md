# Claude CLI spike findings

Factory Issue `claude-code-provider.1.3.1`, measured 2026-10-05.
This is a manual spike, not a provider implementation or an automated CLI test.

## Setup and limits of the measurements

- Native installed **Claude Code 2.1.220**, invoked by its versioned binary
  path. Reauthorization updated the default `claude` to 2.1.289; results from
  that preliminary run were discarded. These findings and fixtures use 2.1.220.
- macOS 26.6.2, arm64, Mac16,5, 36 GiB RAM. Subscription authentication through
  the CLI's existing login, explicitly approved by the owner. Initial attempts
  failed with expired OAuth; Factory recovery `claude-code-provider.1.3.22`
  supplied a refreshed login. Ocman never read or copied credentials.
- Disposable project outside the checkout, no ocman startup or application
  database access. Claude's own spike transcripts were saved in its normal
  history so `--resume` could be measured. Plugin installation used a separate,
  disposable `CLAUDE_CONFIG_DIR`, without a login.
- Manually invoked Python stdlib subprocess driver, separate stdin/stdout
  pipes, newline-delimited JSON, monotonic timestamps from process launch.
  `ps -o rss= -p <pid>` sampled every 0.5 seconds; KiB divided by 1024 gives MiB.
  RSS covers the Claude PID, not Bash/Python children or ocman.
- Haiku for ordinary turns, Sonnet for the successful subagent experiment.
  No custom system prompt, no `--bare`, no permission bypass, no MCP servers.
  `--setting-sources ''` excluded user/project/local settings, but did not
  remove the CLI's built-in skills or authentication.

Working launch command, with `$CLAUDE_221` pointing to the installed 2.1.220
binary and the working directory set to the disposable project:

```sh
DISABLE_AUTOUPDATER=1 "$CLAUDE_221" -p --verbose \
  --input-format stream-json --output-format stream-json \
  --include-partial-messages --replay-user-messages \
  --permission-prompt-tool stdio --strict-mcp-config \
  --setting-sources '' --model haiku
```

Keep stdin open between turns. Closing it after a completed turn lets the
process exit. The AD-6 command without `--verbose` exits with:

```text
Error: When using --print, --output-format=stream-json requires --verbose
```

The committed [fixtures](../../internal/claudeprovider/testdata/2.1.220/) are anonymised
message excerpts from real pipe captures. They preserve input/output direction,
timings, usage and identifier relationships. Their README documents omissions
and an offline validation command. Nothing in these checks runs Claude.

## 1. Idle and busy RSS, pool policy

One plain process, one foreground-subagent process, and one permission wait:

| State | Launch-relative interval, seconds | Samples | RSS min / mean / max, MiB |
|---|---|---:|---|
| Idle before first input | 2 to <5 | 6 | 348.23 / 348.23 / 348.23 |
| Busy, first plain API turn | 5 to 6.305 | 3 | 360.98 / 361.69 / 362.63 |
| Idle after first result | 6.4 to <11.3 | 10 | 319.69 / 332.50 / 363.88 |
| Busy with foreground subagent | 5 to <9.8 | 9 | 276.09 / 310.39 / 332.34 |
| Waiting for permission | 4 to <183 | 351 | 260.98 / 275.68 / 359.19 |

Plain-process peak over all 36 samples was **363.88 MiB**. The subagent
process peaked at **343.94 MiB** over 21 samples. A subagent did not require
another Claude PID. RSS falls after garbage collection; "idle" is not a
negligible-memory state. These are short, small-context runs, not an upper
bound for large histories, parallel subagents or tool subprocesses.

Recommend **4 live Claude children per owner machine**, shared across project
supervisors, and a **600-second idle timeout**. A per-project-only cap would
multiply memory by the number of open projects. Budgeting roughly 400 MiB per
child gives 1.56 GiB for four, before ocman and tool children. Four also limits
simultaneous subscription consumption. Keep this cap configurable for machines
with less RAM or large histories.

At capacity, evict an already-idle child before admitting another session.
If every slot is active or waiting on a permission/question, wait for capacity;
never kill a waiting prompt to enforce the idle timeout. Start the timeout at
turn completion, not the last stdout byte. Ten minutes retains fast follow-ups
while releasing hundreds of MiB for abandoned sessions. The cache experiment
below shows that releasing the process does not itself discard the API cache.
This supplies NFR-2's numbers and retains AD-3's approximately ten-minute policy.

## 2. Resume latency and prompt cache

Created with `--session-id <uuid>`, completed two plain turns, closed stdin and
waited for exit, then started fresh processes with `--resume <same uuid>`.
Each resumed prompt asked for the previously returned `SPIKE_OK` marker, and
each returned it correctly.

| Run | Launch to system init, s | Launch to API message_start, s | Launch to result, s | Cache-read / cache-created tokens |
|---|---:|---:|---:|---:|
| First turn, existing process | Already running | N/A | 1.292 after input | 0 / 25,624 |
| Second turn, same process | Already running | N/A | 0.906 after input | 25,624 / 90 |
| Resume 1 | 0.369 | 1.109 | 1.979 | 25,714 / 77 |
| Resume 2 | 1.046 | 1.767 | 3.209 | 25,791 / 142 |
| Resume 3 | 0.349 | 1.171 | 1.715 | 25,933 / 192 |
| Resume 4 | 0.378 | 1.123 | 2.627 | 26,125 / 96 |

Four fresh resumes: result median **2.303 s**, range **1.715 to 3.209 s**.
System-init median **0.374 s**. Resume 2's API request was about **14.36 minutes**
after Resume 1's API request, with no process kept alive between them, and still
hit the cache. Reported creation was `ephemeral_1h_input_tokens` for these parent
turns. The subagent separately reported five-minute cache creation.

The cache survives a process restart and this measured gap. This does not
promise cache hits after an hour, after changed settings/tools/system prompts,
or under a different model/account. CLI `duration_api_ms` can include work
beyond a single request; use measured launch-relative time for resume latency.
`result.usage` is turn usage, whereas `modelUsage` and `total_cost_usd` accumulated
over the two turns in the same process. Do not sum those cumulative totals as
if they were per-turn usage. A fresh resumed process reported fresh cost totals.

## 3. Stdin while a turn is running

Two experiments establish that input is accepted and queued, with different
turn boundaries depending on when it arrives:

1. While Bash was running `sleep 8`, wrote a second user message at **4.319 s**.
   Its `command_lifecycle` became `queued` at **4.326 s**, then `started` at
   **11.346 s**, after the tool completed. The CLI produced **one** result at
   **13.364 s**, text `FIRST_DONE\n\nSECOND_DONE`. Both commands completed.
   The second input joined the original agent loop before its next API request.
2. While the model streamed integers, wrote a second message on the first text
   delta at **2.008 s**. It became `queued` immediately. The original result
   arrived at **3.245 s**; the second command started at **3.247 s** and produced
   its own result at **4.120 s**, text `SECOND_GENERATION`.

There was no rejection, cancellation or modification of a model response already
streaming. "Queued" here does **not** mean ocman's hold-for-next-turn guarantee.
Plain Enter may join a running tool turn; Ctrl/Cmd+Enter must remain in ocman's
durable queue until the actual idle edge. Do not mark the session idle for each
`command_lifecycle.completed`, since multiple inputs can share one result.
This resolves AD-6's mid-turn assumption and supports FR-4's separate gestures.

## 4. `!cmd` shell input

Sent this ordinary stream-json user input:

```json
{"type":"user","message":{"role":"user","content":"!printf 'SPIKE_SHELL\\n'"},"parent_tool_use_id":null,"session_id":"","uuid":"00000000-0000-4000-8000-000000000001"}
```

It **did not invoke the interactive shell shortcut**. The model received the
literal input, made a normal `Bash` tool call with
`{"command":"printf 'SPIKE_SHELL\\n'","description":"Print SPIKE_SHELL to verify shell is working"}`,
then answered "Shell is working. What would you like to do?". Result time
**4.352 s**, two API iterations, **212 output tokens**, **$0.0118519** reported
API-equivalent cost. Bash output was `SPIKE_SHELL`.

The command happened to execute, but through model interpretation, not a shell
protocol. FR-4 / AD-2's shell operation needs an explicit owner-local execution
path; forwarding `!cmd` as a user prompt is not a correct implementation.
No native shell control request was established by this spike.

## 5. Control protocol, usage and account

Every message is one JSON line. Control requests contain `request_id` and a
`request` object. Responses nest `request_id` inside `response`. Tool decision
keys mix snake_case in requests with camelCase in the approval payload.

### Initialize and account

Measured request:

```json
{"type":"control_request","request_id":"init-1","request":{"subtype":"initialize","hooks":{},"sdkMcpServers":[],"sdkCapabilities":{}}}
```

Successful response's `response.response` has these keys:
`commands`, `agents`, `models`, `available_output_styles`, `output_style`,
`account`, `pid`, `fast_mode_state`, `fast_mode_disabled_reason`,
`ide_rc_auto_enable_gate`, `remote_control_auto_enable`,
`remote_control_auto_on_by_default`.
Its captured account object, with only identity anonymised:

```json
{"email":"spike@example.invalid","organization":"Example","subscriptionType":"Claude Team","apiProvider":"firstParty"}
```

`account_info` and `get_account_info` requests returned
`{"type":"control_response","response":{"subtype":"error","request_id":"account-1","error":"Unsupported control request subtype: account_info"}}`
and the same error naming `get_account_info`, respectively.
Use initialize's `account`, not a guessed account operation. Passing `{}` for
`sdkMcpServers` was rejected with
`initialize: sdkMcpServers and webSearchIsolationExemptMcpServers must be arrays of strings`.
Identifiers in the inline examples are shortened for readability.

### Tool approval

The complete `can_use_tool` Bash request, `permission_suggestions`, allow reply,
CLI echo and tool result are in `permission.jsonl`. Approval measured:

```json
{"type":"control_response","response":{"subtype":"success","request_id":"permission-1","response":{"behavior":"allow","updatedInput":{"command":"python3 -c \"from pathlib import Path; Path('permission-marker.txt').write_text('SPIKE_PERMISSION')\"","description":"Create permission marker file"}}}}
```

The CLI echoes this control response back on stdout. Correlate it by direction
and request id, rather than treating it as a second user decision. This spike
approved once; it did not return `updatedPermissions` or persist CLI rules.

### Interrupt and permission mode

While a Bash permission was unresolved, sent:

```json
{"type":"control_request","request_id":"abort-1","request":{"subtype":"interrupt"}}
```

Within **1 ms**, stdout contained:

```json
{"type":"control_cancel_request","request_id":"permission-1"}
{"type":"control_response","response":{"subtype":"success","request_id":"abort-1","response":{"still_queued":[]}}}
```

A result followed in **2 ms**, `is_error:true`,
`subtype:"error_during_execution"`, `terminal_reason:"aborted_tools"`, with the
cancelled Bash in `permission_denials`. Its intended file was not created.
The process remained available. Both of these mode requests succeeded:

```json
{"type":"control_request","request_id":"mode-1","request":{"subtype":"set_permission_mode","mode":"default"}}
{"type":"control_response","response":{"subtype":"success","request_id":"mode-1","response":{"mode":"default"}}}
{"type":"control_request","request_id":"mode-2","request":{"subtype":"set_permission_mode","mode":"plan"}}
{"type":"control_response","response":{"subtype":"success","request_id":"mode-2","response":{"mode":"plan"}}}
```

The latter also emitted `system/status`, `status:null`, `permissionMode:"plan"`.
It is not an idle edge. Mode success does not prove that every tool will ask
ocman; built-in safe commands such as `sleep 8` proceeded without `can_use_tool`.
Ocman's own rules still decide requests that reach it, as AD-7 specifies.

### Usage and rate limits

Actual unsolicited rate-limit message, also retained in the fixtures:

```json
{"type":"rate_limit_event","rate_limit_info":{"status":"allowed_warning","resetsAt":1791360000,"rateLimitType":"seven_day","utilization":0.76,"isUsingOverage":false,"surpassedThreshold":0.75},"uuid":"00000000-0000-4000-8000-000000000002","session_id":"00000000-0000-4000-8000-000000000003"}
```

`resetsAt` is Unix **seconds**, and utilization is a fraction. This warning did
not block any turn. `result.usage` has `input_tokens`, `output_tokens`,
`cache_read_input_tokens`, `cache_creation_input_tokens`, nested `cache_creation`,
`server_tool_use`, `service_tier`, `inference_geo`, `iterations`, and `speed`.
`modelUsage` uses camelCase. Preserve unknown fields; do not reject new event
types such as `command_lifecycle` or `system/task_updated`.

Sending `/usage` as a user message returned a local `result` in **0.457 s**,
`num_turns:0`, `duration_api_ms:0`, and human-readable text reporting **28%**
current-session utilization, **76%** weekly utilization and **33%** weekly
Fable utilization, with localized reset times. It did not emit a structured
snapshot of both five-hour and weekly windows. Initialize supplied the plan
name but no utilization. A structured all-window account snapshot has therefore
not been established; `/usage` text is a measured fallback, with locale-dependent
parsing if numeric fields are required by AD-2 / AD-12 / FR-7.

No real quota rejection occurred. We did not exhaust the user's subscription
to manufacture one. Current official SDK types document `allowed`,
`allowed_warning`, `rejected`, plus reset/utilization fields, but the exact
2.1.220 rejected-event/result ordering remains unmeasured. Do not label a warning
as `limited` or invent a recorded rejection fixture. FR-7's hard-limit lifecycle
needs a later naturally limited manual capture; until then it is a compatibility
risk, not verified behaviour.

## 6. Permissions, questions and subagents

- **Timeout:** a Bash `can_use_tool` stayed unresolved for **180.002 s** and
  accepted the eventual reply. Tool execution then completed normally.
  It remained pending for approximately three minutes and accepted the reply.
  No infinite-wait guarantee was proved. Pending prompts must not be
  counted as idle by ocman's pool. AD-4 / AD-7a's reconnection design remains
  plausible with the supervisor holding the same live pipe.
- **Subagent origin:** the foreground child's request had
  `agent_id:"a000000000000000d"` and its own `tool_use_id`. The preceding
  `system/task_started` linked that `task_id` to the parent's Agent tool id.
  Child `assistant` and tool-result messages carried the parent's
  `parent_tool_use_id` and the **top-level** `session_id`, not a new child
  session id. Map these identifiers explicitly for FR-3 / AD-7's bubbling.
- **Tool name:** init advertised `Task`, but `ToolSearch` with `select:Task`
  returned a reference to `Agent`; the recorded tool call was named `Agent`.
  A Haiku attempt confused it with `TaskCreate` and did not launch a child.
  The successful Sonnet trace is the committed fixture. Do not match only the
  literal name `Task` in the converter.
- **Live child transcript:** the committed run added `--forward-subagent-text`.
  It forwarded child thinking, the Bash call/result and `SUBAGENT_DONE` text.
  A comparison run without the flag completed its child at **9.455 s** and
  its parent turn at **10.820 s**, forwarding the child Bash call/result but
  no child text or thinking.
  Child assistant-message counts were three with the flag and one without.
  AD-6's base flag list therefore provides child tool events but not the live
  text/thinking transcript; add the flag for that rendering.

### Exact AskUserQuestion exchange

Captured request, with identifiers shortened only:

```json
{"type":"control_request","request_id":"question-1","request":{"subtype":"can_use_tool","tool_name":"AskUserQuestion","display_name":"AskUserQuestion","input":{"questions":[{"question":"Which marker?","header":"Marker","options":[{"label":"Alpha","description":"first marker"},{"label":"Beta","description":"second marker"}],"multiSelect":false}]},"tool_use_id":"toolu_question1","requires_user_interaction":true}}
```

Measured reply:

```json
{"type":"control_response","response":{"subtype":"success","request_id":"question-1","response":{"behavior":"allow","updatedInput":{"questions":[{"question":"Which marker?","header":"Marker","options":[{"label":"Alpha","description":"first marker"},{"label":"Beta","description":"second marker"}],"multiSelect":false}],"answers":{"Which marker?":"Alpha"}}}}}
```

The CLI echoed the reply and the assistant confirmed Alpha. Answer map keys are
the **question text**, not the header or option index. Preserve the original
questions in `updatedInput`; do not send only `answers`. This confirms AD-7a's
question-through-tool flow for the single-choice case. Multi-select/rejection
semantics were not measured in this spike.

### PermissionRequest hook

**It exists and fired**, even in this stream-json run with stdio permissions.
A local plugin's command hook appended this payload:

```json
{"session_id":"session-1","transcript_path":"/spike/config/projects/spike/session-1.jsonl","cwd":"/spike/project","prompt_id":"prompt-1","permission_mode":"default","hook_event_name":"PermissionRequest","tool_name":"Bash","tool_input":{"command":"python3 -c \"from pathlib import Path; Path('plugin-marker.txt').write_text('PLUGIN_TOOL_OK')\"","description":"Write plugin marker file"},"permission_suggestions":[{"type":"addRules","rules":[{"toolName":"Bash","ruleContent":"python3 -c \"from pathlib import Path; Path('plugin-marker.txt').write_text('PLUGIN_TOOL_OK')\""}],"behavior":"allow","destination":"localSettings"}]}
```

The hook exited 0 with `{}` and the stdio `can_use_tool` was also delivered.
The trace put `can_use_tool` and hook-start at 4.611 s, approval at 4.611 s,
its echo at 4.613 s, and hook completion at 4.643 s. The hook log did not
timestamp its append, so the append's ordering relative to approval is unproved.
This proves hook existence and notification, not terminal-session approval or
hook timeout semantics. Keep terminal sessions read-only per FR-6's scope.

## 7. Local marketplace and plugin-dir

Created a minimal local marketplace:

```text
market/.claude-plugin/marketplace.json
market/plugin/.claude-plugin/plugin.json
market/plugin/skills/marker/SKILL.md
market/plugin/hooks/hooks.json
market/plugin/hook.py
```

Marketplace manifest:

```json
{"name":"ocman-spike-market","owner":{"name":"Spike"},"plugins":[{"name":"ocman-spike","source":"./plugin","description":"Disposable protocol spike plugin"}]}
```

Plugin manifest:

```json
{"name":"ocman-spike","version":"0.0.1","description":"Disposable protocol spike plugin"}
```

With `CLAUDE_CONFIG_DIR` set to the disposable config directory for every command:

```sh
"$CLAUDE_221" plugin marketplace add "$SCRATCH/market"
"$CLAUDE_221" plugin install ocman-spike@ocman-spike-market --scope user
"$CLAUDE_221" plugin list --json
"$CLAUDE_221" plugin uninstall ocman-spike@ocman-spike-market --scope user
"$CLAUDE_221" plugin marketplace remove ocman-spike-market
```

Add, install, list, uninstall and marketplace removal succeeded. List reported
`enabled:true`, `scope:"user"`,
`version:"0.0.1"`, and an install path under the disposable config's
`plugins/cache/ocman-spike-market/ocman-spike/0.0.1`. Local marketplace
registration is a separate required step before installation; a direct directory
is not the plugin argument used here. No fallback settings editor is needed.

Separately launched with `--plugin-dir "$SCRATCH/market/plugin"`, using the
existing CLI login and no user-scope installation there. The init catalog
included `ocman-spike:marker`; sending `/ocman-spike:marker` returned
**PLUGIN_SKILL_OK** at **2.761 s**. Command hooks referenced
`python3 "${CLAUDE_PLUGIN_ROOT}/hook.py"` and fired for **SessionStart**,
**PreToolUse**, **PermissionRequest**, and **Stop**. The tool turn created its
marker and completed at **6.104 s**. Thus `--plugin-dir` loads **both skills
and hooks**; it is not a skills-only channel. Ignore/deduplicate owned-session
hook status as AD-11 already requires.

## Design effects and remaining compatibility risks

| Spec | Finding / action |
|---|---|
| AD-6 | Add required `--verbose`; request `--forward-subagent-text` for live child transcripts. |
| AD-2, AD-6, FR-4 | `!cmd` is model input, not a native shell operation. Implement shell explicitly on the owner. |
| AD-6, FR-4 | Mid-tool input can join the same turn; use ocman's queue for the hold gesture. |
| AD-7, AD-9, FR-3 | Handle `Agent` as well as advertised `Task`; correlate `agent_id`, task ids and parent tool ids. |
| AD-7a, AD-4 | Prompt waits survived 180 s and interrupt cancelled them. An unbounded wait and restart/reconnect were not proved by this spike. |
| AD-2, AD-12, FR-7 | Account is in initialize; both-window utilization was only available as `/usage` text. Hard quota rejection was not captured. |
| AD-11, FR-6 | Register the local marketplace first. Both loading routes work; plugin-dir also executes hooks. |
| AD-3, NFR-2 | Default owner-wide cap 4; idle timeout 600 s; never evict active/prompt-waiting children. |
| AD-5, NFR-5 | Only 2.1.220 was measured. Do not infer a supported version range from this single version. |

The direct-pipe supervisor design can work. The literal AD-6 launch command
cannot work without `--verbose`, and native `!cmd` forwarding cannot implement
shell semantics. These corrections and unresolved protocol assumptions are
recorded in [deviations.md](deviations.md). No provider code was changed here.

## Where the findings landed (1.3.61 re-audit)

The measurements above are unchanged; this maps each design-effects row to
the implementation after recovery. Details are in `deviations.md`.

| Row | Outcome |
|---|---|
| AD-6 flags | `--verbose` and `--forward-subagent-text` are passed (`internal/claudeprovider/pool.go`). |
| `!cmd` | No shell operation; `shellExec` is false and `!cmd` is a prompt. |
| Mid-tool input | Ocman's queue holds Ctrl/Cmd+Enter; plain sends stay open in the turn until the CLI's replay echo (when the echo is emitted is assumed, not measured). |
| Agent/Task | Both map to `task`; live and stored subagents render under their call. |
| Prompt waits | Pending prompts survive reconnect and an ocman restart (fake CLI). Waits beyond 180 s remain unmeasured. |
| Account and utilisation | Plan from initialize; utilisation only from observed `rate_limit_event`s (approved FR-7 departure). No hard rejection was captured; the limit fixture is synthetic. `/usage` text is not parsed. |
| Plugin loading | Local marketplace install; `--plugin-dir` for launched sessions, which also loads the hooks (ignored for owned sessions). |
| Cap and idle | Cap 4 machine-wide across project supervisors, 600 s idle. |
| Versions | Only fixture-backed versions are accepted: `2.1.220`. |

## Documentation cross-check

Used Context7's official Claude Code documentation to check the launch/plugin
flags and question replies, then relied on the actual 2.1.220 captures above.
Current docs are not a substitute for version-specific evidence:

- [Headless streaming](https://code.claude.com/docs/en/headless)
- [SDK user input](https://code.claude.com/docs/en/agent-sdk/user-input)
- [Local plugins](https://code.claude.com/docs/en/plugins/create)
- [Official Python SDK types](https://github.com/anthropics/claude-agent-sdk-python/blob/main/src/claude_agent_sdk/types.py), consulted for documented rate-limit statuses only.

## Verification

- All three fixture files parsed and matched the anonymised original captures
  exactly. Permission reply ids, tool-result ids, parent-tool links and subagent
  task ids were checked, including the 180.002-second reply interval.
- Inline JSON examples and relative document links passed offline checks.
- Standards and spec reviews completed; their findings were corrected.
- `go test ./...` passed. The first run hit the existing terminal isolation
  test's two-second deadline; its isolated rerun and the full backend rerun passed.
- `pnpm test` passed all **318 files / 3,044 tests** after installing the missing
  worktree dependencies with `pnpm install --frozen-lockfile`.
- `git diff --check` passed. Application source and coverage were unchanged.

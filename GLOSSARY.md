# Ocman glossary

Use these names in product copy, documentation, and design discussions. Quote
the exact label when giving UI directions. Keep API fields, routes, and native
tool names as written when discussing those interfaces.

This is the application-wide reference. [CONTEXT.md](CONTEXT.md) defines the
finer distinctions in Factory, permission evaluation, and plugins. For package
ownership and data flow, see the [repository map](.agents/repository.md) and
[architecture guide](docs/other/architecture.md).

## Application and ownership

| Term | What it is |
| --- | --- |
| Ocman | The application for managing coding-agent sessions and coordinating software delivery. `ocman` is the executable. |
| Dashboard | The browser UI for an Ocman hub. The desktop app wraps the same UI; the progressive web app (PWA) installs that UI from a browser. |
| Machine | Where Ocman, agent processes, files, and credentials run or live. Use **this machine** for the hub's machine in user-facing instructions. |
| Ocman instance | A running Ocman service. A machine is where it runs; an instance is the service itself. Qualify other uses of *instance*, such as *agent runtime instance*. |
| Hub | The Ocman instance serving the dashboard. It routes work to the owner rather than running remote work itself. |
| Remote machine / remote Ocman instance | Another machine attached to the hub, or the Ocman service on it. **Remotes** is the settings label. A remote machine is unrelated to a Git remote. |
| Owner | The Ocman instance responsible for an object or operation. Use *owning machine* when explaining where the work runs or where the data lives. A remote can own a session shown in the hub. |
| Ocman relay | The separate `ocman-relay` service used for encrypted sharing, webhook buffering, and usage reports. It is neither the hub nor a remote session owner. |
| Ocman sign-in | Access to the dashboard. A remote access token connects Ocman instances, relay sign-in enrolls a connection to the relay, and a native account signs in to a coding-agent platform. |

See [Remote machines](docs/features/multi-remote.md) and [Configuration](docs/configuration/_index.md).

## Platforms and execution

| Term | What it is |
| --- | --- |
| Coding-agent platform | The tool Ocman integrates with: OpenCode, Claude Code, or Codex. Use *platform* when the context is clear. |
| Platform connection | A named, owner-local configuration for one platform, such as `Claude Work`. Several connections can use the same platform or underlying account. A session keeps its recorded connection even if the default changes. |
| Native account / model provider | The native account is the login or credentials used by a platform connection. A model provider supplies a model or a model namespace. Neither term is a substitute for *platform connection*. |
| Agent | The coding assistant that works in a session. In the picker, *agent* can also mean the platform's selectable agent preset. A **subagent** is delegated by an agent and can have its own child session. |
| Model / model catalog | A model generates the agent's responses. The catalog is the set of available models reported for the selected platform connection. The **model picker** selects a model; favorites and fallback order are separate preferences. |
| Managed agent runtime | A native server or supervisor Ocman manages to run agent work on the owning machine. It can outlive any individual session. Name the platform when discussing its scope or lifecycle. |
| Browser terminal | An interactive shell on the owning machine, backed by tmux. It is separate from the agent runtime and the composer's **Bash mode**, which captures a command's output in the conversation. |

See [Platform connections](docs/features/platform-connections.md) and [Platform connection architecture](docs/other/architecture-platform-connections.md).

## Projects, checkout, and source control

| Term | What it is |
| --- | --- |
| Project | Ocman's grouping of sessions, checkout locations, and project settings. A project usually corresponds to a repository; its display name and working directory are not stable routing identifiers. A grouped project can show checkouts on several machines without making their settings or files shared. |
| Repository / checkout / worktree | A Git repository holds version history. A checkout is the files in a working directory. A worktree is a Git working tree; in Ocman, **Worktrees** usually means the additional checkouts used for parallel work within the same repository. |
| Project defaults / model fallback | Project defaults supply initial selections for new sessions. Model fallback is a separate ordered policy for trying another model. |
| Git remote / forge | A Git remote is a named repository endpoint such as `origin`. A forge such as GitHub or Forgejo hosts repositories, pull requests, checks, and issues. **Source control** is the settings area for forge integration. |
| Forge issue / pull request (PR) | A forge issue tracks work on the forge. A pull request proposes merging changes. Neither is a Factory Issue. |
| Session PR link / PR watch | The link associates a pull request with a session. The optional watch monitors checks, review feedback, and related changes for that session. Linking alone does not mean watching. |
| Changes / Explore / Session commits | **Changes** shows edits and working-tree diffs. **Explore** browses checkout files. **Session commits** are commits Ocman observed during a session, not a full Git log. |

See [Pull requests](docs/features/pull-requests.md), [Explore files](docs/features/explore.md), and [Session commits](docs/features/session-commits.md).

## Sessions and conversation

| Term | What it is |
| --- | --- |
| Session | A persistent record of work with one coding-agent platform. It can be idle, have no active process, or exist as a draft before native execution is bound. Use *session* for identity, lifecycle, ownership, and lists. |
| Conversation | The prompts, replies, and tool activity shown when opening a session. Use *conversation* for reading, composing, and sharing; do not imply a second session record. *Transcript* means the recorded contents. |
| Session draft / composer draft | A session draft is an allocated Ocman session before native setup. A composer draft is unsent input in the message editor, including input for an existing session. |
| One-off session | A normal session draft in a new temporary directory on the selected machine, without a preexisting project checkout. Creating the directory does not launch an agent or initialize Git. |
| Ocman session ID / native session ID | The Ocman ID identifies the application record and stays stable when execution is bound. The native ID is issued by the underlying platform and needs owner and source-connection context for routing. |
| Prompt / message / turn | A prompt is user input asking the agent to do work; a message is an entry in the conversation. A turn is one unit of agent execution, which may produce multiple messages or tool calls. Say *permission prompt* or *question* when the agent is asking the user. |
| Composer / session message queue | The composer edits and sends messages and attachments. The session message queue holds follow-ups for later delivery. It does not schedule Factory Issues or routine runs. |
| Session controls | Use **abort** for stopping the active turn, **compact** for reducing the agent's context, and **restart** with an explicit object when restarting a runtime. A browser reload does not restart an agent. |
| Command palette / slash command | The palette finds sessions and actions by name. A slash command invokes an action with `/`, such as `/permissions`. A keyboard shortcut opens a view or invokes an action without typing a command. |
| Read answers aloud | Browser speech synthesis for the final answer of a completed turn. It reads output; it does not capture voice input. |
| Session history / history ingestion | Session history is the persisted conversation. History ingestion imports native history into Ocman's owner-local conversation store. The **session sidebar** is the list and navigation, not the transcript. |
| Archive / snooze / pin | Archive removes an item from the default list without deleting it. Snooze hides a session until its wake time or manual unsnooze. Pin gives it a prominent position; it is not an execution state. Qualify the object, such as *pinned session*, *pinned Inbox message*, or Factory *Pinned Issue*. |
| Read state / session status | Read state tells whether the user has seen an update. Session status describes execution or whether input is needed. Opening a session is not the same as completing a turn or resolving a prompt. |

See [Session features](docs/features/_index.md#sessions), [One-off sessions](docs/features/one-off-sessions.md), and [Session identity and history](docs/other/architecture-history.md).

## Permissions and attention

| Term | What it is |
| --- | --- |
| Permission request / permission prompt | A request is the platform's question about whether it may perform a specific action. A prompt is the UI for answering it. The conversation and Inbox can show the same request. |
| Permission mode / permission rule | A mode is a session-level policy, such as **Plan only**. A rule matches a specific action or pattern and says allow, ask, or deny. Native platform permissions remain separate from Ocman's review. |
| Auto-approval | Ocman's automatic evaluation of a permission request. A cached decision or model judgment can help resolve it. The evaluation result and the final permission resolution are separate. **Auto-approve** is the settings label. |
| Inbox / Inbox message | The Inbox is the durable, cross-machine attention view. A message is an entry in it; it may link to a session, permission request, Factory decision, or routine result. |
| Notification / toast | A notification calls attention to an event, such as through Web Push. A toast is transient in-app feedback. Neither is a synonym for a durable Inbox message. |

See [Session permissions](docs/features/session-permissions.md), [Inbox](docs/features/inbox.md), and [Notifications](docs/features/notifications.md).

## Automation and delivery

| Term | What it is |
| --- | --- |
| Routine / routine run | A routine is a saved prompt with settings for where and how it runs. A run is one manual, scheduled, or webhook-triggered occurrence. A run can start or continue a session; its result is tracked separately. |
| Webhook inbox / webhook delivery | A webhook inbox receives external requests through the relay. A webhook delivery is one received request. A matching subscription can trigger a routine run; the delivery and the run are different records. |
| Factory | The part of Ocman that plans, dispatches, tracks, and delivers work using a local graph of Issues. |
| Work Epic / Formula / Mol | A Work Epic groups work under a goal. A Formula is a template. A Mol is a concrete group of Issues created from an exact Formula version or revision within an Epic or parent Mol. |
| Factory Issue / dependency | An Issue is a node in the Factory graph. A parent-child relationship groups Issues; a dependency determines whether downstream work can proceed. Neither relationship implies the other. |
| Plan / Plan approval / materialization | A Plan is an Issue containing a reviewable graph manifest. Approval accepts an exact Plan revision. Materialization applies that revision to the graph as a separate step. |
| Gate | A Factory Issue that records a decision or verified condition. Only a satisfying resolution releases its success path. A Gate is not a session permission prompt. |
| Factory attempt / Factory permission profile | An attempt is one execution of an Issue. Its permission profile is the named, versioned boundary for that work. The Issue, attempt, and any session launched for it are separate. |
| Factory workspace / Project Delivery | The workspace is the persistent working context shared sequentially by one Epic's work for one project. A Project Delivery publishes that project's combined changes for review in a PR; Factory does not merge it. |
| Factory queue / background task | The **Factory queue** shows Factory dispatch work. A **background task** is tracked work done by Ocman, such as history ingestion or maintenance. Readiness to dispatch a Factory Issue is not the same as a background task being queued. |

See [Routines](docs/features/routines.md), [Encrypted webhooks](docs/features/webhooks.md), and [Factory](docs/features/factory.md). Use [CONTEXT.md](CONTEXT.md) for exact Factory lifecycle and graph terms.

## Extensions, files, and sharing

| Term | What it is |
| --- | --- |
| Plugin / contribution / grant | A plugin is an owner-installed extension. A contribution is what it adds, such as a page, pane, command, or tool. A grant authorizes a specific capability; it is not a session permission rule. |
| Plugin workspace | A plugin-owned group of configuration and results on one machine. It can relate to multiple projects; a project does not own it. It is not a Git worktree or a Factory workspace. |
| Declarative plugin view / in-app plugin UI | Ocman renders declarative pages and panes from typed data without running plugin code in the browser. In-app plugin UI loads a plugin's own React bundle and is available only for hub-local plugins. |
| Plugin agent tool / plugin-owned session tool | A plugin agent tool is a declared operation exposed through Ocman's MCP server after approval. A plugin-owned session tool is dispatched back to the owning plugin under a binding for that specific session. |
| MCP server / tool / skill | Ocman's Model Context Protocol (MCP) server exposes callable tools to agents. A tool is an operation an agent can call; a skill is a set of instructions the agent reads. Neither is itself a plugin. |
| Artifact / composer attachment / file embed | An artifact is a published, immutable set of files or links associated with a project and optionally a session. An attachment is input sent with a prompt. A file embed displays or links a local file in the conversation without creating an artifact. |
| Link preview | A card for an existing link, such as a pull request or a Slack message. It does not publish the target or grant access to it. |
| Shared conversation / artifact share | A shared conversation is an encrypted read-only copy that follows completed turns. An artifact share is an encrypted snapshot of a published artifact. Importing a shared conversation creates an unsent draft for the recipient, not access to the original session. |
| Slack integration / Beads plugin | Slack connects channels or direct chats to Ocman conversations through the Slack plugin. Beads shows an external project's Beads tickets; these are not Forge or Factory issues. |

See [Plugins](docs/features/plugins.md), [MCP](docs/features/mcp.md), [Artifacts](docs/features/artifacts.md), and [Sharing](docs/features/sharing.md).

## Usage, health, and maintenance

| Term | What it is |
| --- | --- |
| Analytics | The historical dashboard for session activity, tokens, estimated costs, and performance. **Analytics → Session trends** charts message activity and session concurrency. |
| Provider usage | Account usage and quota reported by native platforms for platform connections. It is separate from Ocman's estimated token costs and from usage collection. |
| Activity / Background tasks / Resources | **Activity** is the live operations view. **Background tasks** shows an owner's job status and progress; **Resources** shows sampled CPU and memory for processes. |
| Auto-archive / idle-project cleanup / worktree cleanup | Auto-archive changes list visibility. Idle-project cleanup stops eligible Claude project supervisors without deleting sessions or files. Worktree cleanup can remove eligible archived worktree checkouts. |
| Usage collection / OpenTelemetry diagnostics | Usage collection counts active installations with pseudonymous daily reports. Optional OpenTelemetry diagnostics report operational signals to a configured collector. Neither is a provider quota reading. |
| Configuration / owner state | `config.yaml` holds declarative settings. `state.db` holds writable runtime state and Ocman-managed secrets; `state_conv.db` holds the imported conversation history. These are separate from native platform configuration and history. |

See [Scheduled maintenance](docs/features/scheduled-maintenance.md), [Idle projects](docs/features/idle-projects.md), [Worktree cleanup](docs/features/worktree-cleanup.md), and [Usage collection](docs/features/usage-collection.md).

## Naming decisions

| When writing about | Use this wording |
| --- | --- |
| Adding or selecting a platform connection | In **Settings → Platform connections**, choose **Add connection**. Select a **platform** and, if prompted, sign in to its **native account**. In the model picker, select the **connection**. |
| Connecting Ocman across machines | **Remote machines** is the feature. **Remotes** is the settings label. Use **remote Ocman instance** for the service and **Git remote** for a repository endpoint. |
| Archiving, snoozing, and sharing | **Archive a session**, **snooze a session**, and **share a conversation**. Use **session draft** for the pre-launch record and **composer draft** for unsent input. |
| Automation | Use **routine** for the saved prompt and **routine run** for one occurrence. Use **legacy Workflow** for the retired DAG feature, **PR workflow** for session-linked PR operations, and **Formula** or **Mol** for the specific Factory object. |
| Work tracking | Say **Factory Issue**, **Beads ticket**, or **forge issue** when more than one tracker is in scope. In Factory, distinguish **Work Epic** from **Mol**. |
| Delivery | Say **Project Delivery** for the change published for review, **webhook delivery** for the incoming request, and **prompt delivery** for sending input to a native session. |
| Inbox | Capitalize **Inbox** for the attention view. Say **webhook inbox** for intake, even if a webhook also leads to an Inbox message. |
| Queues and tasks | Say **queued message**, **Factory queue**, or **background task**. A queued message is not a Factory Issue or an Ocman job. |
| Workspaces | Say **Factory workspace**, **plugin workspace**, or **worktree**. Use **worktree** for the Git checkout even when Factory uses it. |
| Activity and usage | Use **Activity** for live operations, **Analytics → Session trends** for historical message activity and concurrency, **Provider usage** for account quotas, and **Usage collection** for installation reporting. |
| OpenCode and worktrees | Use **managed OpenCode server** and qualify the default shared setup versus a separate platform connection. A worktree does not imply a separate runtime. |

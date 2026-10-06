# Session sidebar ordering research

Date: 2026-10-06

## Conclusion

Sort ocman's ordinary session rows by the last completed turn, newest first, while showing running and needs-input states separately. Streaming should update the status and transcript, not the ordering key. This is a recommendation for ocman's concurrent-agent workflow, not a claim that other clients already use completion-only ordering.

Three relevant clients were investigated through first-party documentation and source. Open WebUI provides verified timestamp ordering and completion-aware unread presentation. Slack documents configurable ordering and a separate unread workflow. Codex documents completion and input-needed notifications, but the inspected docs do not specify its sidebar comparator.

## Scope and evidence limits

- Research only. No application changes or live-app writes. Sources were public reads; no authenticated APIs or account experiments were used.
- Existing repository research notes use `spec/<topic>/research.md`, so this note follows that convention.
- Open WebUI source was fetched from `main` during this investigation. Its head was resolved to [`8bd8b4fac5e059578ac0c74b3c18d11139f88b7d`](https://github.com/open-webui/open-webui/tree/8bd8b4fac5e059578ac0c74b3c18d11139f88b7d). Source links below use that revision. This is upstream source behavior, not a guarantee about every deployed release.
- Official documentation describes supported behavior, not necessarily every implementation detail. A label such as "Recents" and a completion notification do not prove ordering by completion time. Unknown behavior is explicitly identified below.

## Verified client behavior

### Open WebUI

**Main sidebar ordering is modification time, not a completed-turn timestamp.** `Sidebar.svelte` loads `refreshChatList`; that store calls `getChatList` and preserves the returned order. The request supplies pagination and inclusion flags without a sort override. The router defaults to `sort_by='updated_at'`, `sort_dir='desc'`; `chat_list_order` returns descending `Chat.updated_at` followed by `Chat.id`. Pinned and folder chats have separate lists. [Sidebar](https://github.com/open-webui/open-webui/blob/8bd8b4fac5e059578ac0c74b3c18d11139f88b7d/src/lib/components/layout/Sidebar.svelte), [list store](https://github.com/open-webui/open-webui/blob/8bd8b4fac5e059578ac0c74b3c18d11139f88b7d/src/lib/stores/chatList.ts), [getChatList](https://github.com/open-webui/open-webui/blob/8bd8b4fac5e059578ac0c74b3c18d11139f88b7d/src/lib/apis/chats/index.ts), [get_session_user_chat_list](https://github.com/open-webui/open-webui/blob/8bd8b4fac5e059578ac0c74b3c18d11139f88b7d/backend/open_webui/routers/chats.py), [chat_list_order and get_chat_title_id_list_by_user_id](https://github.com/open-webui/open-webui/blob/8bd8b4fac5e059578ac0c74b3c18d11139f88b7d/backend/open_webui/models/chats.py#L105-L129).

`updated_at` is broader than successful completion. The model's message upsert updates it when `touch=True`, and deleting a message also updates it. The chat-update route touches it for submitted history/messages, but not a title-only update. These paths disprove an exclusively completion-based ordering key. **Not verified:** exactly which streaming chunks cause persistence or a visible reorder in every generation path. [update_chat_by_id route](https://github.com/open-webui/open-webui/blob/8bd8b4fac5e059578ac0c74b3c18d11139f88b7d/backend/open_webui/routers/chats.py), [upsert_message_to_chat_by_id_and_message_id and delete_message_from_chat_by_id_and_message_id](https://github.com/open-webui/open-webui/blob/8bd8b4fac5e059578ac0c74b3c18d11139f88b7d/backend/open_webui/models/chats.py).

**Unread presentation waits until the chat is no longer active.** `ChatItem.svelte` computes unread only when the chat is not selected, is not `active`, and its `updatedAt` exceeds its effective read watermark, or there is no watermark. Active rows show a spinner; unread rows show a blue dot. Viewing the selected chat advances a local `viewedAt`; clicking optimistically clears unread. This is completion-aware presentation, not completion-time sorting. [ChatItem unread expression and chatItemContent](https://github.com/open-webui/open-webui/blob/8bd8b4fac5e059578ac0c74b3c18d11139f88b7d/src/lib/components/layout/Sidebar/ChatItem.svelte).

**Reading and ordering are separate in the main list.** `update_chat_last_read_at_by_id` changes `last_read_at`, not `updated_at`. `setChatReadAt` and `setAllChatsRead` map over existing rows without sorting. [read watermark methods](https://github.com/open-webui/open-webui/blob/8bd8b4fac5e059578ac0c74b3c18d11139f88b7d/backend/open_webui/models/chats.py), [list store](https://github.com/open-webui/open-webui/blob/8bd8b4fac5e059578ac0c74b3c18d11139f88b7d/src/lib/stores/chatList.ts).

**Important exception: folder lists default to unread-first ordering.** The folder-list route uses `unread_updated_at`. That comparator puts unread chats first, excludes chats with unfinished assistant messages from the unread tier, and then sorts by `updated_at` descending. Reading can therefore change a folder row's position on a subsequent list refresh. This is not a universal "reading never moves rows" precedent. It also is not a last-completed-turn comparator. [get_chat_list_by_folder_id](https://github.com/open-webui/open-webui/blob/8bd8b4fac5e059578ac0c74b3c18d11139f88b7d/backend/open_webui/routers/chats.py), [chat_list_order](https://github.com/open-webui/open-webui/blob/8bd8b4fac5e059578ac0c74b3c18d11139f88b7d/backend/open_webui/models/chats.py#L105-L129).

### Slack

**Sidebar ordering is configurable.** Official custom-section instructions offer alphabetical `A-Z`, `Recency`, and `Priority` sorting. Sections can override whole-sidebar sorting and filtering. The documented default filter is `Active only`, hiding conversations without new activity in 30 days. This is evidence of explicit ordering choices, not a completion comparator. The docs inspected do not define the exact Recency timestamp or Priority scoring formula. [Sort a custom section and Manage conversation display](https://slack.com/help/articles/360043207674-Organize-your-sidebar-with-custom-sections), [Filter your sidebar](https://slack.com/help/articles/212596808-Adjust-your-sidebar-preferences).

**Unread handling has its own view and ordering.** Desktop Unreads can sort like the sidebar, alphabetically, in recommended order, by newest activity, or by oldest activity. Mark as Read clears that conversation's messages from Unreads and can be undone. New messages arriving while the user is viewing Unreads require the refresh icon to reveal a fresh batch. This provides a documented stable-batch attention workflow, not proof that Slack freezes its ordinary sidebar. [Sort and filter unread conversations, Mark messages as read, Refresh for new messages](https://slack.com/help/articles/226410907-View-all-your-unread-messages).

**Transferable lesson, inferred:** the user can triage unread work separately from navigation order. Slack messages are not coding-agent turns; these docs establish no agent-completion or permission-request semantics.

### Codex desktop and IDE

The former Codex app documentation URLs currently serve shared ChatGPT/Codex documentation. Claims here are limited to the named surfaces and availability qualifiers in those docs.

**Verified organization, unknown ordinary ordering.** Desktop docs describe pinning projects near the top of the sidebar, pinning chats even when newer chats appear in the project, and archiving finished chats. IDE docs describe selecting from Recent chats. Neither statement defines whether unpinned chats use creation, submission, activity, opening, or completion time. Do not infer a comparator from "newer" or "Recent". [Organize projects and chats; Work in a workspace](https://developers.openai.com/codex/projects#organize-projects-and-chats), [Markdown source](https://developers.openai.com/codex/projects.md).

**Completion and input-needed events are explicitly distinct.** Desktop settings offer turn-completion alerts never, only in the background, or always, with separate permission and question notification controls. Where Activity is available, its sidebar bell opens a view of unread, running, and waiting-for-response chats; Mark all as read clears unread items. The documented pet states are Running, Needs input, Ready, and Blocked. [Configure desktop notifications and Follow chats in Activity view](https://developers.openai.com/codex/notifications), [Markdown source](https://developers.openai.com/codex/notifications.md).

The same notifications page says the IDE extension has no separate notification controls and the open chat shows activity. **Not verified:** desktop sidebar sorting, Activity sorting, exactly when unread becomes set or cleared, or whether opening a chat changes its rank. Notifications do not establish any of these.

## Ocman's current behavior

The inspected checkout already reduces churn with minute-bucket activity sorting. `compareSidebarActivity` compares `floor(timeUpdated / 60_000)`; `mergeSidebarSessions` preserves prior rank within a bucket. The activity handler explicitly receives per-token activity and patches timestamps when they cross a minute bucket. This reduces token-by-token movement but still lets streaming activity determine rank. [sidebarHelpers.ts:4-8 and 109-126](../../frontend/src/lib/sidebarHelpers.ts), [useSidebarSessions.ts:208-218](../../frontend/src/pages/session-detail/useSidebarSessions.ts).

Ocman already treats busy sessions and pending permission/question prompts as meaningful states, and preserves active children while hiding completed children unless selected or pinned. Those are distinct from recency. [filterInactiveChildren](../../frontend/src/lib/sidebarHelpers.ts), [turn-lifecycle and nested prompt conventions](../../AGENTS.md).

## Recommended simplest policy

This section is a proposed product rule, not observed client behavior.

1. Within existing pinned/project organization, sort ordinary rows by the timestamp of the latest successfully completed top-level turn, descending, with a deterministic session identity tie-breaker. Use creation time for sessions with no completed turn. Do not substitute the timestamp of the first assistant token or a completed tool step.
2. A busy session retains its previous completed-turn ordering key. Submission, token output, tools, title changes, viewing, and marking read do not advance it. Other sessions completing can still displace it; "retain rank" means retain its key, not an immutable screen index.
3. Advance the key once per actual turn completion. Use the agent's lifecycle and durable completion metadata, not a frontend arrival timestamp or a generic idle observation. Duplicate events, polling, reconnect replay, and server restart must not make old completions newly recent.
4. Keep unread independent. A newly completed unseen reply marks the row unread. Reading clears that indicator without changing rank. Running a new turn should not erase an older unread completion.
5. Permissions and questions are actionable before completion. Keep their existing needs-input indicators visible immediately, including prompts bubbled up from subagents. Reading a session does not resolve a prompt. Start with these indicators rather than adding unread-first or busy-first reordering.

This is smaller than introducing multiple new sidebar modes. Open WebUI supports separating active/read state from the main list's ordering; Codex supports distinguishing completion from required input. Neither proves completion-only sorting, which should be justified by ocman's workflow and the reported jumping rows.

## Semantics to settle before implementation

- **Turn versus message:** a tool call or intermediate assistant message must not count as a completed turn. Decide whether a settled turn without final reply text counts, especially cancellation, errors, or interruption. Recommended default: keep those separate attention states and retain the previous successful-completion key.
- **Reading during streaming:** seeing partial text must not automatically acknowledge an unseen final result. Decide whether an open, focused conversation at the bottom acknowledges completion automatically. A background tab should not acknowledge merely because its route is selected.
- **Old unread results during a new turn:** preserve the watermark and unread state for the earlier completion, even while the row also shows running.
- **Needs input:** badges are the simplest initial rule. If users miss blocked sessions, consider a small persistent needs-input group later; reading must not remove an unresolved request from it.
- **No completed history:** creation-time fallback leaves a resumed old unfinished session low in the list. Decide whether that is acceptable, with pinning/current-session visibility as the existing escape hatch, rather than quietly advancing rank on submission.
- **Grouped sidebar:** decide whether project headers use latest completion too. Changing only row order while project groups still follow streaming activity would preserve some of the reported jumping.
- **Cross-machine timing and recovery:** identify an owner-provided durable completion timestamp, stable across restart and reconnect, and deterministic ties across compound platform/session identities. A cached browser rank alone would disagree across clients.

## Implementation decisions

- The session API exposes `lastTurnCompletedAt` from the latest terminal assistant message's stored completion time. Errors also count as actionable outcomes, with their stored message creation time as fallback when no completion time exists.
- Tool-call steps, unknown finishes, unfinished assistant messages and compaction summaries do not advance the ordering key. Completion-less sessions and older remotes fall back to session creation time.
- Both sidebar views and worktree groups use this key. Main checkouts remain first; project headers retain alphabetical/manual order; pins retain pin order.
- Streaming and read-state updates keep their current timestamps and badges but do not rank rows. Terminal status events refresh the durable session row. Stale responses cannot undo a newer completion timestamp.

## Research gaps

No inspected source establishes a completion-only ordinary sidebar sort. Slack's exact Recency/Priority algorithms and Codex's unpinned/Activity comparators remain unknown. Open WebUI's full streaming persistence cadence was not traced. No live UI experiment was performed. LibreChat was attempted but excluded from the comparison because the guessed source paths returned 404 and public GitHub directory discovery returned 403; no behavior was inferred from those failures.

import { useEffect } from 'react';
import './SessionChangesSidebar.css';
import './SessionInfoSidebar.css';
import { usePlatformCapabilities } from '../lib/useCapabilities';
import { useSessionInfo } from '../lib/useSessionInfo';
import { useGitInfo } from '../lib/useGitInfo';
import { useSidebarCallbacks } from '../lib/useSidebarCallbacks';
import { ChangesRefreshButton, type PaneSummary } from './SessionChangesSidebar';
import { TodoList } from './TodoList';
import { SessionSection, TokensSection, LiveSection } from './SessionInfoSections';
import type { Session } from '../lib/api';
import type { TodoItem } from '../lib/todos';

// SessionInfoSidebar mirrors the structure of SessionChangesSidebar /
// WorkingTreeChangesSidebar so RightPanel can hold all three panes
// uniformly (same embedded/standalone modes, same summary/refresh
// callbacks). Sections, in render order:
//
//   - Session: cross-platform metadata sourced from the `session`
//     prop (branch, status, message count, duration, lifetime
//     changes summary, estimated cost). Always rendered when
//     `session` is provided — no live port required. This section
//     replaces most of the page-header `stats` strip that used to
//     surface the same numbers. The project path stays in the page
//     header itself (rendered next to the breadcrumb) so it remains
//     visible regardless of which right-panel pane is active.
//   - Todo: most-recent todowrite tool call from the loaded `parts`,
//     rendered as a checklist. Hidden when no todo state exists yet.
//     Cross-platform (matches OpenCode `todowrite` and Claude Code
//     `TodoWrite`).
//   - Tokens: input / output / cache read / cache write totals,
//     summed from the loaded `messages`. Cross-platform.
//   - Context: tokens used + % of model context window.
//     Live-only (OpenCode with a port).
//   - MCP: configured MCP servers + status. Live-only.
//   - LSP: configured LSPs + status. Live-only.
//
// The "Modified Files" section seen in the OpenCode TUI is
// intentionally omitted: it duplicates the Session changes / Working
// tree panes that already live below this one in the right panel.

interface SessionInfoSidebarProps {
  sessionId: string;
  // Platform ID for the current session. Used to look up capability
  // flags (sessionInfo). When undefined (loading) the live sections
  // render a loading skeleton. AD-12a: never branched on directly.
  platformId: string | undefined;
  // Increments whenever the parent observes a new edit/write part
  // arriving via SSE, prompting a debounced re-fetch.
  dirtyTick?: number;
  // The currently-rendered session, supplying the cross-platform
  // metadata for the Session section. Undefined while the parent
  // page is still loading.
  session?: Session;
  // When true the sidebar's outer chrome is omitted; used by RightPanel's
  // split mode where the parent supplies the pane header.
  embedded?: boolean;
  // Accepted for API parity with the other two sidebars but never
  // emitted: the changes panes' "N files +A -D" header layout doesn't
  // map cleanly onto this pane's mixed metric / list / checklist
  // content.
  onSummaryChange?: (summary: PaneSummary) => void;
  // Called once with a stable refresh callback so embedded parents
  // (RightPanel) can render their own refresh button in the pane
  // header.
  onRefresh?: (refresh: () => void) => void;
  // Called whenever the underlying request's loading flag flips.
  onLoadingChange?: (loading: boolean) => void;
}

export function SessionInfoSidebar({
  sessionId,
  platformId,
  dirtyTick,
  session,
  embedded = false,
  onSummaryChange,
  onRefresh,
  onLoadingChange,
}: SessionInfoSidebarProps) {
  const caps = usePlatformCapabilities(platformId);
  // We always issue the fetch when the platform has at least *some*
  // useful data to return. The backend handler degrades gracefully
  // (returns Supported=false with empty slices) for adapters that
  // don't implement SessionInfo, so this only stays disabled when no
  // platform claims this session at all (typically during initial
  // load before capabilities resolve).
  const liveEnabled = caps.sessionInfo;
  const { data, loading, error, refresh } = useSessionInfo(sessionId, {
    enabled: liveEnabled,
    dirtyTick,
  });

  // Per-session git info now comes from /api/git/info, fetched
  // on-demand here while the sidebar is mounted, instead of being
  // attached to every /api/sessions response by the backend (which
  // produced fork-pressure pauses; see docs/profiling.md).
  //
  // useGitInfo internally normalises the input list to a stable
  // query param so this fresh array literal on every render is
  // fine — the hook's effect dep is the param string, not the
  // array identity.
  const dir = session?.directory;
  const { infos: gitInfos } = useGitInfo(dir ? [dir] : []);
  const gitInfo = dir ? gitInfos[dir] : undefined;

  const ctx = data?.context;
  const ctxTokens = ctx?.tokens ?? 0;
  const ctxLimit = ctx?.limit ?? 0;
  // % of model context window. Hidden when the limit is unknown (0)
  // so the panel never displays a fabricated percentage.
  const pct = ctxLimit > 0 ? Math.min(100, Math.round((ctxTokens / ctxLimit) * 100)) : null;

  const tokenTotals = data?.tokens;
  const todos: TodoItem[] = (data?.todos ?? []) as TodoItem[];

  // Suppress the changes-panes-style summary block in the parent's
  // pane header — see the prop comment for rationale.
  useEffect(() => {
    if (!onSummaryChange) return;
    onSummaryChange({ files: 0, additions: 0, deletions: 0 });
  }, [onSummaryChange]);

  useSidebarCallbacks({ refresh, loading, onRefresh, onLoadingChange });

  // Session section: cross-platform metadata. Renders whenever a
  // session is available, regardless of caps.sessionInfo. Branch is
  // shown when gitInfo is populated; the changes summary uses the
  // session's lifetime additions/deletions which the backend already
  // computes from the per-edit filediffs. The Messages row prefers
  // the user/assistant breakdown from /api/session/{id}/info when
  // it's available and falls back to the legacy `messageCount` (user
  // turns only) otherwise — this keeps the panel useful for Claude
  // Code sessions, where the SessionInfo endpoint isn't implemented.
  //
  // Project intentionally lives in the page header (not here) so it
  // stays visible regardless of which right-panel pane is open.
  const msgBreakdown = data?.messages;
  const hasMsgBreakdown = !!msgBreakdown
    && (msgBreakdown.user > 0 || msgBreakdown.assistant > 0);
  // Cost / Est are two independent rows:
  //
  //  - Cost: what the platform recorded as billed (sum of
  //    `data.cost` across assistant messages). Falls back to
  //    `session.totalCost` (same source, SQL-summed) when the
  //    SessionInfo endpoint hasn't loaded yet or doesn't exist
  //    (Claude Code today).
  //  - Est: token-derived estimate from the pricing table, computed
  //    server-side. Only meaningful for the OpenCode SessionInfo
  //    endpoint. We hide the Est row when it's 0 to avoid a
  //    duplicate-looking "$0.00" line on platforms that don't emit
  //    one.
  //
  // Showing both side-by-side makes subscription-plan sessions
  // ($0 Cost + non-zero Est) obvious, and gives a sanity-check on
  // API-priced sessions (large Cost vs Est mismatches usually mean
  // an unrecognised model name).
  const displayCost = data?.context.cost && data.context.cost > 0
    ? data.context.cost
    : (session?.totalCost ?? 0);
  const displayEst = data?.context.estCost ?? 0;
  const sessionSection = session ? (
    <SessionSection
      session={session}
      branch={gitInfo?.branch}
      branchDirty={!!gitInfo?.dirty}
      hasMsgBreakdown={hasMsgBreakdown}
      msgUser={msgBreakdown?.user ?? 0}
      msgAssistant={msgBreakdown?.assistant ?? 0}
      displayCost={displayCost}
      displayEst={displayEst}
    />
  ) : null;

  // Todo section: only renders when there's an active todo list to
  // show. The empty state would be visual noise — every session that
  // hasn't run todowrite would otherwise render a "no tasks" line.
  // Sourced from /api/session/{id}/info.todos so the latest list is
  // always visible regardless of how many messages are currently
  // paginated into the view.
  const todoSection = todos.length > 0 ? (
    <section className="oc-info-section">
      <header className="oc-info-section-header">Todo</header>
      <TodoList todos={todos} />
    </section>
  ) : null;

  // Tokens section: cross-platform lifetime totals. Sourced from the
  // server which sums every message's tokens.{input,output,cache.{read,write}}.
  // Hidden until the response arrives so we don't render a flash of
  // zeroes during the initial fetch.
  const hasTokenData = tokenTotals
    && (tokenTotals.input > 0 || tokenTotals.output > 0
      || tokenTotals.cacheRead > 0 || tokenTotals.cacheWrite > 0);
  const tokensSection = hasTokenData && tokenTotals ? (
    <TokensSection
      input={tokenTotals.input}
      output={tokenTotals.output}
      cacheRead={tokenTotals.cacheRead}
      cacheWrite={tokenTotals.cacheWrite}
    />
  ) : null;

  // Context / MCP / LSP section group. Gated on the live capability
  // flag and the supported flag from the wire response. When the
  // platform doesn't support live data we render a single muted
  // "Live data unavailable" line instead of three separate empty
  // states — the cross-platform sections above already give the user
  // useful information.
  const liveDataUnavailable = !liveEnabled || (data && !data.supported);
  const liveSection = liveEnabled ? (
    <LiveSection loading={loading} error={error} data={data} ctxTokens={ctxTokens} pct={pct} />
  ) : null;

  const Body = (
    <div className="oc-changes-sidebar-body oc-info-body">
      {sessionSection}
      {todoSection}
      {tokensSection}
      {liveSection}
      {liveDataUnavailable && (
        <section className="oc-info-section">
          <div className="oc-info-empty">
            Live context, MCP and LSP data unavailable for this session.
          </div>
        </section>
      )}
    </div>
  );

  if (embedded) {
    return <div className="oc-changes-sidebar-embedded">{Body}</div>;
  }

  return (
    <aside className="oc-changes-sidebar" aria-label="Session info">
      <div className="oc-changes-sidebar-header">
        <span className="oc-changes-sidebar-title">Session info</span>
        <ChangesRefreshButton onClick={refresh} loading={loading} disabled={!liveEnabled} />
      </div>
      {Body}
    </aside>
  );
}

import { useCallback, useEffect, useMemo, useState } from 'react';
import './UpstreamPane.css';
import { useUpstreamList } from '../../lib/useUpstreamList';
import type { PR, Issue, StateFilter, Upstream } from '../../lib/upstreamApi';
import type { PaneSummary } from '../SessionChangesSidebar';
import { useForgeUser } from '../../lib/useForgeUser';
import { useGitInfo } from '../../lib/useGitInfo';
import { PRRow } from './PRRow';
import { IssueRow } from './IssueRow';
import { RemoteErrorBanner } from './RemoteErrorBanner';
import { UpstreamApiError } from '../../lib/upstreamApi';

interface UpstreamPaneProps {
  directory: string | undefined;
  upstreams: Upstream[];
  embedded?: boolean;
  // RightPanel API parity. PaneSummary isn't meaningful for the
  // upstream view (no +/- diff counts) so we emit zeros once on
  // mount; the parent renders no numbers next to the title.
  onSummaryChange?: (summary: PaneSummary) => void;
  onRefresh?: (refresh: () => void) => void;
  onLoadingChange?: (loading: boolean) => void;
}

type Tab = 'prs' | 'issues';

/**
 * UpstreamPane renders the PR & Issue browser inside the RightPanel.
 *
 * Layout:
 *
 *   [PRs | Issues]   ← tab strip (with refresh)
 *   ─────────────
 *   <filters: open|closed|all + mine toggle>
 *   <per-remote group(s)>
 *     <header: github.com / forgejo host (only shown when >1 remote)>
 *     <list of rows>
 *     <pagination (prev / next)>
 *
 * State scoped per-tab so flipping between PRs and Issues doesn't
 * reset filters or pagination of the other tab.
 */
export function UpstreamPane({
  directory,
  upstreams,
  onRefresh,
  onLoadingChange,
  onSummaryChange,
}: UpstreamPaneProps) {
  const [tab, setTab] = useState<Tab>('prs');

  // Independent filter state per tab.
  const [prState, setPRState] = useState<StateFilter>('open');
  const [issueState, setIssueState] = useState<StateFilter>('open');
  const [prMine, setPRMine] = useState(false);
  const [issueMine, setIssueMine] = useState(false);

  // Tell the parent we have no diff summary to contribute. Done once
  // because the value never changes.
  useEffect(() => {
    onSummaryChange?.({ files: 0, additions: 0, deletions: 0 });
  }, [onSummaryChange]);

  // No remote → explain instead of rendering tabs / filters / lists.
  // The pane stays available in the strip so users can discover the
  // feature even on unsupported projects.
  if (upstreams.length === 0) {
    return (
      <div className="oc-upstream-pane" data-testid="upstream-pane">
        <NoUpstreamMessage directory={directory} />
      </div>
    );
  }

  return (
    <div className="oc-upstream-pane" data-testid="upstream-pane">
      <div className="oc-upstream-tabs" role="tablist">
        <button
          role="tab"
          aria-selected={tab === 'prs'}
          className={`oc-upstream-tab${tab === 'prs' ? ' active' : ''}`}
          onClick={() => setTab('prs')}
          data-testid="upstream-tab-prs"
        >
          PRs
        </button>
        <button
          role="tab"
          aria-selected={tab === 'issues'}
          className={`oc-upstream-tab${tab === 'issues' ? ' active' : ''}`}
          onClick={() => setTab('issues')}
          data-testid="upstream-tab-issues"
        >
          Issues
        </button>
      </div>

      {tab === 'prs' ? (
        <UpstreamTabContent
          key="prs"
          kind="prs"
          directory={directory}
          upstreams={upstreams}
          state={prState}
          onStateChange={setPRState}
          mine={prMine}
          onMineChange={setPRMine}
          onRefresh={onRefresh}
          onLoadingChange={onLoadingChange}
        />
      ) : (
        <UpstreamTabContent
          key="issues"
          kind="issues"
          directory={directory}
          upstreams={upstreams}
          state={issueState}
          onStateChange={setIssueState}
          mine={issueMine}
          onMineChange={setIssueMine}
          onRefresh={onRefresh}
          onLoadingChange={onLoadingChange}
        />
      )}
    </div>
  );
}

interface UpstreamTabContentProps {
  kind: Tab;
  directory: string | undefined;
  upstreams: Upstream[];
  state: StateFilter;
  onStateChange: (s: StateFilter) => void;
  mine: boolean;
  onMineChange: (m: boolean) => void;
  onRefresh?: (refresh: () => void) => void;
  onLoadingChange?: (loading: boolean) => void;
}

function UpstreamTabContent({
  kind,
  directory,
  upstreams,
  state,
  onStateChange,
  mine,
  onMineChange,
  onRefresh,
  onLoadingChange,
}: UpstreamTabContentProps) {
  // Always render the filter strip + per-remote groups. Each group
  // owns its own fetch hook (via UpstreamRemoteGroup below) so a
  // failure in one host doesn't block the other.
  const groupRefreshers = useMemo<Array<() => void>>(() => [], []);

  // Compose a single refresh callback that fans out to every group.
  const refreshAll = useCallback(() => {
    for (const r of groupRefreshers) r();
  }, [groupRefreshers]);

  useEffect(() => {
    onRefresh?.(refreshAll);
  }, [refreshAll, onRefresh]);

  // Loading is "any group still loading" — UpstreamRemoteGroup
  // pushes its loading state up through onLoadingChange below.
  const [loadingCount, setLoadingCount] = useState(0);
  useEffect(() => {
    onLoadingChange?.(loadingCount > 0);
  }, [loadingCount, onLoadingChange]);
  const handleGroupLoading = useCallback((loading: boolean) => {
    setLoadingCount((n) => (loading ? n + 1 : Math.max(0, n - 1)));
  }, []);

  const showGroupHeader = upstreams.length > 1;

  // UpstreamPane guarantees upstreams.length > 0 by the time we get
  // here (the no-upstream case is handled one level up so the tab
  // strip is hidden along with the lists).
  return (
    <div className="oc-upstream-tab-content">
      <FilterStrip
        state={state}
        onStateChange={onStateChange}
        mine={mine}
        onMineChange={onMineChange}
      />
      {upstreams.map((u) => (
        <UpstreamRemoteGroup
          key={`${u.host}/${u.remote}`}
          kind={kind}
          upstream={u}
          directory={directory!}
          state={state}
          mine={mine}
          registerRefresh={(fn) => {
            groupRefreshers.push(fn);
            return () => {
              const i = groupRefreshers.indexOf(fn);
              if (i >= 0) groupRefreshers.splice(i, 1);
            };
          }}
          onLoadingChange={handleGroupLoading}
          showHeader={showGroupHeader}
        />
      ))}
    </div>
  );
}

interface FilterStripProps {
  state: StateFilter;
  onStateChange: (s: StateFilter) => void;
  mine: boolean;
  onMineChange: (m: boolean) => void;
}

function FilterStrip({ state, onStateChange, mine, onMineChange }: FilterStripProps) {
  return (
    <div className="oc-upstream-filters" role="toolbar">
      <div className="oc-upstream-filter-group" role="radiogroup" aria-label="State">
        {(['open', 'closed', 'all'] as StateFilter[]).map((s) => (
          <button
            key={s}
            role="radio"
            aria-checked={state === s}
            className={`oc-upstream-filter${state === s ? ' active' : ''}`}
            onClick={() => onStateChange(s)}
            data-testid={`upstream-filter-${s}`}
          >
            {s}
          </button>
        ))}
      </div>
      <label className="oc-upstream-mine">
        <input
          type="checkbox"
          checked={mine}
          onChange={(e) => onMineChange(e.target.checked)}
          data-testid="upstream-filter-mine"
        />
        Mine
      </label>
    </div>
  );
}

interface UpstreamRemoteGroupProps {
  kind: Tab;
  upstream: Upstream;
  directory: string;
  state: StateFilter;
  mine: boolean;
  registerRefresh: (fn: () => void) => () => void;
  onLoadingChange: (loading: boolean) => void;
  showHeader: boolean;
}

function UpstreamRemoteGroup({
  kind,
  upstream,
  directory,
  state,
  mine,
  registerRefresh,
  onLoadingChange,
  showHeader,
}: UpstreamRemoteGroupProps) {
  // Resolve the "mine" identity for this remote's host. null means
  // the forge has no credential — disable the mine toggle visually
  // and don't send the filter parameter.
  const myLogin = useForgeUser(directory, upstream.remote);
  const mineFilter = mine && myLogin ? myLogin : undefined;

  // Working-tree branch for the project, used to highlight PRs whose
  // source branch matches what the user has checked out locally.
  // useGitInfo polls every 30s; only relevant for PRs, but cheap
  // enough to fetch unconditionally (the result is shared via the
  // backend cache).
  const gitInfoDirs = useMemo(() => [directory], [directory]);
  const { infos: gitInfos } = useGitInfo(gitInfoDirs);
  const currentBranch = gitInfos[directory]?.branch;

  const list = useUpstreamList<PR | Issue>({
    kind,
    dir: directory,
    remote: upstream.remote,
    state,
    mine: mineFilter,
    enabled: true,
  });

  // Push our refresh callback up; unregister on unmount.
  useEffect(() => {
    const unregister = registerRefresh(list.refresh);
    return unregister;
  }, [registerRefresh, list.refresh]);

  // Mirror loading flag up.
  useEffect(() => {
    onLoadingChange(list.loading);
    // Decrement on unmount-while-loading.
    return () => {
      if (list.loading) onLoadingChange(false);
    };
  }, [list.loading, onLoadingChange]);

  return (
    <section className="oc-upstream-group" data-testid={`upstream-group-${upstream.host}`}>
      {showHeader && (
        <header className="oc-upstream-group-header">
          <span className="oc-upstream-group-host">{upstream.host}</span>
          <span className="oc-upstream-group-repo">{upstream.repo}</span>
        </header>
      )}
      {list.error ? (
        <RemoteErrorBanner error={list.error} onRetry={list.refresh} />
      ) : null}
      {list.rateLimit.limited ? (
        <RemoteErrorBanner
          error={
            new UpstreamApiError(
              {
                error: {
                  code: 'rate_limited',
                  message: 'Rate limited',
                  retryAfter: list.rateLimit.resetAt,
                },
              },
              429,
            )
          }
          onRetry={list.refresh}
        />
      ) : null}
      {!list.error && list.items.length === 0 && !list.loading ? (
        <div className="oc-upstream-empty">No {kind === 'prs' ? 'pull requests' : 'issues'}.</div>
      ) : null}
      <ul className="oc-upstream-list" data-testid={`upstream-${kind}-list`}>
        {list.items.map((item) => {
          if (kind === 'prs') {
            return (
              <PRRow
                key={item.number}
                pr={item as PR}
                directory={directory}
                remote={upstream.remote}
                currentBranch={currentBranch}
              />
            );
          }
          return (
            <IssueRow
              key={item.number}
              issue={item as Issue}
              directory={directory}
              remote={upstream.remote}
            />
          );
        })}
      </ul>
      <Pagination
        page={list.page}
        hasMore={list.pagination.hasMore}
        onPrev={() => list.setPage(Math.max(1, list.page - 1))}
        onNext={() => list.setPage(list.page + 1)}
      />
    </section>
  );
}

function Pagination({
  page,
  hasMore,
  onPrev,
  onNext,
}: {
  page: number;
  hasMore: boolean;
  onPrev: () => void;
  onNext: () => void;
}) {
  if (page === 1 && !hasMore) {
    // Only one page — no need for controls.
    return null;
  }
  return (
    <div className="oc-upstream-pagination">
      <button onClick={onPrev} disabled={page <= 1} data-testid="upstream-page-prev">
        ‹ Prev
      </button>
      <span className="oc-upstream-pagination-page">page {page}</span>
      <button onClick={onNext} disabled={!hasMore} data-testid="upstream-page-next">
        Next ›
      </button>
    </div>
  );
}

/**
 * NoUpstreamMessage renders when the project has no GitHub/Forgejo
 * remote. Kept informational rather than apologetic — the user
 * intentionally opened the pane, so spell out what *would* make it
 * work.
 */
function NoUpstreamMessage({ directory }: { directory: string | undefined }) {
  return (
    <div className="oc-upstream-no-upstream" data-testid="upstream-no-upstream">
      <p className="oc-upstream-no-upstream-title">No supported upstream detected</p>
      <p>
        Ocman looks for git remotes pointing at <strong>github.com</strong> or a
        Forgejo host configured in <code>~/.config/tea/config.yml</code>.
      </p>
      {directory ? (
        <p className="oc-upstream-no-upstream-hint">
          Current project: <code>{directory}</code>
        </p>
      ) : null}
      <p className="oc-upstream-no-upstream-hint">
        To enable this pane, add a remote (<code>git remote add origin …</code>)
        or run <code>tea login add</code> for your Forgejo server.
      </p>
    </div>
  );
}

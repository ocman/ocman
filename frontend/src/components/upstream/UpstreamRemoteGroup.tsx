import { useEffect, useRef } from 'react';
import { useUpstreamList } from '../../lib/useUpstreamList';
import type { PR, Issue, StateFilter, Upstream } from '../../lib/upstreamApi';
import { UpstreamApiError } from '../../lib/upstreamApi';
import { useForgeUser } from '../../lib/useForgeUser';
import { PRRow } from './PRRow';
import { IssueRow } from './IssueRow';
import { RemoteErrorBanner } from './RemoteErrorBanner';
import { Pagination } from '../Pagination';
import './UpstreamRemoteGroup.css';

export interface UpstreamRemoteGroupProps {
  kind: 'prs' | 'issues';
  upstream: Upstream;
  directory: string;
  /** Where row actions launch; undefined disables them. */
  launchDirectory: string | undefined;
  remoteId: string;
  state: StateFilter;
  mine: boolean;
  registerRefresh: (fn: () => void) => () => void;
  onLoadingChange: (loading: boolean) => void;
  showHeader: boolean;
  currentBranch?: string;
}

export function UpstreamRemoteGroup({
  kind,
  upstream,
  directory,
  launchDirectory,
  remoteId,
  state,
  mine,
  registerRefresh,
  onLoadingChange,
  showHeader,
  currentBranch,
}: UpstreamRemoteGroupProps) {
  // Resolve the "mine" identity for this remote's host. null means
  // the forge has no credential — disable the mine toggle visually
  // and don't send the filter parameter.
  const identity = useForgeUser(mine ? directory : undefined, mine ? upstream.remote : undefined, remoteId);
  const mineFilter = mine && identity.login ? identity.login : undefined;

  const list = useUpstreamList<PR | Issue>({
    kind,
    dir: directory,
    remoteId,
    remote: upstream.remote,
    state,
    mine: mineFilter,
    enabled: !mine || (identity.ready && !!identity.login),
  });

  // Push our refresh callback up; unregister on unmount.
  useEffect(() => {
    const unregister = registerRefresh(list.refresh);
    return unregister;
  }, [registerRefresh, list.refresh]);

  // Refocus the PR for the checked-out branch when it (or the list) changes.
  const sectionRef = useRef<HTMLElement>(null);
  useEffect(() => scrollToCurrentBranch(sectionRef.current), [currentBranch, list.items]);

  // Mirror loading flag up.
  useEffect(() => {
    if (!list.loading) return;
    onLoadingChange(true);
    return () => onLoadingChange(false);
  }, [list.loading, onLoadingChange]);

  return (
    <section ref={sectionRef} className="oc-upstream-group" data-testid={`upstream-group-${upstream.host}`}>
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
      {mine && identity.loading ? (
        <div className="oc-upstream-empty">Resolving forge identity…</div>
      ) : mine && identity.ready && !identity.login ? (
        <div className="oc-upstream-empty">Mine requires forge authentication.</div>
      ) : !list.error && list.items.length === 0 && !list.loading ? (
        <div className="oc-upstream-empty">No {kind === 'prs' ? 'pull requests' : 'issues'}.</div>
      ) : null}
      <ul className="oc-upstream-list" data-testid={`upstream-${kind}-list`}>
        {list.items.map((item) => {
          if (kind === 'prs') {
            return (
              <PRRow
                key={`${remoteId}/${item.number}`}
                pr={item as PR}
                directory={launchDirectory}
                checksDirectory={directory}
                remoteId={remoteId}
                remote={upstream.remote}
                currentBranch={currentBranch}
              />
            );
          }
          return (
            <IssueRow
              key={`${remoteId}/${item.number}`}
              issue={item as Issue}
              directory={launchDirectory}
              remoteId={remoteId}
              remote={upstream.remote}
            />
          );
        })}
      </ul>
      {(list.page !== 1 || list.pagination.hasMore) && (
        <Pagination
          className="oc-upstream-pagination"
          previousLabel="‹ Prev"
          nextLabel="Next ›"
          previousDisabled={list.page <= 1}
          nextDisabled={!list.pagination.hasMore}
          onPrevious={() => list.setPage(Math.max(1, list.page - 1))}
          onNext={() => list.setPage(list.page + 1)}
          previousTestId="upstream-page-prev"
          nextTestId="upstream-page-next"
        >
          <span className="oc-upstream-pagination-page">page {list.page}</span>
        </Pagination>
      )}
    </section>
  );
}

// jsdom has no scrollIntoView, hence the optional call.
function scrollToCurrentBranch(section: HTMLElement | null) {
  section?.querySelector('.current-branch')?.scrollIntoView?.({ block: 'nearest' });
}

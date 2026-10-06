import { useEffect, useState } from 'react';
import type { CIState, Check, PR, PRChecks } from '../../lib/upstreamApi';
import { fetchPRChecks } from '../../lib/upstreamApi';
import { CI_POLL_MS, cachePRChecks, getCachedPRChecks, isSettled, prChecksCacheKey } from '../../lib/prChecksCache';
import { ExpandableRow } from './ExpandableRow';

interface PRRowProps {
  pr: PR;
  /** Where launches run; undefined disables them. */
  directory: string | undefined;
  /**
   * Directory the CI checks are fetched for. Defaults to `directory`; pinned
   * to the project so a sibling-worktree switch keeps loaded checks.
   */
  checksDirectory?: string;
  remoteId: string;
  remote: string;
  /**
   * The branch currently checked out in the project's working tree.
   * When this matches the PR's source branch (and the PR isn't from
   * a fork), the row is highlighted so the user can quickly spot the
   * PR that corresponds to whatever they're working on locally.
   * Undefined when git info is still loading or unavailable.
   */
  currentBranch?: string;
}

/**
 * PRRow renders a single PR row with an inline-expand affordance.
 *
 * Click anywhere on the collapsed row (except the link) to expand.
 * Click the title/header again to collapse. Only one row is expanded
 * at a time within a list — collapse is opt-in here, the parent
 * doesn't enforce single-expansion in v1 (matches the "best-effort"
 * note in FR-7; can be tightened later).
 */
export function PRRow({ pr, directory, checksDirectory = directory ?? '', remoteId, remote, currentBranch }: PRRowProps) {
  const [visible, setVisible] = useState(false);
  const checks = usePRChecks(pr, checksDirectory, remoteId, remote, visible);

  // Cross-fork PRs share their head branch name with the user's
  // local tree by coincidence at best (different repo entirely), so
  // skip the match in that case to avoid false positives.
  const isCurrentBranch =
    !pr.crossFork && !!currentBranch && pr.branch === currentBranch;

  // The CI dot is always rendered for PRs (grey/unknown until the
  // fetch, which starts once the row is visible, resolves). We can only *fetch* a status when the forge
  // reported a head SHA, so gate the fetch — not the dot — on that.
  // This keeps the indicator visible (and its absence meaningful)
  // even when a forge omits the SHA.
  const canFetchCI = !!pr.headSha;

  return (
    <ExpandableRow
      type="pr"
      number={pr.number}
      title={pr.title}
      body={pr.body}
      author={pr.author}
      status={pr.status}
      updatedAt={pr.updatedAt}
      labels={pr.labels}
      assignees={pr.assignees}
      url={pr.url}
      host={pr.host}
      directory={directory}
      remoteId={remoteId}
      remote={remote}
      crossFork={pr.crossFork}
      className={isCurrentBranch ? 'current-branch' : undefined}
      onVisibleChange={canFetchCI ? setVisible : undefined}
      summaryPrefix={<CIDot state={canFetchCI ? checks.state : 'unknown'} prNumber={pr.number} />}
      summarySuffix={isCurrentBranch ? (
        <span
          className="oc-upstream-row-current-branch"
          title={`Matches your current branch: ${pr.branch}`}
          data-testid={`pr-row-${pr.number}-current-branch`}
        >
          current
        </span>
      ) : undefined}
      detailBeforeBody={pr.crossFork ? (
        <div className="oc-upstream-row-fork-note">
          Cross-fork PR — worktree launch will fetch the PR ref.
        </div>
      ) : undefined}
      detailAfterBody={canFetchCI ? <CIChecks checks={checks} prNumber={pr.number} /> : undefined}
    />
  );
}

interface ChecksState {
  state: CIState;
  checks: Check[];
  loading: boolean;
  loaded: boolean;
  error: boolean;
}

interface ChecksResult {
  key: string;
  data: PRChecks | null;
  loading: boolean;
  error: boolean;
}

/**
 * usePRChecks fetches a PR's CI/build status while its row is visible.
 * A settled status (every check finished) is cached per repository + head
 * SHA, so it is fetched once; anything else is re-fetched every CI_POLL_MS
 * until it settles or the row scrolls out of view.
 */
function usePRChecks(pr: PR, directory: string, remoteId: string, remote: string, visible: boolean): ChecksState {
  const sha = pr.headSha ?? '';
  const requestKey = `${remoteId}\0${directory}\0${remote}\0${sha}`;
  const cacheKey = prChecksCacheKey(pr.host, pr.repo, sha);
  const [result, setResult] = useState<ChecksResult>({ key: requestKey, data: null, loading: false, error: false });

  useEffect(() => {
    if (!sha || !visible) return;
    const ctrl = new AbortController();
    let timer: number | undefined;
    const run = () => {
      const cached = getCachedPRChecks(cacheKey);
      if (cached) {
        setResult({ key: requestKey, data: cached, loading: false, error: false });
        return;
      }
      setResult((prev) => ({ key: requestKey, data: prev.key === requestKey ? prev.data : null, loading: true, error: false }));
      fetchPRChecks({ dir: directory, remoteId, remote, sha, signal: ctrl.signal })
        .then((res) => {
          if (ctrl.signal.aborted) return;
          cachePRChecks(cacheKey, res);
          setResult({ key: requestKey, data: res, loading: false, error: false });
          if (!isSettled(res)) timer = window.setTimeout(run, CI_POLL_MS);
        })
        .catch(() => {
          if (ctrl.signal.aborted) return;
          setResult((prev) => ({ ...prev, key: requestKey, loading: false, error: true }));
          timer = window.setTimeout(run, CI_POLL_MS);
        });
    };
    run();
    return () => {
      ctrl.abort();
      window.clearTimeout(timer);
    };
  }, [sha, directory, remoteId, remote, visible, requestKey, cacheKey]);

  const current = result.key === requestKey;
  return {
    state: current ? result.data?.state ?? 'unknown' : 'unknown',
    checks: current ? result.data?.checks ?? [] : [],
    loading: current && result.loading,
    loaded: current && result.data !== null,
    error: current && result.error,
  };
}

const CI_LABEL: Record<CIState, string> = {
  unknown: 'No CI status',
  pending: 'Checks running',
  success: 'All checks passed',
  failure: 'Some checks failed',
};

function CIDot({ state, prNumber }: { state: CIState; prNumber: number }) {
  return (
    <span
      className={`oc-upstream-ci-dot oc-upstream-ci-dot-${state}`}
      title={CI_LABEL[state]}
      aria-label={CI_LABEL[state]}
      role="img"
      data-testid={`pr-row-${prNumber}-ci`}
    />
  );
}

function CIChecks({ checks, prNumber }: { checks: ChecksState; prNumber: number }) {
  if (checks.loading && !checks.loaded) {
    return <div className="oc-upstream-ci-checks oc-upstream-ci-loading">Loading checks…</div>;
  }
  if (checks.error) {
    return <div className="oc-upstream-ci-checks oc-upstream-ci-error">Failed to load checks.</div>;
  }
  if (!checks.loaded || checks.checks.length === 0) {
    return <div className="oc-upstream-ci-checks oc-upstream-ci-empty">No CI checks.</div>;
  }
  return (
    <ul className="oc-upstream-ci-checks" data-testid={`pr-detail-${prNumber}-checks`}>
      {checks.checks.map((c, i) => (
        <li key={`${c.name}-${i}`} className="oc-upstream-ci-check">
          <span className={`oc-upstream-ci-dot oc-upstream-ci-dot-${c.state}`} aria-hidden="true" />
          {c.url ? (
            <a href={c.url} target="_blank" rel="noreferrer noopener" className="oc-upstream-ci-check-name">
              {c.name || '(unnamed check)'}
            </a>
          ) : (
            <span className="oc-upstream-ci-check-name">{c.name || '(unnamed check)'}</span>
          )}
          <span className="oc-upstream-ci-check-state">{c.state}</span>
        </li>
      ))}
    </ul>
  );
}

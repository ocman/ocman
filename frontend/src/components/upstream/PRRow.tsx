import { useCallback, useRef, useState } from 'react';
import type { CIState, Check, PR, PRChecks } from '../../lib/upstreamApi';
import { fetchPRChecks } from '../../lib/upstreamApi';
import { ExpandableRow } from './ExpandableRow';

interface PRRowProps {
  pr: PR;
  directory: string;
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
export function PRRow({ pr, directory, remote, currentBranch }: PRRowProps) {
  const checks = usePRChecks(pr, directory, remote);

  // Cross-fork PRs share their head branch name with the user's
  // local tree by coincidence at best (different repo entirely), so
  // skip the match in that case to avoid false positives.
  const isCurrentBranch =
    !pr.crossFork && !!currentBranch && pr.branch === currentBranch;

  // The CI dot is always rendered for PRs (grey/unknown until the
  // lazy fetch resolves). We can only *fetch* a status when the forge
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
      remote={remote}
      crossFork={pr.crossFork}
      className={isCurrentBranch ? 'current-branch' : undefined}
      onMouseEnter={canFetchCI ? checks.load : undefined}
      onToggle={canFetchCI ? checks.load : undefined}
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
  load: () => void;
}

/**
 * usePRChecks lazily fetches a PR's CI/build status. The fetch fires
 * only when `load()` is called (on hover or expand), runs at most
 * once per row, and is aborted on unmount. Until it resolves the
 * state is "unknown" (neutral dot).
 */
function usePRChecks(pr: PR, directory: string, remote: string): ChecksState {
  const [data, setData] = useState<PRChecks | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState(false);
  // Guards against duplicate fetches: hover + click can both fire.
  const startedRef = useRef(false);
  const abortRef = useRef<AbortController | null>(null);

  const load = useCallback(() => {
    if (startedRef.current || !pr.headSha) return;
    startedRef.current = true;
    setLoading(true);
    const ctrl = new AbortController();
    abortRef.current = ctrl;
    fetchPRChecks({ dir: directory, remote, sha: pr.headSha, signal: ctrl.signal })
      .then((res) => setData(res))
      .catch((err) => {
        if (ctrl.signal.aborted) return;
        // Failed fetch shouldn't break the row; allow a retry by
        // clearing the started guard.
        startedRef.current = false;
        setError(true);
        void err;
      })
      .finally(() => {
        if (!ctrl.signal.aborted) setLoading(false);
      });
  }, [pr.headSha, directory, remote]);

  return {
    state: data?.state ?? 'unknown',
    checks: data?.checks ?? [],
    loading,
    loaded: data !== null,
    error,
    load,
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

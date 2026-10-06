// @vitest-environment jsdom
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { act, render, screen, fireEvent, waitFor } from '@testing-library/react';
import { PRRow } from './PRRow';
import { CI_POLL_MS, clearPRChecksCache, getCachedPRChecks, prChecksCacheKey, resetPRChecksMemoryForTest } from '../../lib/prChecksCache';
import type { PR } from '../../lib/upstreamApi';
import * as api from '../../lib/upstreamApi';

function makePR(overrides: Partial<PR> = {}): PR {
  return {
    number: 42,
    title: 'Tighten slug',
    body: '',
    author: 'dries',
    status: 'open',
    updatedAt: '2025-01-01T00:00:00Z',
    labels: null,
    assignees: null,
    requestedReviewers: null,
    branch: 'tighten-slug',
    url: 'https://example.com/pr/42',
    host: 'example.com',
    repo: 'dries/ocman',
    crossFork: false,
    ...overrides,
  };
}

describe('PRRow current-branch highlight', () => {
  it('keeps current, status, and label badges before the author on the metadata line', () => {
    render(<PRRow pr={makePR({ labels: [{ name: 'bug', color: 'ff0000' }] })} directory="/repo" remoteId="local" remote="origin" currentBranch="tighten-slug" />);

    const summary = screen.getByRole('button', { expanded: false });
    const author = screen.getByText('by dries');
    const current = screen.getByTestId('pr-row-42-current-branch');
    const status = screen.getByText('open');
    const label = screen.getByText('bug');
    expect(current.parentElement).toBe(author.parentElement);
    expect(status.parentElement).toBe(author.parentElement);
    for (const badge of [current, status, label]) {
      expect(summary).not.toContainElement(badge);
      expect(author.parentElement).toContainElement(badge);
      expect(badge.compareDocumentPosition(author) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    }
    expect(summary).toContainElement(screen.getByText('Tighten slug'));
  });

  it('highlights the row when the PR branch matches the current branch', () => {
    render(<PRRow pr={makePR()} directory="/repo" remoteId="local" remote="origin" currentBranch="tighten-slug" />);

    const row = screen.getByTestId('pr-row-42');
    expect(row.className).toContain('current-branch');

    const badge = screen.getByTestId('pr-row-42-current-branch');
    expect(badge).toBeInTheDocument();
    expect(badge.getAttribute('title')).toContain('tighten-slug');
  });

  it('does not highlight when the current branch differs', () => {
    render(<PRRow pr={makePR()} directory="/repo" remoteId="local" remote="origin" currentBranch="main" />);

    expect(screen.getByTestId('pr-row-42').className).not.toContain('current-branch');
    expect(screen.queryByTestId('pr-row-42-current-branch')).not.toBeInTheDocument();
  });

  it('does not highlight when currentBranch is undefined (git info not yet loaded)', () => {
    render(<PRRow pr={makePR()} directory="/repo" remoteId="local" remote="origin" />);

    expect(screen.getByTestId('pr-row-42').className).not.toContain('current-branch');
    expect(screen.queryByTestId('pr-row-42-current-branch')).not.toBeInTheDocument();
  });

  it('does not highlight cross-fork PRs even when branch names match', () => {
    // Cross-fork PRs live in a different repo, so a coincidental branch-name
    // match between the fork and the user's working tree is meaningless.
    render(
      <PRRow
        pr={makePR({ crossFork: true })}
        directory="/repo"
        remoteId="local"
        remote="origin"
        currentBranch="tighten-slug"
      />,
    );

    expect(screen.getByTestId('pr-row-42').className).not.toContain('current-branch');
    expect(screen.queryByTestId('pr-row-42-current-branch')).not.toBeInTheDocument();
  });

  it('does not highlight when currentBranch is an empty string', () => {
    // Detached HEAD or missing git info would surface as "", which must not
    // match a (legitimately empty?) PR branch.
    render(
      <PRRow
        pr={makePR({ branch: '' })}
        directory="/repo"
        remoteId="local"
        remote="origin"
        currentBranch=""
      />,
    );

    expect(screen.getByTestId('pr-row-42').className).not.toContain('current-branch');
    expect(screen.queryByTestId('pr-row-42-current-branch')).not.toBeInTheDocument();
  });
});

describe('PRRow open-in-browser icon', () => {
  it('links to the PR url, opens in a new tab, and does not expand the row', () => {
    render(<PRRow pr={makePR({ headSha: 'abc123' })} directory="/repo" remoteId="local" remote="origin" />);

    const link = screen.getByTestId('pr-row-42-open');
    expect(link).toHaveAttribute('href', 'https://example.com/pr/42');
    expect(link).toHaveAttribute('target', '_blank');
    expect(link.getAttribute('rel')).toContain('noopener');

    // Clicking the icon must not toggle the expand state.
    fireEvent.click(link);
    expect(screen.queryByTestId('pr-detail-42')).not.toBeInTheDocument();
    expect(screen.getByRole('button', { expanded: false })).toBeInTheDocument();
  });
});

describe('PRRow detail slots', () => {
  it('keeps cross-fork guidance in the shared detail shell', () => {
    render(<PRRow pr={makePR({ crossFork: true })} directory="/repo" remoteId="local" remote="origin" />);

    fireEvent.click(screen.getByRole('button', { expanded: false }));
    expect(screen.getByText('Cross-fork PR — worktree launch will fetch the PR ref.')).toBeInTheDocument();
    expect(screen.getByTestId('launch-split-button')).toBeInTheDocument();
  });
});

describe('PRRow CI build-status indicator', () => {
  // Rows observed by the stubbed IntersectionObserver; `show` flips visibility.
  let observers: Array<{ cb: (e: { isIntersecting: boolean }[]) => void; disconnected: boolean }>;
  const show = (visible: boolean) =>
    act(() => {
      for (const o of observers) if (!o.disconnected) o.cb([{ isIntersecting: visible }]);
    });

  beforeEach(() => {
    vi.restoreAllMocks();
    localStorage.clear();
    clearPRChecksCache();
    observers = [];
    vi.stubGlobal('IntersectionObserver', class {
      o: (typeof observers)[number];
      constructor(cb: (e: { isIntersecting: boolean }[]) => void) {
        this.o = { cb, disconnected: false };
        observers.push(this.o);
      }
      observe() {}
      disconnect() { this.o.disconnected = true; }
    });
  });
  afterEach(() => {
    vi.useRealTimers();
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
  });

  const success = { state: 'success' as const, checks: [{ name: 'build', state: 'success' as const }] };
  const pending = { state: 'pending' as const, checks: [{ name: 'build', state: 'pending' as const }] };

  it('renders a neutral CI dot and never fetches without a head SHA', () => {
    const spy = vi.spyOn(api, 'fetchPRChecks');
    render(<PRRow pr={makePR()} directory="/repo" remoteId="local" remote="origin" />);
    show(true);
    expect(screen.getByTestId('pr-row-42-ci').className).toContain('oc-upstream-ci-dot-unknown');
    expect(spy).not.toHaveBeenCalled();
  });

  it('fetches once the collapsed row becomes visible, not before', async () => {
    const spy = vi.spyOn(api, 'fetchPRChecks').mockResolvedValue(success);
    render(<PRRow pr={makePR({ headSha: 'abc123' })} directory="/repo" remoteId="local" remote="origin" />);
    expect(spy).not.toHaveBeenCalled();
    expect(screen.getByTestId('pr-row-42-ci').className).toContain('oc-upstream-ci-dot-unknown');

    show(true);
    await waitFor(() => expect(screen.getByTestId('pr-row-42-ci').className).toContain('oc-upstream-ci-dot-success'));
    expect(spy).toHaveBeenCalledWith(expect.objectContaining({ dir: '/repo', remote: 'origin', sha: 'abc123' }));
    expect(screen.getByRole('button', { expanded: false })).toBeInTheDocument();
  });

  it('fetches without IntersectionObserver', async () => {
    vi.stubGlobal('IntersectionObserver', undefined);
    const spy = vi.spyOn(api, 'fetchPRChecks').mockResolvedValue(success);
    render(<PRRow pr={makePR({ headSha: 'abc123' })} directory="/repo" remoteId="local" remote="origin" />);
    await waitFor(() => expect(spy).toHaveBeenCalledTimes(1));
  });

  it('never fetches a SHA with a cached final status again, even after a reload', async () => {
    const spy = vi.spyOn(api, 'fetchPRChecks').mockResolvedValue(success);
    const first = render(<PRRow pr={makePR({ headSha: 'abc123' })} directory="/repo" remoteId="local" remote="origin" />);
    show(true);
    await waitFor(() => expect(spy).toHaveBeenCalledTimes(1));
    first.unmount();

    resetPRChecksMemoryForTest();
    render(<PRRow pr={makePR({ headSha: 'abc123' })} directory="/repo" remoteId="local" remote="origin" />);
    show(true);
    await waitFor(() => expect(screen.getByTestId('pr-row-42-ci').className).toContain('oc-upstream-ci-dot-success'));
    expect(spy).toHaveBeenCalledTimes(1);

    fireEvent.click(screen.getByRole('button', { expanded: false }));
    expect(screen.getByTestId('pr-detail-42-checks')).toBeInTheDocument();
  });

  it('polls a non-final status every 15s while visible, then caches the final one', async () => {
    vi.useFakeTimers();
    const spy = vi.spyOn(api, 'fetchPRChecks')
      .mockResolvedValueOnce(pending)
      .mockResolvedValueOnce(success);
    render(<PRRow pr={makePR({ headSha: 'abc123' })} directory="/repo" remoteId="local" remote="origin" />);
    show(true);
    await act(async () => {});
    expect(spy).toHaveBeenCalledTimes(1);
    expect(screen.getByTestId('pr-row-42-ci').className).toContain('oc-upstream-ci-dot-pending');
    expect(getCachedPRChecks(prChecksCacheKey('example.com', 'dries/ocman', 'abc123'))).toBeUndefined();

    await act(async () => { await vi.advanceTimersByTimeAsync(CI_POLL_MS); });
    expect(spy).toHaveBeenCalledTimes(2);
    expect(screen.getByTestId('pr-row-42-ci').className).toContain('oc-upstream-ci-dot-success');
    expect(getCachedPRChecks(prChecksCacheKey('example.com', 'dries/ocman', 'abc123'))?.state).toBe('success');

    await act(async () => { await vi.advanceTimersByTimeAsync(CI_POLL_MS * 3); });
    expect(spy).toHaveBeenCalledTimes(2);
  });

  it('keeps polling a failure while other checks still run', async () => {
    vi.useFakeTimers();
    const partial = { state: 'failure' as const, checks: [{ name: 'lint', state: 'failure' as const }, { name: 'e2e', state: 'pending' as const }] };
    const done = { state: 'failure' as const, checks: [{ name: 'lint', state: 'failure' as const }, { name: 'e2e', state: 'success' as const }] };
    const spy = vi.spyOn(api, 'fetchPRChecks').mockResolvedValueOnce(partial).mockResolvedValueOnce(done);
    render(<PRRow pr={makePR({ headSha: 'abc123' })} directory="/repo" remoteId="local" remote="origin" />);
    show(true);
    await act(async () => {});
    expect(getCachedPRChecks(prChecksCacheKey('example.com', 'dries/ocman', 'abc123'))).toBeUndefined();

    await act(async () => { await vi.advanceTimersByTimeAsync(CI_POLL_MS); });
    expect(spy).toHaveBeenCalledTimes(2);
    expect(getCachedPRChecks(prChecksCacheKey('example.com', 'dries/ocman', 'abc123'))).toEqual(done);
    await act(async () => { await vi.advanceTimersByTimeAsync(CI_POLL_MS * 2); });
    expect(spy).toHaveBeenCalledTimes(2);
  });

  it('does not share a cached status between repositories with the same SHA', async () => {
    const failed = { state: 'failure' as const, checks: [{ name: 'build', state: 'failure' as const }] };
    const spy = vi.spyOn(api, 'fetchPRChecks').mockResolvedValueOnce(success).mockResolvedValueOnce(failed);
    const first = render(<PRRow pr={makePR({ headSha: 'abc123' })} directory="/repo" remoteId="local" remote="origin" />);
    show(true);
    await waitFor(() => expect(screen.getByTestId('pr-row-42-ci').className).toContain('oc-upstream-ci-dot-success'));
    first.unmount();

    render(<PRRow pr={makePR({ headSha: 'abc123', host: 'code.example' })} directory="/repo" remoteId="local" remote="mirror" />);
    show(true);
    await waitFor(() => expect(screen.getByTestId('pr-row-42-ci').className).toContain('oc-upstream-ci-dot-failure'));
    expect(spy).toHaveBeenCalledTimes(2);
  });

  it('stops polling while the row is out of view and resumes when it returns', async () => {
    vi.useFakeTimers();
    const spy = vi.spyOn(api, 'fetchPRChecks').mockResolvedValue(pending);
    render(<PRRow pr={makePR({ headSha: 'abc123' })} directory="/repo" remoteId="local" remote="origin" />);
    show(true);
    await act(async () => {});
    expect(spy).toHaveBeenCalledTimes(1);

    show(false);
    await act(async () => { await vi.advanceTimersByTimeAsync(CI_POLL_MS * 3); });
    expect(spy).toHaveBeenCalledTimes(1);
    expect(screen.getByTestId('pr-row-42-ci').className).toContain('oc-upstream-ci-dot-pending');

    show(true);
    await act(async () => {});
    expect(spy).toHaveBeenCalledTimes(2);
  });

  it('retries a failed fetch after 15s and shows the error meanwhile', async () => {
    vi.useFakeTimers();
    const spy = vi.spyOn(api, 'fetchPRChecks')
      .mockRejectedValueOnce(new Error('boom'))
      .mockResolvedValueOnce(success);
    render(<PRRow pr={makePR({ headSha: 'abc123' })} directory="/repo" remoteId="local" remote="origin" />);
    show(true);
    await act(async () => {});
    fireEvent.click(screen.getByRole('button', { expanded: false }));
    expect(screen.getByText('Failed to load checks.')).toBeInTheDocument();

    await act(async () => { await vi.advanceTimersByTimeAsync(CI_POLL_MS); });
    expect(spy).toHaveBeenCalledTimes(2);
    expect(screen.getByTestId('pr-detail-42-checks')).toBeInTheDocument();
  });

  it('aborts a stale request and clears the dot when the head SHA changes', async () => {
    const spy = vi.spyOn(api, 'fetchPRChecks').mockReturnValueOnce(new Promise(() => {})).mockResolvedValue(success);
    const { rerender } = render(
      <PRRow pr={makePR({ headSha: 'old' })} directory="/repo" remoteId="local" remote="origin" />,
    );
    show(true);
    await waitFor(() => expect(spy).toHaveBeenCalledTimes(1));
    const oldSignal = spy.mock.calls[0][0].signal;

    rerender(<PRRow pr={makePR({ headSha: 'new' })} directory="/repo" remoteId="local" remote="origin" />);
    expect(oldSignal?.aborted).toBe(true);
    await waitFor(() => expect(spy).toHaveBeenCalledTimes(2));
    expect(spy.mock.calls[1][0].sha).toBe('new');
  });
});

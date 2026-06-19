// @vitest-environment jsdom
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, fireEvent, waitFor } from '@testing-library/react';
import { PRRow } from './PRRow';
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
  it('highlights the row when the PR branch matches the current branch', () => {
    render(<PRRow pr={makePR()} directory="/repo" remote="origin" currentBranch="tighten-slug" />);

    const row = screen.getByTestId('pr-row-42');
    expect(row.className).toContain('current-branch');

    const badge = screen.getByTestId('pr-row-42-current-branch');
    expect(badge).toBeInTheDocument();
    expect(badge.getAttribute('title')).toContain('tighten-slug');
  });

  it('does not highlight when the current branch differs', () => {
    render(<PRRow pr={makePR()} directory="/repo" remote="origin" currentBranch="main" />);

    expect(screen.getByTestId('pr-row-42').className).not.toContain('current-branch');
    expect(screen.queryByTestId('pr-row-42-current-branch')).not.toBeInTheDocument();
  });

  it('does not highlight when currentBranch is undefined (git info not yet loaded)', () => {
    render(<PRRow pr={makePR()} directory="/repo" remote="origin" />);

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
    render(<PRRow pr={makePR({ headSha: 'abc123' })} directory="/repo" remote="origin" />);

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

describe('PRRow CI build-status indicator', () => {
  beforeEach(() => {
    vi.restoreAllMocks();
  });
  afterEach(() => {
    vi.restoreAllMocks();
  });

  it('renders a neutral (unknown) CI dot when the PR has no head SHA', () => {
    render(<PRRow pr={makePR()} directory="/repo" remote="origin" />);
    const dot = screen.getByTestId('pr-row-42-ci');
    expect(dot).toBeInTheDocument();
    expect(dot.className).toContain('oc-upstream-ci-dot-unknown');
  });

  it('does not fetch checks when the PR has no head SHA', () => {
    const spy = vi.spyOn(api, 'fetchPRChecks');
    render(<PRRow pr={makePR()} directory="/repo" remote="origin" />);
    fireEvent.mouseEnter(screen.getByTestId('pr-row-42'));
    expect(spy).not.toHaveBeenCalled();
  });

  it('renders a neutral CI dot when a head SHA is present', () => {
    render(<PRRow pr={makePR({ headSha: 'abc123' })} directory="/repo" remote="origin" />);
    const dot = screen.getByTestId('pr-row-42-ci');
    expect(dot).toBeInTheDocument();
    expect(dot.className).toContain('oc-upstream-ci-dot-unknown');
  });

  it('lazily fetches checks on hover and colors the dot', async () => {
    const spy = vi.spyOn(api, 'fetchPRChecks').mockResolvedValue({
      state: 'success',
      checks: [{ name: 'build', state: 'success' }],
    });

    render(<PRRow pr={makePR({ headSha: 'abc123' })} directory="/repo" remote="origin" />);

    // No fetch until interaction.
    expect(spy).not.toHaveBeenCalled();

    fireEvent.mouseEnter(screen.getByTestId('pr-row-42'));

    await waitFor(() => {
      expect(screen.getByTestId('pr-row-42-ci').className).toContain('oc-upstream-ci-dot-success');
    });
    expect(spy).toHaveBeenCalledWith(
      expect.objectContaining({ dir: '/repo', remote: 'origin', sha: 'abc123' }),
    );
  });

  it('fetches at most once across hover and expand', async () => {
    const spy = vi.spyOn(api, 'fetchPRChecks').mockResolvedValue({
      state: 'failure',
      checks: [{ name: 'test', state: 'failure', url: 'https://ci/test' }],
    });

    render(<PRRow pr={makePR({ headSha: 'abc123' })} directory="/repo" remote="origin" />);

    fireEvent.mouseEnter(screen.getByTestId('pr-row-42'));
    fireEvent.click(screen.getByRole('button', { expanded: false }));

    await waitFor(() => {
      expect(screen.getByTestId('pr-detail-42-checks')).toBeInTheDocument();
    });
    // Hover + click must not double-fetch.
    expect(spy).toHaveBeenCalledTimes(1);
    expect(screen.getByText('test')).toBeInTheDocument();
  });
});

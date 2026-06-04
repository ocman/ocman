// @vitest-environment jsdom
import { describe, it, expect } from 'vitest';
import { render, screen } from '@testing-library/react';
import { PRRow } from './PRRow';
import type { PR } from '../../lib/upstreamApi';

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

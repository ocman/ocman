// @vitest-environment jsdom
import { render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { PRRow } from './PRRow';
import { usePRMergeability } from '../../lib/usePRMergeability';
import type { PR } from '../../lib/upstreamApi';

vi.mock('../../lib/usePRMergeability');

const pr = { number: 42, title: 'Approved PR', status: 'open', author: 'dries', host: 'example.com', repo: 'dries/ocman', url: 'https://example.com/pr/42' } as PR;

describe('PRRow approval indicator', () => {
  it.each([true, false, null, undefined])('shows a checkmark only for confirmed approval %s', (approved) => {
    vi.mocked(usePRMergeability).mockReturnValue({ mergeable: true, approved });
    render(<PRRow pr={pr} directory="/repo" remoteId="local" remote="origin" />);
    const dot = screen.getByTestId('pr-row-42-ci');
    expect(screen.queryByTestId('pr-row-42-approved') !== null).toBe(approved === true);
    expect(dot.getAttribute('aria-label')?.includes('Approved')).toBe(approved === true);
    expect(dot.getAttribute('title')?.includes('Approved')).toBe(approved === true);
    if (approved) {
      expect(dot).toContainElement(screen.getByTestId('pr-row-42-approved'));
      expect(screen.getByTestId('pr-row-42-approved')).toHaveAttribute('aria-hidden', 'true');
    }
  });

  it('keeps the unmergeable slash when an approved PR has conflicts', () => {
    vi.mocked(usePRMergeability).mockReturnValue({ mergeable: false, approved: true });
    render(<PRRow pr={pr} directory="/repo" remoteId="local" remote="origin" />);
    expect(screen.getByTestId('pr-row-42-ci')).toHaveClass('oc-upstream-ci-dot-unmergeable');
    expect(screen.getByTestId('pr-row-42-ci').getAttribute('aria-label')).toContain('Not mergeable · Approved');
    expect(screen.getByTestId('pr-row-42-approved')).toBeInTheDocument();
  });
});

// @vitest-environment jsdom

import { render, screen } from '@testing-library/react';
import { expect, it, vi } from 'vitest';
import { SessionChangesSidebar } from './SessionChangesSidebar';
import { WorkingTreeChangesSidebar } from './WorkingTreeChangesSidebar';

vi.mock('../lib/useCapabilities', () => ({ usePlatformCapabilities: () => ({ fileChanges: true }) }));
vi.mock('../lib/useSessionChanges', () => ({
  useSessionChanges: () => ({
    data: {
      supported: true,
      filesChanged: 1,
      totalAdditions: 1,
      totalDeletions: 0,
      files: [{
        path: '/repo/session.ts',
        displayPath: 'session.ts',
        additions: 1,
        deletions: 0,
        editCount: 1,
        firstEditAt: 0,
        lastEditAt: 0,
        patch: 'session patch',
        edits: [],
      }],
    },
    loading: false,
    error: 'network failure',
    refresh: vi.fn(),
  }),
}));
vi.mock('../lib/useWorkingTreeDiff', () => ({
  useWorkingTreeDiff: () => ({
    data: {
      repo: '/repo',
      branch: 'main',
      ahead: 0,
      behind: 0,
      truncated: false,
      files: [{
        path: 'working.ts',
        status: 'modified',
        additions: 1,
        deletions: 0,
        diff: 'working patch',
        isBinary: false,
      }],
    },
    loading: false,
    error: 'network failure',
    notRepo: false,
    refresh: vi.fn(),
  }),
}));

it('keeps session changes visible when a refresh fails', () => {
  render(<SessionChangesSidebar sessionId="s1" platformId="test" />);

  expect(screen.getByRole('button', { name: 'session.ts+1' })).toBeInTheDocument();
  expect(screen.getByRole('alert')).toHaveTextContent('data may be out of date');
  expect(screen.getByRole('alert')).toHaveTextContent('network failure');
});

it('keeps working tree changes visible when a refresh fails', () => {
  render(<WorkingTreeChangesSidebar directory="/repo" />);

  expect(screen.getByRole('button', { name: 'Mworking.ts+1' })).toBeInTheDocument();
  expect(screen.getByRole('alert')).toHaveTextContent('data may be out of date');
  expect(screen.getByRole('alert')).toHaveTextContent('network failure');
});

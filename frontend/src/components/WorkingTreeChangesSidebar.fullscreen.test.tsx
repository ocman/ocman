// @vitest-environment jsdom

import { render, screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { expect, it, vi } from 'vitest';
import { WorkingTreeChangesSidebar } from './WorkingTreeChangesSidebar';

vi.mock('../lib/useWorkingTreeDiff', () => ({
  useWorkingTreeDiff: () => ({
    data: {
      repo: '/repo',
      branch: 'main',
      ahead: 0,
      behind: 0,
      truncated: false,
      files: [{
        path: 'src/b.ts',
        oldPath: 'src/a.ts',
        status: 'renamed',
        additions: 1,
        deletions: 1,
        diff: 'diff --git a/src/a.ts b/src/b.ts',
        isBinary: false,
      }],
    },
    loading: false,
    error: null,
    notRepo: false,
    refresh: vi.fn(),
  }),
}));

it('passes structured rename paths to the fullscreen browser', async () => {
  const user = userEvent.setup();
  render(<WorkingTreeChangesSidebar directory="/repo" />);

  await user.click(screen.getByRole('button', { name: 'Fullscreen' }));

  const dialog = screen.getByRole('dialog', { name: 'Working tree' });
  expect(within(dialog).getByText('a.ts → b.ts')).toBeInTheDocument();
  expect(within(dialog).getByText('src')).toBeInTheDocument();
});

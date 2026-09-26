// @vitest-environment jsdom

import { render, screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { expect, it, vi } from 'vitest';
import type { FileChange } from '../lib/api';
import { ChangedFileRow } from './ChangedFileRow';
import { FileChangeGroup } from './FileChangeGroup';
import { SessionChangesSidebar } from './SessionChangesSidebar';
import { WorkingTreeChangesSidebar } from './WorkingTreeChangesSidebar';

vi.mock('./RawDiffView', () => ({
  RawDiffView: ({ diff, filePath }: { diff: string; filePath: string }) => <pre aria-label={filePath}>{diff}</pre>,
}));
vi.mock('./DiffView', () => ({
  DiffView: ({ before, after }: { before: string; after: string }) => <pre>{before} → {after}</pre>,
}));
vi.mock('../lib/useCapabilities', () => ({ usePlatformCapabilities: () => ({ fileChanges: true }) }));
vi.mock('../lib/useSessionChanges', () => ({
  useSessionChanges: () => ({
    data: { supported: true, files: [change], filesChanged: 1, totalAdditions: 3, totalDeletions: 2 },
    loading: false, error: null, refresh: vi.fn(),
  }),
}));
vi.mock('../lib/useWorkingTreeDiff', () => ({
  useWorkingTreeDiff: () => ({
    data: { files: [
      { path: 'src/new.ts', oldPath: 'src/old.ts', status: 'renamed', additions: 3, deletions: 2, diff: 'working patch' },
      { path: 'image.png', status: 'untracked', additions: 0, deletions: 0, isBinary: true },
    ] },
    loading: false, error: null, notRepo: false, refresh: vi.fn(),
  }),
}));

const change: FileChange = {
  path: '/repo/src/file.ts', displayPath: 'src/file.ts', additions: 3, deletions: 2,
  editCount: 2, firstEditAt: 0, lastEditAt: 1, patch: 'session patch',
  edits: [
    { partId: '1', messageId: 'm1', timeCreated: 0, tool: 'edit', additions: 1, deletions: 1, patch: 'first edit' },
    { partId: '2', messageId: 'm2', timeCreated: 1, tool: 'edit', additions: 2, deletions: 1, before: 'old', after: 'new' },
  ],
};

function dialog() {
  return within(screen.getByRole('dialog'));
}

it('calls onOpen on click and keyboard activation', async () => {
  const user = userEvent.setup();
  const onOpen = vi.fn();
  render(<ChangedFileRow path="file.ts" additions={0} deletions={0} onOpen={onOpen} />);
  expect(screen.getByTitle('file.ts')).toHaveTextContent('file.ts');
  await user.tab();
  await user.keyboard('{Enter}');
  await user.click(screen.getByRole('button', { name: 'file.ts' }));
  expect(onOpen).toHaveBeenCalledTimes(2);
});

it('opens the session changes modal on the clicked file with its individual edits', async () => {
  const user = userEvent.setup();
  render(<SessionChangesSidebar sessionId="s1" platformId="test" />);
  const row = screen.getByRole('button', { name: 'src/file.ts+3-2' });
  expect(within(row).getByTitle(change.path)).toHaveTextContent(change.displayPath);
  expect(screen.queryByText('session patch')).not.toBeInTheDocument();
  await user.click(row);
  expect(dialog().getByLabelText(change.path)).toHaveTextContent('session patch');
  await user.click(dialog().getByRole('button', { name: 'Show 2 individual edits' }));
  expect(dialog().getByText('first edit')).toBeInTheDocument();
  expect(dialog().getByText('old → new')).toBeInTheDocument();
  await user.click(dialog().getByRole('button', { name: 'Hide 2 individual edits' }));
  expect(dialog().queryByText('first edit')).not.toBeInTheDocument();
});

it('falls back to the legacy session diff without an edits toggle', () => {
  render(<FileChangeGroup change={{ ...change, editCount: 1, patch: '', before: 'before', after: 'after' }} />);
  expect(screen.getByText('before → after')).toBeInTheDocument();
  expect(screen.queryByRole('button', { name: /individual edits/ })).not.toBeInTheDocument();
});

it('opens the working tree modal on the clicked file, keeping badges and binary fallback', async () => {
  const user = userEvent.setup();
  render(<WorkingTreeChangesSidebar directory="/repo" />);
  const renamed = screen.getByRole('button', { name: 'Rsrc/old.ts → src/new.ts+3-2' });
  const binary = screen.getByRole('button', { name: '?image.png' });
  expect(within(renamed).getByTitle('renamed')).toHaveTextContent('R');
  expect(within(renamed).getByTitle('src/new.ts')).toHaveTextContent('src/old.ts → src/new.ts');
  expect(within(binary).getByTitle('untracked')).toHaveTextContent('?');
  expect(screen.queryByText('working patch')).not.toBeInTheDocument();

  // The binary file is second in the list, so this proves the modal
  // opens on the clicked row rather than the first file.
  await user.click(binary);
  expect(dialog().getByText('Binary file — diff not shown.')).toBeInTheDocument();
  expect(dialog().queryByText('working patch')).not.toBeInTheDocument();
  await user.click(dialog().getByRole('button', { name: 'Close' }));

  await user.click(renamed);
  expect(dialog().getByLabelText('src/new.ts')).toHaveTextContent('working patch');
});

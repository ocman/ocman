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

it('lazily mounts the body and footer and supports keyboard disclosure', async () => {
  const user = userEvent.setup();
  const body = vi.fn(() => <div>diff body</div>);
  const Body = body;
  render(<ChangedFileRow path="file.ts" additions={0} deletions={0} expandedFooter={<p>footer</p>}><Body /></ChangedFileRow>);
  const row = screen.getByRole('button', { name: 'file.ts', expanded: false });
  expect(body).not.toHaveBeenCalled();
  expect(screen.queryByText('footer')).not.toBeInTheDocument();
  expect(screen.getByTitle('file.ts')).toHaveTextContent('file.ts');
  await user.tab();
  await user.keyboard('{Enter}');
  expect(row).toHaveAttribute('aria-expanded', 'true');
  expect(screen.getByText('diff body')).toBeInTheDocument();
  expect(screen.getByText('footer')).toBeInTheDocument();
  await user.keyboard(' ');
  expect(row).toHaveAttribute('aria-expanded', 'false');
  expect(screen.queryByText('diff body')).not.toBeInTheDocument();
});

it('preserves session paths, counts, and individual-edit state across collapse', async () => {
  const user = userEvent.setup();
  render(<SessionChangesSidebar sessionId="s1" platformId="test" />);
  const row = screen.getByRole('button', { name: 'src/file.ts+3-2', expanded: false });
  expect(within(row).getByTitle(change.path)).toHaveTextContent(change.displayPath);
  expect(screen.queryByText('session patch')).not.toBeInTheDocument();
  await user.click(row);
  expect(screen.getByLabelText(change.path)).toHaveTextContent('session patch');
  await user.click(screen.getByRole('button', { name: 'Show 2 individual edits' }));
  expect(screen.getByText('first edit')).toBeInTheDocument();
  expect(screen.getByText('old → new')).toBeInTheDocument();
  await user.click(row);
  expect(screen.queryByText('first edit')).not.toBeInTheDocument();
  await user.click(row);
  expect(screen.getByText('first edit')).toBeInTheDocument();
  await user.click(screen.getByRole('button', { name: 'Hide 2 individual edits' }));
  expect(screen.queryByText('first edit')).not.toBeInTheDocument();
});

it('retains default expansion and legacy session diff fallback', () => {
  render(<FileChangeGroup change={{ ...change, displayPath: '', editCount: 1, patch: '', before: 'before', after: 'after' }} defaultExpanded />);
  expect(screen.getByRole('button', { name: `${change.path}+3-2`, expanded: true })).toBeInTheDocument();
  expect(screen.getByText('before → after')).toBeInTheDocument();
  expect(screen.queryByRole('button', { name: /individual edits/ })).not.toBeInTheDocument();
});

it('preserves working-tree badges, rename paths, binary fallback, and independent expansion', async () => {
  const user = userEvent.setup();
  render(<WorkingTreeChangesSidebar directory="/repo" />);
  const renamed = screen.getByRole('button', { name: 'Rsrc/old.ts → src/new.ts+3-2', expanded: false });
  const binary = screen.getByRole('button', { name: '?image.png', expanded: false });
  expect(within(renamed).getByTitle('renamed')).toHaveTextContent('R');
  expect(within(renamed).getByTitle('src/new.ts')).toHaveTextContent('src/old.ts → src/new.ts');
  expect(within(binary).getByTitle('untracked')).toHaveTextContent('?');
  expect(screen.queryByText('working patch')).not.toBeInTheDocument();
  await user.click(renamed);
  await user.click(binary);
  expect(screen.getByLabelText('src/new.ts')).toHaveTextContent('working patch');
  expect(screen.getByText('Binary file — diff not shown.')).toBeInTheDocument();
  await user.click(renamed);
  expect(screen.queryByText('working patch')).not.toBeInTheDocument();
  expect(binary).toHaveAttribute('aria-expanded', 'true');
});

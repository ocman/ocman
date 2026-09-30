// @vitest-environment jsdom
import { beforeEach, expect, it, vi } from 'vitest';
import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router-dom';
import { HeaderContext } from '../lib/headerContext';
import { api } from '../lib/api';
import { AppHeader } from './AppHeader';

// Shiki highlighting is not the subject here.
vi.mock('@pierre/diffs/react', () => ({
  File: ({ file }: { file: { name: string; contents: string } }) => <pre>{file.contents}</pre>,
}));

function tree() {
  return within(screen.getByTestId('explore-tree').shadowRoot as unknown as HTMLElement);
}

function renderHeader(remoteId?: string) {
  const info = { sessionId: 's1', sessionTitle: 'T', sessionProject: 'repo', sessionProjectFull: '/repo/wt', sessionRemoteId: remoteId };
  render(
    <MemoryRouter initialEntries={['/session/s1']}>
      <HeaderContext.Provider value={{ info, setInfo: vi.fn() }}>
        <AppHeader onOpenNav={vi.fn()} />
      </HeaderContext.Provider>
    </MemoryRouter>,
  );
}

beforeEach(() => {
  vi.restoreAllMocks();
  vi.spyOn(api, 'repoFiles').mockResolvedValue({ root: '/repo/wt', files: ['src/a.ts', 'img.png', 'big.txt'] });
  vi.spyOn(api, 'repoFile').mockImplementation(async (_dir, path) => ({
    'src/a.ts': { path, content: 'const a = 1;', size: 12 },
    'img.png': { path, content: '', size: 9, binary: true },
    'big.txt': { path, content: 'head', size: 2e6, truncated: true },
  })[path]!);
});

it('opens from the header and shows the selected file', async () => {
  const user = userEvent.setup();
  renderHeader('rem1');
  await user.click(screen.getByRole('button', { name: 'Explore files' }));

  await waitFor(() => expect(tree().getByRole('treeitem', { name: 'src' })).toBeInTheDocument());
  expect(api.repoFiles).toHaveBeenCalledWith('/repo/wt', 'rem1', expect.anything());
  expect(screen.getByText(/3 files/)).toBeInTheDocument();
  expect(screen.getByText('Select a file to view it.')).toBeInTheDocument();

  await user.click(tree().getByRole('treeitem', { name: 'img.png' }));
  expect(await screen.findByText('Binary file, not shown.')).toBeInTheDocument();
  await user.click(tree().getByRole('treeitem', { name: 'big.txt' }));
  expect(await screen.findByText('Showing the first 1 MiB of this file.')).toBeInTheDocument();
  expect(api.repoFile).toHaveBeenLastCalledWith('/repo/wt', 'big.txt', 'rem1', expect.anything());

  await user.click(screen.getByRole('button', { name: 'Close' }));
  expect(screen.queryByTestId('explore-modal')).not.toBeInTheDocument();
});

it('defaults the owner to local and surfaces a listing error', async () => {
  vi.mocked(api.repoFiles).mockRejectedValue(new Error('not a repo'));
  const user = userEvent.setup();
  renderHeader();
  await user.click(screen.getByRole('button', { name: 'Explore files' }));
  expect(await screen.findByText('not a repo')).toBeInTheDocument();
  expect(api.repoFiles).toHaveBeenCalledWith('/repo/wt', 'local', expect.anything());
});

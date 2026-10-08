// @vitest-environment jsdom
import { beforeEach, expect, it, vi } from 'vitest';
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router-dom';
import { HeaderContext } from '../lib/headerContext';
import { api } from '../lib/api';
import { AppHeader } from './AppHeader';
import checkboxStyles from './CheckboxField.module.css';

// Shiki highlighting is not the subject here.
vi.mock('@pierre/diffs/react', () => ({
  File: ({ file }: { file: { name: string; contents: string } }) => <pre>{file.contents}</pre>,
}));

function tree() {
  return within(screen.getByTestId('explore-tree').shadowRoot as unknown as HTMLElement);
}

function header(remoteId?: string, dir = '/repo/wt') {
  const info = { sessionId: 's1', sessionTitle: 'T', sessionProject: 'repo', sessionProjectFull: dir, sessionRemoteId: remoteId };
  return (
    <MemoryRouter initialEntries={['/session/s1']}>
      <HeaderContext.Provider value={{ info, setInfo: vi.fn() }}>
        <AppHeader onOpenNav={vi.fn()} />
      </HeaderContext.Provider>
    </MemoryRouter>
  );
}

function renderHeader(remoteId?: string) {
  return render(header(remoteId));
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
  expect(api.repoFiles).toHaveBeenCalledWith('/repo/wt', 'rem1', expect.anything(), false);
  expect(screen.getByText(/3 files/)).toBeInTheDocument();
  expect(screen.getByText('Select a file to view it.')).toBeInTheDocument();

  await user.click(tree().getByRole('treeitem', { name: 'img.png' }));
  expect(await screen.findByText('Binary file, not shown.')).toBeInTheDocument();
  await user.click(tree().getByRole('treeitem', { name: 'big.txt' }));
  expect(await screen.findByText('Showing the first 1 MiB of this file.')).toBeInTheDocument();
  expect(api.repoFile).toHaveBeenLastCalledWith('/repo/wt', 'big.txt', 'rem1', expect.anything(), false);

  await user.click(screen.getByRole('button', { name: 'Close' }));
  expect(screen.queryByTestId('explore-modal')).not.toBeInTheDocument();
});

it('defaults the owner to local and surfaces a listing error', async () => {
  vi.mocked(api.repoFiles).mockRejectedValue(new Error('not a repo'));
  const user = userEvent.setup();
  renderHeader();
  await user.click(screen.getByRole('button', { name: 'Explore files' }));
  expect(await screen.findByText('not a repo')).toBeInTheDocument();
  expect(api.repoFiles).toHaveBeenCalledWith('/repo/wt', 'local', expect.anything(), false);
});

it.each([
  ['directory', 'rem1', '/repo/other'],
  ['owner', 'rem2', '/repo/wt'],
])('drops the open file when the %s changes', async (_what, remoteId, dir) => {
  const user = userEvent.setup();
  const { rerender } = renderHeader('rem1');
  await user.click(screen.getByRole('button', { name: 'Explore files' }));
  await waitFor(() => expect(tree().getByRole('treeitem', { name: 'img.png' })).toBeInTheDocument());
  await user.click(tree().getByRole('treeitem', { name: 'img.png' }));
  expect(await screen.findByText('Binary file, not shown.')).toBeInTheDocument();

  rerender(header(remoteId, dir));
  await waitFor(() => expect(screen.queryByText('Binary file, not shown.')).not.toBeInTheDocument());
  expect(api.repoFiles).toHaveBeenLastCalledWith(dir, remoteId, expect.anything(), false);
});

it('toggles gitignored files', async () => {
  const user = userEvent.setup();
  renderHeader('rem1');
  await user.click(screen.getByRole('button', { name: 'Explore files' }));
  await waitFor(() => expect(tree().getByRole('treeitem', { name: 'img.png' })).toBeInTheDocument());
  expect(api.repoFiles).toHaveBeenLastCalledWith('/repo/wt', 'rem1', expect.anything(), false);
  expect(screen.getByRole('checkbox', { name: 'Show ignored files' })).toHaveClass(checkboxStyles.input);

  vi.mocked(api.repoFiles).mockResolvedValue({ root: '/repo/wt', files: ['src/a.ts', '.env'] });
  await user.click(screen.getByRole('checkbox', { name: 'Show ignored files' }));
  await waitFor(() => expect(tree().getByRole('treeitem', { name: '.env' })).toBeInTheDocument());
  expect(api.repoFiles).toHaveBeenLastCalledWith('/repo/wt', 'rem1', expect.anything(), true);
  await user.click(tree().getByRole('treeitem', { name: '.env' }));
  await waitFor(() => expect(api.repoFile).toHaveBeenLastCalledWith('/repo/wt', '.env', 'rem1', expect.anything(), true));

  await user.click(screen.getByRole('checkbox', { name: 'Show ignored files' }));
  expect(screen.getByRole('checkbox', { name: 'Show ignored files' })).not.toBeChecked();
  expect(screen.getByText('Select a file to view it.')).toBeInTheDocument();
  expect(api.repoFiles).toHaveBeenLastCalledWith('/repo/wt', 'rem1', expect.anything(), false);
});

it('previews images from the owner and resets decode errors on selection', async () => {
  vi.mocked(api.repoFiles).mockResolvedValue({ root: '/repo/wt', files: ['a.png', 'b.svg'] });
  vi.mocked(api.repoFile).mockImplementation(async (_dir, path) => ({
    path, content: 'aW1hZ2U=', size: 5, binary: true,
    mimeType: path.endsWith('.svg') ? 'image/svg+xml' : 'image/png',
  }));
  const user = userEvent.setup();
  renderHeader('rem1');
  await user.click(screen.getByRole('button', { name: 'Explore files' }));
  await waitFor(() => expect(tree().getByRole('treeitem', { name: 'a.png' })).toBeInTheDocument());
  await user.click(tree().getByRole('treeitem', { name: 'a.png' }));
  const img = await screen.findByRole('img', { name: 'a.png' });
  expect(img).toHaveAttribute('src', 'data:image/png;base64,aW1hZ2U=');
  expect(api.repoFile).toHaveBeenLastCalledWith('/repo/wt', 'a.png', 'rem1', expect.anything(), false);
  fireEvent.error(img);
  expect(screen.getByText('Unable to display this image.')).toBeInTheDocument();
  await user.click(tree().getByRole('treeitem', { name: 'b.svg' }));
  expect(await screen.findByRole('img', { name: 'b.svg' })).toHaveAttribute('src', 'data:image/svg+xml;base64,aW1hZ2U=');
  expect(screen.queryByText('Unable to display this image.')).not.toBeInTheDocument();
});

it('does not render a truncated image', async () => {
  vi.mocked(api.repoFile).mockResolvedValue({ path: 'img.png', content: '', size: 11e6, truncated: true, mimeType: 'image/png' });
  const user = userEvent.setup();
  renderHeader();
  await user.click(screen.getByRole('button', { name: 'Explore files' }));
  await waitFor(() => expect(tree().getByRole('treeitem', { name: 'img.png' })).toBeInTheDocument());
  await user.click(tree().getByRole('treeitem', { name: 'img.png' }));
  expect(await screen.findByText('Image exceeds the 10 MiB preview limit.')).toBeInTheDocument();
  expect(screen.queryByRole('img')).not.toBeInTheDocument();
});

// @vitest-environment jsdom
import { describe, expect, it, vi } from 'vitest';
import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { DiffFullscreenModal, type FullscreenDiffFile } from './DiffFullscreenModal';
import { useFullscreenDiff } from './useFullscreenDiff';

const files: FullscreenDiffFile[] = [
  { key: 'a', path: 'src/one.ts', additions: 3, deletions: 1, body: <div>diff-one</div> },
  { key: 'b', path: 'src/deep/two.ts', additions: 0, deletions: 5, body: <div>diff-two</div> },
];

// The tree renders inside a shadow root, which screen queries skip.
function tree() {
  return within(screen.getByTestId('changed-files-tree').shadowRoot as unknown as HTMLElement);
}

describe('DiffFullscreenModal', () => {
  it('shows the first file diff by default and switches on click', async () => {
    const user = userEvent.setup();
    render(<DiffFullscreenModal title="Working tree" files={files} onClose={vi.fn()} />);

    expect(screen.getByText('diff-one')).toBeInTheDocument();
    expect(screen.queryByText('diff-two')).not.toBeInTheDocument();

    await user.click(tree().getByRole('treeitem', { name: 'two.ts' }));

    expect(screen.getByText('diff-two')).toBeInTheDocument();
    expect(screen.queryByText('diff-one')).not.toBeInTheDocument();
  });

  it('keeps the diff when a directory row is clicked', async () => {
    const user = userEvent.setup();
    render(<DiffFullscreenModal title="Working tree" files={files} onClose={vi.fn()} />);
    await user.click(tree().getByRole('treeitem', { name: 'deep' }));
    expect(screen.getByText('diff-one')).toBeInTheDocument();
  });

  it('nests files under their directories with change counts', () => {
    render(<DiffFullscreenModal title="Working tree" files={files} onClose={vi.fn()} />);
    const t = tree();
    expect(t.getByRole('treeitem', { name: 'src' })).toBeInTheDocument();
    expect(t.getByRole('treeitem', { name: 'deep' })).toBeInTheDocument();
    expect(t.getByRole('treeitem', { name: 'one.ts' })).toHaveTextContent('+3');
    expect(t.getByRole('treeitem', { name: 'two.ts' })).toHaveTextContent('-5');
  });

  it('places a rename at its new path', () => {
    const renamed = [{
      ...files[0],
      path: 'src/b.ts',
      oldPath: 'src/a.ts',
      label: 'src/a.ts → src/b.ts',
      status: 'renamed',
    }];
    render(<DiffFullscreenModal title="Working tree" files={renamed} onClose={vi.fn()} />);

    expect(tree().getByRole('treeitem', { name: 'b.ts' })).toBeInTheDocument();
    expect(tree().queryByRole('treeitem', { name: 'a.ts' })).not.toBeInTheDocument();
  });

  it('does not treat an arrow in a filename as a rename', () => {
    const arrowFile = [{ ...files[0], path: 'src/a → b.ts', label: 'src/a → b.ts' }];
    render(<DiffFullscreenModal title="Working tree" files={arrowFile} onClose={vi.fn()} />);
    expect(tree().getByRole('treeitem', { name: 'a → b.ts' })).toBeInTheDocument();
  });

  it('follows a refreshed file list', async () => {
    const { rerender } = render(<DiffFullscreenModal title="Working tree" files={files} onClose={vi.fn()} />);
    rerender(<DiffFullscreenModal title="Working tree" files={[files[1]]} onClose={vi.fn()} />);
    await waitFor(() => expect(tree().queryByRole('treeitem', { name: 'one.ts' })).not.toBeInTheDocument());
    expect(screen.getByText('diff-two')).toBeInTheDocument();
  });

  it('opens on initialKey when given', () => {
    render(<DiffFullscreenModal title="Working tree" files={files} initialKey="b" onClose={vi.fn()} />);
    expect(screen.getByText('diff-two')).toBeInTheDocument();
  });

  it('renders an empty state with no files', () => {
    render(<DiffFullscreenModal title="Session changes" files={[]} onClose={vi.fn()} />);
    expect(screen.getByText('No changes to show.')).toBeInTheDocument();
    expect(screen.getByText('0 files')).toBeInTheDocument();
  });

  it('closes via the close button', async () => {
    const user = userEvent.setup();
    const onClose = vi.fn();
    render(<DiffFullscreenModal title="Working tree" files={files} onClose={onClose} />);
    expect(screen.getByRole('button', { name: 'Close' })).toHaveClass('oc-icon-button');
    expect(screen.getByTestId('modal-header')).toHaveTextContent('2 files');
    await user.click(screen.getByRole('button', { name: 'Close' }));
    expect(onClose).toHaveBeenCalled();
  });
});

function Harness({ onFullscreen }: { onFullscreen?: (open: () => void) => void }) {
  const { open, modal } = useFullscreenDiff('Working tree', files, onFullscreen);
  return (
    <div>
      <button type="button" onClick={open}>open it</button>
      {modal}
    </div>
  );
}

describe('useFullscreenDiff', () => {
  it('opens via the returned callback and closes again', async () => {
    const user = userEvent.setup();
    render(<Harness />);

    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    await user.click(screen.getByRole('button', { name: 'open it' }));
    expect(screen.getByRole('dialog', { name: 'Working tree' })).toBeInTheDocument();
    expect(screen.getByText('diff-one')).toBeInTheDocument();

    await user.click(screen.getByRole('button', { name: 'Close' }));
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
  });

  it('registers a stable open callback with onFullscreen', async () => {
    const user = userEvent.setup();
    const onFullscreen = vi.fn<(open: () => void) => void>();
    render(<Harness onFullscreen={onFullscreen} />);

    expect(onFullscreen).toHaveBeenCalledTimes(1);
    const open = onFullscreen.mock.calls[0][0];
    await user.click(screen.getByRole('button', { name: 'open it' }));
    await user.click(screen.getByRole('button', { name: 'Close' }));
    // Re-renders must not re-register a new callback.
    expect(onFullscreen).toHaveBeenCalledTimes(1);
    expect(onFullscreen.mock.calls[0][0]).toBe(open);
  });
});

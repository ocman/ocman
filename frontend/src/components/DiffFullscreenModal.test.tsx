// @vitest-environment jsdom
import { describe, expect, it, vi } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { DiffFullscreenModal, type FullscreenDiffFile } from './DiffFullscreenModal';
import { useFullscreenDiff } from './useFullscreenDiff';

const files: FullscreenDiffFile[] = [
  { key: 'a', path: 'src/one.ts', additions: 3, deletions: 1, body: <div>diff-one</div> },
  { key: 'b', path: 'src/deep/two.ts', additions: 0, deletions: 5, body: <div>diff-two</div> },
];

describe('DiffFullscreenModal', () => {
  it('shows the first file diff by default and switches on click', async () => {
    const user = userEvent.setup();
    render(<DiffFullscreenModal title="Working tree" files={files} onClose={vi.fn()} />);

    expect(screen.getByText('diff-one')).toBeInTheDocument();
    expect(screen.queryByText('diff-two')).not.toBeInTheDocument();

    await user.click(screen.getByRole('button', { name: /two\.ts/ }));

    expect(screen.getByText('diff-two')).toBeInTheDocument();
    expect(screen.queryByText('diff-one')).not.toBeInTheDocument();
  });

  it('splits the path into name and directory', () => {
    render(<DiffFullscreenModal title="Working tree" files={files} onClose={vi.fn()} />);
    expect(screen.getByText('two.ts')).toBeInTheDocument();
    expect(screen.getByText('src/deep')).toBeInTheDocument();
  });

  it('splits both sides of a renamed path', () => {
    const renamed = [{
      ...files[0],
      path: 'src/b.ts',
      label: 'src/a.ts → src/b.ts',
    }];
    render(<DiffFullscreenModal title="Working tree" files={renamed} onClose={vi.fn()} />);

    expect(screen.getByText('a.ts → b.ts')).toBeInTheDocument();
    expect(screen.getByText('src')).toBeInTheDocument();
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

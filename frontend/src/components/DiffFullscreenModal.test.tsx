// @vitest-environment jsdom
import { describe, expect, it, vi } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { DiffFullscreenModal, type FullscreenDiffFile } from './DiffFullscreenModal';

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

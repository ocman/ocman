// @vitest-environment jsdom
import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { Composer } from './Composer';

describe('Composer queue', () => {
  it('renders queued shell commands in the follow-up queue', () => {
    const onCancelQueuedShell = vi.fn();
    render(<Composer isRunning queuedShellCommand="git status" onCancelQueuedShell={onCancelQueuedShell} />);

    expect(screen.getByRole('list', { name: 'Queued follow-up messages' })).toHaveTextContent('!git status');
    expect(screen.queryByTestId('shell-queued')).not.toBeInTheDocument();
    fireEvent.click(screen.getByLabelText('Cancel queued shell command'));
    expect(onCancelQueuedShell).toHaveBeenCalledOnce();
  });
});

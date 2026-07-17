// @vitest-environment jsdom
import { act, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { Composer } from './Composer';

afterEach(() => vi.useRealTimers());

describe('Composer queue', () => {
  it('renders queued shell commands in the follow-up queue', () => {
    const onCancelQueuedShell = vi.fn();
    render(<Composer isRunning queuedShellCommand="git status" onCancelQueuedShell={onCancelQueuedShell} />);

    expect(screen.getByRole('list', { name: 'Queued follow-up messages' })).toHaveTextContent('!git status');
    expect(screen.queryByTestId('shell-queued')).not.toBeInTheDocument();
    fireEvent.click(screen.getByLabelText('Cancel queued shell command'));
    expect(onCancelQueuedShell).toHaveBeenCalledOnce();
  });

  it('shows active duration and hides the shortcut hint while running', () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date('2026-01-01T00:00:00Z'));
    const { rerender } = render(
      <Composer isRunning={false} contextTokens={12_000} activeDurationMs={90_000} />,
    );

    expect(screen.getByText('1m 30s')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /for shortcuts/i })).toBeInTheDocument();

    rerender(<Composer isRunning contextTokens={12_000} activeDurationMs={90_000} />);

    expect(screen.getByText('1m 30s')).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /for shortcuts/i })).not.toBeInTheDocument();

    act(() => vi.advanceTimersByTime(1_000));

    expect(screen.getByText('1m 31s')).toBeInTheDocument();
  });
});

describe('Composer input', () => {
  it('does not rewrite its height while typing', () => {
    render(<Composer isRunning={false} />);
    const input = screen.getByRole('textbox');

    fireEvent.input(input, { target: { value: 'hello' } });

    expect((input as HTMLTextAreaElement).style.height).toBe('');
  });
});

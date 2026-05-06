// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from 'vitest';
import { act, fireEvent, render, screen } from '@testing-library/react';
import { SseStatusIndicator } from './SseStatusIndicator';

afterEach(() => {
  vi.useRealTimers();
});

describe('SseStatusIndicator', () => {
  it('renders nothing when SSE is healthy', () => {
    const { container } = render(
      <SseStatusIndicator
        active={true}
        reconnecting={false}
        attempt={0}
        nextRetryAt={null}
        onRetryNow={() => {}}
      />,
    );
    expect(container.innerHTML).toBe('');
  });

  it('shows the polling-fallback notice when never connected', () => {
    render(
      <SseStatusIndicator
        active={false}
        reconnecting={false}
        attempt={0}
        nextRetryAt={null}
        onRetryNow={() => {}}
      />,
    );
    expect(
      screen.getByText(/live updates unavailable/i),
    ).toBeInTheDocument();
  });

  it('shows attempt count and live countdown while reconnecting', () => {
    vi.useFakeTimers({ shouldAdvanceTime: false });
    vi.setSystemTime(new Date('2025-01-01T00:00:00Z'));

    render(
      <SseStatusIndicator
        active={false}
        reconnecting={true}
        attempt={3}
        nextRetryAt={Date.now() + 4000}
        onRetryNow={() => {}}
      />,
    );

    // Attempt count surfaces so users can see how long we've been
    // disconnected.
    expect(screen.getByText(/attempt 3/i)).toBeInTheDocument();
    // The countdown rounds *up* to whole seconds so it never reads "0s"
    // before the retry actually fires.
    expect(screen.getByText(/retrying in 4s/i)).toBeInTheDocument();

    // Tick one second; countdown should drop.
    act(() => {
      vi.advanceTimersByTime(1000);
    });
    expect(screen.getByText(/retrying in 3s/i)).toBeInTheDocument();
  });

  it('omits the countdown once the retry instant has passed', () => {
    vi.useFakeTimers({ shouldAdvanceTime: false });
    vi.setSystemTime(new Date('2025-01-01T00:00:00Z'));

    render(
      <SseStatusIndicator
        active={false}
        reconnecting={true}
        attempt={1}
        nextRetryAt={Date.now() - 100}
        onRetryNow={() => {}}
      />,
    );
    // No countdown text when we're already past the retry instant —
    // the retry is in flight, the message just says we're still
    // trying.
    expect(screen.queryByText(/retrying in/i)).not.toBeInTheDocument();
    expect(screen.getByText(/reconnecting/i)).toBeInTheDocument();
  });

  it('invokes onRetryNow when the retry-now button is clicked', () => {
    const onRetryNow = vi.fn();
    render(
      <SseStatusIndicator
        active={false}
        reconnecting={true}
        attempt={2}
        nextRetryAt={Date.now() + 30_000}
        onRetryNow={onRetryNow}
      />,
    );
    fireEvent.click(screen.getByRole('button', { name: /retry now/i }));
    expect(onRetryNow).toHaveBeenCalledTimes(1);
  });
});

// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { render, screen, act } from '@testing-library/react';
import { LaunchProgressOverlay } from './LaunchProgressOverlay';
import { useLaunchProgressStore } from '../lib/launchProgressStore';

function resetStore() {
  useLaunchProgressStore.setState({
    phase: 'idle',
    directory: '',
    step: 'launch',
    attempt: 0,
    maxAttempts: 0,
    skipLaunch: false,
    error: null,
  });
}

describe('LaunchProgressOverlay', () => {
  beforeEach(() => {
    resetStore();
  });
  afterEach(() => {
    vi.useRealTimers();
    resetStore();
  });

  it('renders nothing while idle', () => {
    const { container } = render(<LaunchProgressOverlay />);
    expect(container.innerHTML).toBe('');
  });

  it('shows the project name and all steps while running', () => {
    render(<LaunchProgressOverlay />);
    act(() => {
      useLaunchProgressStore.getState().begin('/home/u/src/myproject');
    });

    expect(screen.getByText(/Starting session in myproject/)).toBeInTheDocument();
    expect(screen.getByTestId('launch-step-launch')).toHaveClass('active');
    expect(screen.getByTestId('launch-step-wait')).toHaveClass('pending');
    expect(screen.getByTestId('launch-step-create')).toHaveClass('pending');
  });

  it('marks earlier steps done and shows the attempt counter', () => {
    render(<LaunchProgressOverlay />);
    act(() => {
      const s = useLaunchProgressStore.getState();
      s.begin('/tmp/foo');
      s.setStep('wait');
      s.setAttempt(2, 5);
    });

    expect(screen.getByTestId('launch-step-launch')).toHaveClass('done');
    expect(screen.getByTestId('launch-step-wait')).toHaveClass('active');
    expect(screen.getByText(/attempt 2\/5/)).toBeInTheDocument();
  });

  it('hides the launch step when opencode was launched externally', () => {
    render(<LaunchProgressOverlay />);
    act(() => {
      useLaunchProgressStore.getState().begin('/tmp/foo', { skipLaunch: true });
    });

    expect(screen.queryByTestId('launch-step-launch')).not.toBeInTheDocument();
    expect(screen.getByTestId('launch-step-wait')).toHaveClass('active');
  });

  it('shows the error message and marks the failing step', () => {
    render(<LaunchProgressOverlay />);
    act(() => {
      const s = useLaunchProgressStore.getState();
      s.begin('/tmp/foo');
      s.setStep('wait');
      s.fail('OpenCode did not start in time.');
    });

    expect(screen.getByText('Failed to start session')).toBeInTheDocument();
    expect(screen.getByText('OpenCode did not start in time.')).toBeInTheDocument();
    expect(screen.getByTestId('launch-step-wait')).toHaveClass('error');
  });

  it('auto-dismisses shortly after success', () => {
    vi.useFakeTimers({ shouldAdvanceTime: false });
    render(<LaunchProgressOverlay />);
    act(() => {
      const s = useLaunchProgressStore.getState();
      s.begin('/tmp/foo');
      s.succeed();
    });

    expect(screen.getByText('Session ready')).toBeInTheDocument();
    act(() => {
      vi.advanceTimersByTime(2000);
    });
    expect(screen.queryByTestId('launch-progress-overlay')).not.toBeInTheDocument();
    expect(useLaunchProgressStore.getState().phase).toBe('idle');
  });

  it('can be dismissed manually', () => {
    render(<LaunchProgressOverlay />);
    act(() => {
      useLaunchProgressStore.getState().begin('/tmp/foo');
    });

    act(() => {
      screen.getByLabelText('Dismiss launch progress').click();
    });
    expect(screen.queryByTestId('launch-progress-overlay')).not.toBeInTheDocument();
  });
});

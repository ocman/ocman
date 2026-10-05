// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { render, screen, act } from '@testing-library/react';
import { LaunchProgressCard } from './LaunchProgressCard';
import { LAUNCH_QUICK_MS, useLaunchProgressStore } from '../lib/launchProgressStore';

const DIR = '/home/u/src/myproject';

function resetStore() {
  useLaunchProgressStore.setState({
    phase: 'idle',
    directory: '',
    remoteId: 'local',
    step: 'launch',
    attempt: 0,
    maxAttempts: 0,
    skipLaunch: false,
    error: null,
  });
}

describe('LaunchProgressCard', () => {
  beforeEach(() => {
    resetStore();
  });
  afterEach(() => {
    vi.useRealTimers();
    resetStore();
  });

  it('renders nothing while idle', () => {
    const { container } = render(<LaunchProgressCard directory={DIR} />);
    expect(container.innerHTML).toBe('');
  });

  it('shows the project name and all steps while running', () => {
    render(<LaunchProgressCard directory={DIR} />);
    act(() => {
      useLaunchProgressStore.getState().begin(DIR);
    });

    expect(screen.getByText(/Starting OpenCode in myproject/)).toBeInTheDocument();
    expect(screen.getByTestId('launch-step-launch')).toHaveClass('active');
    expect(screen.getByTestId('launch-step-wait')).toHaveClass('pending');
  });

  it('marks earlier steps done and shows the attempt counter', () => {
    render(<LaunchProgressCard directory={DIR} />);
    act(() => {
      const s = useLaunchProgressStore.getState();
      s.begin(DIR);
      s.setStep('wait');
      s.setAttempt(2, 5);
    });

    expect(screen.getByTestId('launch-step-launch')).toHaveClass('done');
    expect(screen.getByTestId('launch-step-wait')).toHaveClass('active');
    expect(screen.getByText(/attempt 2\/5/)).toBeInTheDocument();
  });

  it('hides the launch step when opencode was launched externally', () => {
    render(<LaunchProgressCard directory={DIR} />);
    act(() => {
      useLaunchProgressStore.getState().begin(DIR, { skipLaunch: true });
    });

    expect(screen.queryByTestId('launch-step-launch')).not.toBeInTheDocument();
    expect(screen.getByTestId('launch-step-wait')).toHaveClass('active');
  });

  it('shows the error message and marks the failing step', () => {
    render(<LaunchProgressCard directory={DIR} />);
    act(() => {
      const s = useLaunchProgressStore.getState();
      s.begin(DIR);
      s.setStep('wait');
      s.fail('OpenCode did not start in time.');
    });

    expect(screen.getByText('Failed to start OpenCode')).toBeInTheDocument();
    expect(screen.getByText('OpenCode did not start in time.')).toBeInTheDocument();
    expect(screen.getByTestId('launch-step-wait')).toHaveClass('error');
  });

  it('skips the success card when the flow finishes quickly', () => {
    vi.useFakeTimers({ shouldAdvanceTime: false });
    render(<LaunchProgressCard directory={DIR} />);
    act(() => {
      const s = useLaunchProgressStore.getState();
      s.begin(DIR);
      vi.advanceTimersByTime(LAUNCH_QUICK_MS - 1);
      s.succeed();
    });

    expect(screen.queryByTestId('launch-progress')).not.toBeInTheDocument();
    expect(useLaunchProgressStore.getState().phase).toBe('idle');
  });

  it('auto-dismisses shortly after success', () => {
    vi.useFakeTimers({ shouldAdvanceTime: false });
    render(<LaunchProgressCard directory={DIR} />);
    act(() => {
      const s = useLaunchProgressStore.getState();
      s.begin(DIR);
      vi.advanceTimersByTime(LAUNCH_QUICK_MS);
      s.succeed();
    });

    expect(screen.getByText('OpenCode ready')).toBeInTheDocument();
    act(() => {
      vi.advanceTimersByTime(2000);
    });
    expect(screen.queryByTestId('launch-progress')).not.toBeInTheDocument();
    expect(useLaunchProgressStore.getState().phase).toBe('idle');
  });

  it('can be dismissed manually', () => {
    render(<LaunchProgressCard directory={DIR} />);
    act(() => {
      useLaunchProgressStore.getState().begin(DIR);
    });

    act(() => {
      screen.getByLabelText('Dismiss launch progress').click();
    });
    expect(screen.queryByTestId('launch-progress')).not.toBeInTheDocument();
  });

  it('renders only in the conversation for the launching directory', () => {
    render(<LaunchProgressCard directory="/somewhere/else" />);
    act(() => {
      useLaunchProgressStore.getState().begin(DIR);
    });
    expect(screen.queryByTestId('launch-progress')).not.toBeInTheDocument();
  });

  it('does not show a launch on another machine at the same path', () => {
    render(<LaunchProgressCard directory={DIR} remoteId="local" />);
    act(() => {
      useLaunchProgressStore.getState().begin(DIR, { remoteId: 'r1' });
    });
    expect(screen.queryByTestId('launch-progress')).not.toBeInTheDocument();
  });

  it('shows a launch for the same machine and path', () => {
    render(<LaunchProgressCard directory={DIR} remoteId="r1" />);
    act(() => {
      useLaunchProgressStore.getState().begin(DIR, { remoteId: 'r1' });
    });
    expect(screen.getByTestId('launch-progress')).toBeInTheDocument();
  });
});

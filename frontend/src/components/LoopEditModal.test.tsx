// @vitest-environment jsdom
import { describe, expect, it, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent, waitFor } from '@testing-library/react';
import type { Loop } from '../lib/api.types';
import { LoopEditModal } from './LoopEditModal';

function makeLoop(overrides: Partial<Loop> = {}): Loop {
  return {
    id: 'loop_1',
    platform: 'opencode',
    rootSessionID: 's1',
    directory: '/src/ocman',
    projectName: 'ocman',
    title: 'Check PRs',
    currentTask: '',
    pattern: '',
    triggerType: 'schedule',
    actionType: 'prompt_root',
    actionTemplate: 'old prompt',
    model: 'anthropic/claude-haiku-4-5',
    sessionMode: 'fresh',
    state: 'active',
    iteration: 0,
    errorStreak: 0,
    tokensUsed: 0,
    costUSD: 0,
    lastFiredAt: 0,
    createdAt: 0,
    updatedAt: 0,
    completedAt: 0,
    lastSummary: '',
    triggerConfig: { interval_seconds: 1800 },
    stopConditions: { max_iterations: 48, max_cost_usd: 5 },
    ...overrides,
  };
}

let onSave: ReturnType<typeof vi.fn> & ((req: unknown) => Promise<void>);
let onClose: ReturnType<typeof vi.fn> & (() => void);

beforeEach(() => {
  onSave = vi.fn().mockResolvedValue(undefined) as typeof onSave;
  onClose = vi.fn() as typeof onClose;
});

describe('LoopEditModal', () => {
  it('prefills fields from the loop', () => {
    render(<LoopEditModal loop={makeLoop()} onSave={onSave} onClose={onClose} />);
    expect(screen.getByTestId('loop-edit-form')).toBeInTheDocument();
    expect(screen.getByLabelText('Title')).toHaveValue('Check PRs');
    expect(screen.getByLabelText('Max cost (USD)')).toHaveValue(5);
    expect(screen.getByLabelText('Model (optional)')).toHaveValue('anthropic/claude-haiku-4-5');
    // 1800s is shown as a Go-style duration.
    expect(screen.getByLabelText('Interval')).toHaveValue('30m');
  });

  it('saves edited fields (interval parsed from Go duration) then closes', async () => {
    render(<LoopEditModal loop={makeLoop()} onSave={onSave} onClose={onClose} />);
    fireEvent.change(screen.getByLabelText('Session per iteration'), { target: { value: 'reuse' } });
    fireEvent.change(screen.getByLabelText('Model (optional)'), { target: { value: 'openai/gpt-5.5' } });
    fireEvent.change(screen.getByLabelText('Max iterations'), { target: { value: '10' } });
    fireEvent.change(screen.getByLabelText('Interval'), { target: { value: '1h30m' } });
    fireEvent.click(screen.getByRole('button', { name: 'Save' }));

    await waitFor(() => expect(onSave).toHaveBeenCalled());
    const req = onSave.mock.calls[0][0];
    expect(req.session_mode).toBe('reuse');
    expect(req.model).toBe('openai/gpt-5.5');
    expect(req.stop_conditions.max_iterations).toBe(10);
    expect(req.trigger_config.interval_seconds).toBe(5400);
    await waitFor(() => expect(onClose).toHaveBeenCalled());
  });

  it('rejects an unparseable interval', () => {
    render(<LoopEditModal loop={makeLoop()} onSave={onSave} onClose={onClose} />);
    fireEvent.change(screen.getByLabelText('Interval'), { target: { value: 'soon' } });
    fireEvent.click(screen.getByRole('button', { name: 'Save' }));
    expect(onSave).not.toHaveBeenCalled();
    expect(screen.getByText(/duration like/i)).toBeInTheDocument();
  });

  it('blocks save when the budget is removed', () => {
    render(<LoopEditModal loop={makeLoop()} onSave={onSave} onClose={onClose} />);
    fireEvent.change(screen.getByLabelText('Max cost (USD)'), { target: { value: '' } });
    fireEvent.click(screen.getByRole('button', { name: 'Save' }));
    expect(onSave).not.toHaveBeenCalled();
    expect(screen.getByText(/budget is required/i)).toBeInTheDocument();
  });

  it('hides the interval field for non-schedule loops', () => {
    render(<LoopEditModal loop={makeLoop({ triggerType: 'pr_event' })} onSave={onSave} onClose={onClose} />);
    expect(screen.queryByLabelText('Interval')).not.toBeInTheDocument();
  });

  it('closes on backdrop click and Escape', () => {
    render(<LoopEditModal loop={makeLoop()} onSave={onSave} onClose={onClose} />);
    fireEvent.click(screen.getByTestId('loop-edit-backdrop'));
    expect(onClose).toHaveBeenCalledTimes(1);
    fireEvent.keyDown(window, { key: 'Escape' });
    expect(onClose).toHaveBeenCalledTimes(2);
  });

  it('does not close when clicking inside the dialog', () => {
    render(<LoopEditModal loop={makeLoop()} onSave={onSave} onClose={onClose} />);
    fireEvent.click(screen.getByRole('dialog'));
    expect(onClose).not.toHaveBeenCalled();
  });

  it('hosts lifecycle controls: Pause + Trigger now for an active schedule loop', () => {
    const onPause = vi.fn().mockResolvedValue(undefined);
    const onTrigger = vi.fn().mockResolvedValue(undefined);
    render(
      <LoopEditModal
        loop={makeLoop({ state: 'active', triggerType: 'schedule' })}
        onSave={onSave} onClose={onClose} onPause={onPause} onTrigger={onTrigger}
      />,
    );
    fireEvent.click(screen.getByRole('button', { name: 'Pause' }));
    expect(onPause).toHaveBeenCalled();
    fireEvent.click(screen.getByRole('button', { name: 'Trigger now' }));
    expect(onTrigger).toHaveBeenCalled();
    // No Stop control exists anymore.
    expect(screen.queryByRole('button', { name: 'Stop' })).not.toBeInTheDocument();
  });

  it('shows Resume (not Pause) for a paused loop', () => {
    const onResume = vi.fn().mockResolvedValue(undefined);
    render(
      <LoopEditModal loop={makeLoop({ state: 'paused' })} onSave={onSave} onClose={onClose} onResume={onResume} />,
    );
    fireEvent.click(screen.getByRole('button', { name: 'Resume' }));
    expect(onResume).toHaveBeenCalled();
    expect(screen.queryByRole('button', { name: 'Pause' })).not.toBeInTheDocument();
  });

  it('deletes only after confirmation, then closes', async () => {
    const onDelete = vi.fn().mockResolvedValue(undefined);
    render(<LoopEditModal loop={makeLoop()} onSave={onSave} onClose={onClose} onDelete={onDelete} />);
    fireEvent.click(screen.getByRole('button', { name: 'Delete' }));
    // First click only reveals the confirm; nothing deleted yet.
    expect(onDelete).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole('button', { name: 'Confirm' }));
    await waitFor(() => expect(onDelete).toHaveBeenCalled());
    expect(onClose).toHaveBeenCalled();
  });
});

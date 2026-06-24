// @vitest-environment jsdom
import { describe, expect, it, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/react';
import type { Loop } from '../lib/api.types';
import { LoopHistoryModal } from './LoopHistoryModal';

// The view fetches + needs the router; stub it so this test covers only
// the modal shell (LoopHistoryView has its own test).
vi.mock('./LoopHistoryView', () => ({
  LoopHistoryView: ({ loop }: { loop: Loop }) => <div data-testid="history-view">{loop.id}</div>,
}));

function makeLoop(overrides: Partial<Loop> = {}): Loop {
  return {
    id: 'loop_1', platform: 'opencode', rootSessionID: 's1', directory: '', projectName: '',
    title: 'Watch PRs', currentTask: '', pattern: '', triggerType: 'schedule',
    actionType: 'prompt_root', actionTemplate: '', sessionMode: 'fresh', state: 'active',
    iteration: 0, errorStreak: 0, tokensUsed: 0, costUSD: 0, lastFiredAt: 0, createdAt: 0,
    updatedAt: 0, completedAt: 0, lastSummary: '', triggerConfig: {}, stopConditions: { max_iterations: 5 },
    ...overrides,
  };
}

let onClose: ReturnType<typeof vi.fn> & (() => void);
beforeEach(() => { onClose = vi.fn() as typeof onClose; });

describe('LoopHistoryModal', () => {
  it('renders the title and the history view', () => {
    render(<LoopHistoryModal loop={makeLoop()} onClose={onClose} />);
    expect(screen.getByText('Watch PRs')).toBeInTheDocument();
    expect(screen.getByTestId('history-view')).toHaveTextContent('loop_1');
  });

  it('closes on backdrop click, Close button, and Escape', () => {
    render(<LoopHistoryModal loop={makeLoop()} onClose={onClose} />);
    fireEvent.click(screen.getByTestId('loop-history-backdrop'));
    expect(onClose).toHaveBeenCalledTimes(1);
    fireEvent.click(screen.getByRole('button', { name: 'Close' }));
    expect(onClose).toHaveBeenCalledTimes(2);
    fireEvent.keyDown(window, { key: 'Escape' });
    expect(onClose).toHaveBeenCalledTimes(3);
  });

  it('does not close when clicking inside the dialog', () => {
    render(<LoopHistoryModal loop={makeLoop()} onClose={onClose} />);
    fireEvent.click(screen.getByRole('dialog'));
    expect(onClose).not.toHaveBeenCalled();
  });
});

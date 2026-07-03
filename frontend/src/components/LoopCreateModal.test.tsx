// @vitest-environment jsdom
import { describe, expect, it, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent, waitFor } from '@testing-library/react';
import { LoopCreateModal } from './LoopCreateModal';

let onCreate: ReturnType<typeof vi.fn> & ((req: unknown) => Promise<void>);
let onClose: ReturnType<typeof vi.fn> & (() => void);

beforeEach(() => {
  onCreate = vi.fn().mockResolvedValue(undefined) as typeof onCreate;
  onClose = vi.fn() as typeof onClose;
});

function renderModal(extra: Partial<Parameters<typeof LoopCreateModal>[0]> = {}) {
  return render(
    <LoopCreateModal
      rootSessionId="sess1"
      directory="/src/ocman"
      onCreate={onCreate}
      onClose={onClose}
      {...extra}
    />,
  );
}

describe('LoopCreateModal', () => {
  it('creates a schedule/prompt_root loop with parsed interval and budget', async () => {
    renderModal();
    fireEvent.change(screen.getByLabelText('Interval'), { target: { value: '15m' } });
    fireEvent.change(screen.getByLabelText('Model (optional)'), { target: { value: 'anthropic/claude-haiku-4-5' } });
    fireEvent.change(screen.getByLabelText('Max cost (USD)'), { target: { value: '3' } });
    fireEvent.click(screen.getByRole('button', { name: 'Create' }));

    await waitFor(() => expect(onCreate).toHaveBeenCalled());
    const req = onCreate.mock.calls[0][0];
    expect(req.root_session_id).toBe('sess1');
    expect(req.trigger_type).toBe('schedule');
    expect(req.trigger_config.interval_seconds).toBe(900);
    expect(req.action_type).toBe('prompt_root');
    expect(req.model).toBe('anthropic/claude-haiku-4-5');
    expect(req.session_mode).toBe('fresh');
    expect(req.stop_conditions.max_cost_usd).toBe(3);
    expect(onClose).toHaveBeenCalled();
  });

  it('requires a PR number for a pr_event trigger', () => {
    renderModal();
    fireEvent.change(screen.getByLabelText('Trigger'), { target: { value: 'pr_event' } });
    fireEvent.click(screen.getByRole('button', { name: 'Create' }));
    expect(onCreate).not.toHaveBeenCalled();
    expect(screen.getByText(/PR number is required/i)).toBeInTheDocument();
  });

  it('sends pr_number when provided', async () => {
    renderModal();
    fireEvent.change(screen.getByLabelText('Trigger'), { target: { value: 'pr_event' } });
    fireEvent.change(screen.getByLabelText('PR number'), { target: { value: '42' } });
    fireEvent.click(screen.getByRole('button', { name: 'Create' }));
    await waitFor(() => expect(onCreate).toHaveBeenCalled());
    expect(onCreate.mock.calls[0][0].trigger_config.pr_number).toBe(42);
  });

  it('requires a target session id for prompt_child', () => {
    renderModal();
    fireEvent.change(screen.getByLabelText('Action'), { target: { value: 'prompt_child' } });
    fireEvent.click(screen.getByRole('button', { name: 'Create' }));
    expect(onCreate).not.toHaveBeenCalled();
    expect(screen.getByText(/Target session id is required/i)).toBeInTheDocument();
  });

  it('blocks creation without a budget', () => {
    renderModal();
    fireEvent.change(screen.getByLabelText('Max cost (USD)'), { target: { value: '' } });
    fireEvent.click(screen.getByRole('button', { name: 'Create' }));
    expect(onCreate).not.toHaveBeenCalled();
    expect(screen.getByText(/budget is required/i)).toBeInTheDocument();
  });

  it('passes parent_loop_id when creating a sub-loop', async () => {
    renderModal({ parentLoopId: 'loop_parent' });
    expect(screen.getByText('New sub-loop')).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Create' }));
    await waitFor(() => expect(onCreate).toHaveBeenCalled());
    expect(onCreate.mock.calls[0][0].parent_loop_id).toBe('loop_parent');
  });

  it('anchors to a selected project when no root session is given', async () => {
    render(
      <LoopCreateModal
        projectOptions={['/src/ocman', '/src/other']}
        onCreate={onCreate}
        onClose={onClose}
      />,
    );
    // Project selector is shown; turn_complete trigger is hidden.
    const projectSelect = screen.getByTestId('loop-create-project');
    expect(projectSelect).toBeInTheDocument();
    expect(screen.queryByRole('option', { name: /Turn complete/i })).not.toBeInTheDocument();

    fireEvent.change(projectSelect, { target: { value: '/src/other' } });
    fireEvent.click(screen.getByRole('button', { name: 'Create' }));

    await waitFor(() => expect(onCreate).toHaveBeenCalled());
    const req = onCreate.mock.calls[0][0];
    expect(req.root_session_id).toBeUndefined();
    expect(req.directory).toBe('/src/other');
  });

  it('closes on backdrop click and Escape', () => {
    renderModal();
    fireEvent.click(screen.getByTestId('loop-create-backdrop'));
    expect(onClose).toHaveBeenCalledTimes(1);
    fireEvent.keyDown(window, { key: 'Escape' });
    expect(onClose).toHaveBeenCalledTimes(2);
  });
});

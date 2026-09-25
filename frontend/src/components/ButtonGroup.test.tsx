// @vitest-environment jsdom
import { render, screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { expect, it, vi } from 'vitest';
import { Button, ButtonGroup } from './Control';

it.each([false, true])('labels independent actions and preserves native focus and form behaviour with joined=%s', async (joined) => {
  const user = userEvent.setup();
  const run = vi.fn();
  const remove = vi.fn();
  const submit = vi.fn((event) => event.preventDefault());
  render(<form onSubmit={submit}>
    <ButtonGroup label="Routine actions" joined={joined} className="custom-actions" id="routine-actions">
      <Button type="button" onClick={run}>Run</Button>
      <Button type="button" disabled onClick={remove}>Delete</Button>
      <Button type="submit">Save</Button>
    </ButtonGroup>
  </form>);
  const group = screen.getByRole('group', { name: 'Routine actions' });
  expect(group).toHaveClass('oc-button-group', 'custom-actions');
  expect(group.classList.contains('oc-button-group--joined')).toBe(joined);
  expect(group).toHaveAttribute('id', 'routine-actions');
  await user.tab();
  expect(within(group).getByRole('button', { name: 'Run' })).toHaveFocus();
  await user.keyboard('{Enter}');
  expect(run).toHaveBeenCalledOnce();
  expect(submit).not.toHaveBeenCalled();
  await user.tab();
  expect(within(group).getByRole('button', { name: 'Save' })).toHaveFocus();
  await user.keyboard('{Enter}');
  expect(submit).toHaveBeenCalledOnce();
  expect(remove).not.toHaveBeenCalled();
});

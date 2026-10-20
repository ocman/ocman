// @vitest-environment jsdom
import { afterEach, expect, it, vi } from 'vitest';
import { fireEvent, render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { SnoozeDialog } from './SnoozeDialog';

afterEach(() => vi.useRealTimers());

it('focuses the date, previews local time, and cancels without snoozing', async () => {
  const onClose = vi.fn();
  const onSnooze = vi.fn();
  render(<SnoozeDialog busy={false} error="" onClose={onClose} onSnooze={onSnooze} />);
  expect(screen.getByLabelText('Wake-up date')).toHaveFocus();
  expect(screen.getByRole('status')).toHaveTextContent('local timezone');
  fireEvent.change(screen.getByLabelText('Wake-up date'), { target: { value: '2099-01-02' } });
  expect(screen.getByRole('status')).toHaveTextContent('Wakes');
  await userEvent.click(screen.getByRole('button', { name: 'Cancel' }));
  expect(onClose).toHaveBeenCalledOnce();
  expect(onSnooze).not.toHaveBeenCalled();
});

it('blocks a time that expired while the dialog was open', () => {
  vi.useFakeTimers();
  vi.setSystemTime(new Date('2099-01-02T07:59'));
  const onSnooze = vi.fn();
  render(<SnoozeDialog busy={false} error="" onClose={vi.fn()} onSnooze={onSnooze} />);
  fireEvent.change(screen.getByLabelText('Wake-up date'), { target: { value: '2099-01-02' } });
  const submit = screen.getByRole('button', { name: 'Snooze' });
  expect(submit).toBeEnabled();
  vi.setSystemTime(new Date('2099-01-02T08:01'));
  fireEvent.submit(submit.closest('form')!);
  expect(onSnooze).not.toHaveBeenCalled();
  expect(submit).toBeDisabled();
  expect(screen.getByRole('status')).toHaveTextContent('in the future');
});

it('disables inputs and dismissal while a request is pending', async () => {
  const onClose = vi.fn();
  const onSnooze = vi.fn();
  const { rerender } = render(<SnoozeDialog busy={false} error="" onClose={onClose} onSnooze={onSnooze} />);
  fireEvent.change(screen.getByLabelText('Wake-up date'), { target: { value: '2099-01-02' } });
  rerender(<SnoozeDialog busy error="" onClose={onClose} onSnooze={onSnooze} />);
  expect(screen.getByLabelText('Wake-up date')).toBeDisabled();
  expect(screen.getByLabelText('Wake-up time')).toBeDisabled();
  const submit = screen.getByRole('button', { name: 'Snoozing...' });
  expect(submit).toBeDisabled();
  fireEvent.submit(submit.closest('form')!);
  await userEvent.keyboard('{Escape}');
  expect(onSnooze).not.toHaveBeenCalled();
  expect(onClose).not.toHaveBeenCalled();
});

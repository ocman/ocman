// @vitest-environment jsdom
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import { BackgroundTasksNotice } from './BackgroundTasksNotice';
import { api } from '../../lib/api';

afterEach(() => { vi.restoreAllMocks(); vi.useRealTimers(); });

const tasks = [{ id: 'one', description: 'pnpm test' }, { id: 'two', description: 'go test' }];

it('shows a quiet count only while tasks are running', () => {
  const { rerender } = render(<BackgroundTasksNotice count={0} />);
  expect(screen.queryByRole('status')).not.toBeInTheDocument();
  rerender(<BackgroundTasksNotice count={1} />);
  expect(screen.getByRole('status')).toHaveTextContent('1 background task running');
  rerender(<BackgroundTasksNotice count={3} />);
  expect(screen.getByRole('status')).toHaveTextContent('3 background tasks running');
  rerender(<BackgroundTasksNotice count={0} />);
  expect(screen.queryByRole('status')).not.toBeInTheDocument();
});

it('opens on hover and stays open while crossing into the task list', () => {
  vi.useFakeTimers();
  render(<BackgroundTasksNotice count={2} tasks={tasks} />);
  const trigger = screen.getByRole('button', { name: '2 background tasks running' });
  fireEvent.mouseEnter(trigger);
  act(() => { vi.advanceTimersByTime(1000); });
  expect(screen.getByRole('dialog', { name: 'Background tasks' })).toBeInTheDocument();
  expect(screen.getByText('pnpm test')).toBeInTheDocument();
  fireEvent.mouseLeave(trigger);
  fireEvent.mouseEnter(screen.getByText('Background tasks').parentElement!);
  act(() => { vi.advanceTimersByTime(200); });
  expect(screen.getByRole('dialog')).toBeInTheDocument();
  fireEvent.mouseLeave(screen.getByText('Background tasks').parentElement!);
  act(() => { vi.advanceTimersByTime(200); });
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
});

it('cancels short hovers, supports click and focus, and clears the list after the last task ends', () => {
  vi.useFakeTimers();
  const { rerender } = render(<BackgroundTasksNotice count={1} />);
  const trigger = screen.getByRole('button');
  fireEvent.mouseEnter(trigger);
  fireEvent.mouseLeave(trigger);
  act(() => { vi.advanceTimersByTime(1000); });
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
  fireEvent.focus(trigger);
  expect(screen.getByText('Task details are unavailable from this backend.')).toBeInTheDocument();
  fireEvent.keyDown(trigger, { key: 'Tab' });
  expect(screen.getByRole('dialog')).toHaveFocus();
  fireEvent.mouseDown(document.body);
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
  fireEvent.click(trigger);
  expect(screen.getByRole('dialog')).toBeInTheDocument();
  rerender(<BackgroundTasksNotice count={0} />);
  rerender(<BackgroundTasksNotice count={1} />);
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
});

it('stops only the selected task with its owner and disables that row while pending', async () => {
  let finish!: () => void;
  const stop = vi.spyOn(api, 'stopBackgroundTask').mockImplementation(() => new Promise(resolve => { finish = resolve; }));
  render(<BackgroundTasksNotice count={2} tasks={tasks} sessionId="s1" platform="r-owner:platform" canStop />);
  fireEvent.click(screen.getByRole('button', { name: '2 background tasks running' }));
  fireEvent.click(screen.getByRole('button', { name: 'Stop pnpm test' }));
  expect(stop).toHaveBeenCalledExactlyOnceWith('s1', 'one', 'r-owner:platform');
  expect(screen.getByRole('button', { name: 'Stop pnpm test' })).toBeDisabled();
  expect(screen.getByRole('button', { name: 'Stop go test' })).toBeEnabled();
  await act(async () => { finish(); });
  expect(screen.getByRole('button', { name: 'Stop pnpm test' })).toBeEnabled();
});

it('keeps tasks visible and reports stop errors for retry', async () => {
  vi.spyOn(api, 'stopBackgroundTask').mockRejectedValue(new Error('Disconnected'));
  render(<BackgroundTasksNotice count={1} tasks={[{ id: 'one', description: '' }]} sessionId="s1" platform="p" canStop />);
  fireEvent.click(screen.getByRole('button', { name: '1 background task running' }));
  fireEvent.click(screen.getByRole('button', { name: 'Stop one' }));
  await waitFor(() => expect(screen.getByRole('alert')).toHaveTextContent('Could not stop one: Disconnected'));
  expect(screen.getByRole('button', { name: 'Stop one' })).toBeEnabled();
});

it('does not offer individual stopping without the capability', () => {
  render(<BackgroundTasksNotice count={2} tasks={tasks} />);
  fireEvent.click(screen.getByRole('button', { name: '2 background tasks running' }));
  expect(screen.getByRole('button', { name: 'Stop pnpm test' })).toBeDisabled();
  expect(screen.getByText('Individual stopping is unavailable from this backend or connection.')).toBeInTheDocument();
});

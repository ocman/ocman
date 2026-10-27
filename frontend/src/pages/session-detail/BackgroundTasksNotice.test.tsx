// @vitest-environment jsdom
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import { BackgroundTasksNotice } from './BackgroundTasksNotice';
import { api } from '../../lib/api';

afterEach(() => { vi.restoreAllMocks(); vi.useRealTimers(); });

const tasks = [{ id: 'one', description: 'pnpm test' }, { id: 'two', description: 'go test' }];

it('identifies background agents without offering shell stop actions', () => {
  render(<BackgroundTasksNotice count={1} tasks={[{ id: 'child', description: 'Review changes', kind: 'agent' }]} canStop />);
  fireEvent.click(screen.getByRole('button', { name: '1 background agent running' }));
  expect(screen.getByText('Review changes')).toBeInTheDocument();
  expect(screen.queryByRole('button', { name: 'Stop Review changes' })).not.toBeInTheDocument();
});

it('shows an icon with the task count only while tasks are running', () => {
  const { rerender } = render(<BackgroundTasksNotice count={0} />);
  expect(screen.queryByRole('status')).not.toBeInTheDocument();
  rerender(<BackgroundTasksNotice count={1} />);
  expect(screen.getByRole('button', { name: '1 background task running' })).toHaveTextContent(/^1$/);
  rerender(<BackgroundTasksNotice count={3} />);
  expect(screen.getByRole('button', { name: '3 background tasks running' })).toHaveTextContent(/^3$/);
  rerender(<BackgroundTasksNotice count={0} />);
  expect(screen.queryByRole('status')).not.toBeInTheDocument();
});

it('opens the list on click only, toggles closed, and closes on outside clicks', () => {
  render(<BackgroundTasksNotice count={2} tasks={tasks} />);
  const trigger = screen.getByRole('button', { name: '2 background tasks running' });
  fireEvent.mouseEnter(trigger);
  fireEvent.focus(trigger);
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
  fireEvent.click(trigger);
  expect(screen.getByRole('dialog', { name: 'Background tasks' })).toBeInTheDocument();
  expect(trigger).toHaveAttribute('aria-expanded', 'true');
  expect(screen.getByText('pnpm test')).toBeInTheDocument();
  fireEvent.click(trigger);
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
  fireEvent.click(trigger);
  fireEvent.mouseDown(document.body);
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
});

it('moves focus into the open list on Tab and resets when the last task ends', () => {
  const { rerender } = render(<BackgroundTasksNotice count={1} />);
  const trigger = screen.getByRole('button');
  fireEvent.click(trigger);
  expect(screen.getByText('Task details are unavailable from this backend.')).toBeInTheDocument();
  fireEvent.keyDown(trigger, { key: 'Tab' });
  expect(screen.getByRole('dialog')).toHaveFocus();
  rerender(<BackgroundTasksNotice count={0} />);
  rerender(<BackgroundTasksNotice count={1} />);
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
});

it('anchors the list to the right edge of the trigger within the viewport', () => {
  render(<BackgroundTasksNotice count={1} />);
  const trigger = screen.getByRole('button');
  vi.spyOn(trigger, 'getBoundingClientRect').mockReturnValue({ left: 900, right: 920, top: 700, bottom: 716, width: 20, height: 16, x: 900, y: 700, toJSON: () => ({}) });
  fireEvent.click(trigger);
  const dialog = screen.getByRole('dialog');
  expect(dialog.style.left).toBe(`${Math.max(12, Math.min(920 - 340, window.innerWidth - 352))}px`);
  expect(dialog.style.bottom).toBe(`${window.innerHeight - 700 + 4}px`);
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

it('loads persisted task types and lets the owner switch between task and service', async () => {
  vi.spyOn(api, 'sessionBackgroundTasks').mockResolvedValue({ count: 1, tasks: [{ ...tasks[0], lifecycle: 'service' }] });
  const classify = vi.spyOn(api, 'setBackgroundLifecycle').mockResolvedValue(undefined);
  render(<BackgroundTasksNotice count={1} tasks={[tasks[0]]} sessionId="s1" platform="r-owner:platform" canClassify />);
  fireEvent.click(screen.getByRole('button', { name: '1 background task running' }));
  await waitFor(() => expect(screen.getByRole('button', { name: 'Mark pnpm test as task' })).toBeEnabled());
  fireEvent.click(screen.getByRole('button', { name: 'Mark pnpm test as task' }));
  await waitFor(() => expect(screen.getByRole('button', { name: 'Mark pnpm test as service' })).toBeEnabled());
  expect(classify).toHaveBeenCalledWith('s1', 'one', 'r-owner:platform', 'task');
  fireEvent.click(screen.getByRole('button', { name: 'Mark pnpm test as service' }));
  await waitFor(() => expect(screen.getByRole('button', { name: 'Mark pnpm test as task' })).toBeEnabled());
  expect(classify).toHaveBeenLastCalledWith('s1', 'one', 'r-owner:platform', 'service');
});

it('reports lifecycle read and mutation failures without changing the task type', async () => {
  const read = vi.spyOn(api, 'sessionBackgroundTasks').mockRejectedValue(new Error('Offline'));
  const { unmount } = render(<BackgroundTasksNotice count={1} tasks={[tasks[0]]} sessionId="s1" platform="p" canClassify />);
  fireEvent.click(screen.getByRole('button', { name: '1 background task running' }));
  await waitFor(() => expect(screen.getByRole('alert')).toHaveTextContent('Could not read task types: Offline'));
  expect(screen.getByRole('button', { name: 'Mark pnpm test as service' })).toBeDisabled();
  unmount();
  read.mockResolvedValue({ count: 1, tasks: [tasks[0]] });
  vi.spyOn(api, 'setBackgroundLifecycle').mockRejectedValue(new Error('Disconnected'));
  render(<BackgroundTasksNotice count={1} tasks={[tasks[0]]} sessionId="s1" platform="p" canClassify />);
  fireEvent.click(screen.getByRole('button', { name: '1 background task running' }));
  await waitFor(() => expect(screen.getByRole('button', { name: 'Mark pnpm test as service' })).toBeEnabled());
  fireEvent.click(screen.getByRole('button', { name: 'Mark pnpm test as service' }));
  await waitFor(() => expect(screen.getByRole('alert')).toHaveTextContent('Could not classify pnpm test: Disconnected'));
  expect(screen.getByRole('button', { name: 'Mark pnpm test as service' })).toBeEnabled();
});

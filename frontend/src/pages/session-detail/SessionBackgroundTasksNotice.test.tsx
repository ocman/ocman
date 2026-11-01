// @vitest-environment jsdom
import { act, fireEvent, screen, waitFor } from '@testing-library/react';
import { beforeEach, expect, it, vi } from 'vitest';
import { renderWithQuery } from '../../test/renderWithQuery';
import { prWorkflowApi, type PRLink } from '../../lib/prWorkflowApi';
import { SessionBackgroundTasksNotice } from './SessionBackgroundTasksNotice';

vi.mock('../../lib/prWorkflowApi', () => ({ prWorkflowApi: { links: vi.fn(), change: vi.fn() } }));
const watch = { watching: true, number: 42, remote: { host: 'forge.example', repo: 'team/repo' },
  pr: { title: 'Fix login' } } as PRLink;
beforeEach(() => { vi.resetAllMocks(); vi.mocked(prWorkflowApi.links).mockResolvedValue({ pullRequests: [watch] }); });

it('lists PR watches alongside native tasks and stops them through the hub', async () => {
  const { rerender } = renderWithQuery(<SessionBackgroundTasksNotice sessionId="s1" platform="r-box:fake" count={1}
    tasks={[{ id: 'shell', description: 'Run tests' }]} />);
  fireEvent.click(await screen.findByRole('button', { name: '2 background tasks running' }));
  expect(screen.getByText('Run tests')).toBeVisible();
  expect(screen.getByText('Watching team/repo#42: Fix login')).toBeVisible();
  expect(screen.queryByRole('button', { name: /Mark Watching/ })).not.toBeInTheDocument();
  vi.mocked(prWorkflowApi.links).mockResolvedValue({ pullRequests: [{ ...watch, watching: false }] });
  fireEvent.click(screen.getByRole('button', { name: 'Stop Watching team/repo#42: Fix login' }));
  await waitFor(() => expect(prWorkflowApi.change).toHaveBeenCalledWith('s1', 'r-box:fake', {
    action: 'unwatch', host: 'forge.example', repo: 'team/repo', number: 42,
  }));
  await screen.findByRole('button', { name: '1 background task running' });
  expect(screen.queryByText('Watching team/repo#42: Fix login')).not.toBeInTheDocument();
  rerender(<SessionBackgroundTasksNotice sessionId="s1" platform="r-box:fake" count={0} />);
  expect(screen.queryByRole('status')).not.toBeInTheDocument();
});

it('shows watch-only work without native background capabilities and keeps failed stops retryable', async () => {
  vi.mocked(prWorkflowApi.change).mockRejectedValue(new Error('Offline'));
  renderWithQuery(<SessionBackgroundTasksNotice sessionId="s1" platform="fake" count={0} />);
  fireEvent.click(await screen.findByRole('button', { name: '1 background task running' }));
  fireEvent.click(screen.getByRole('button', { name: /Stop Watching/ }));
  expect(await screen.findByRole('alert')).toHaveTextContent('Offline');
  expect(screen.getByRole('button', { name: /Stop Watching/ })).toBeEnabled();
});

it('ignores stopped watches and keeps same-number PRs in different repositories distinct', async () => {
  vi.mocked(prWorkflowApi.links).mockResolvedValue({ pullRequests: [
    watch, { ...watch, remote: { ...watch.remote, repo: 'other/repo' } },
    { ...watch, number: 43, watching: false },
  ] });
  renderWithQuery(<SessionBackgroundTasksNotice sessionId="s1" platform="fake" count={0} />);
  fireEvent.click(await screen.findByRole('button', { name: '2 background tasks running' }));
  expect(screen.getByText('Watching team/repo#42: Fix login')).toBeVisible();
  expect(screen.getByText('Watching other/repo#42: Fix login')).toBeVisible();
  expect(screen.queryByText(/#43/)).not.toBeInTheDocument();
});

it('does not carry watch state between session owners', async () => {
  const { rerender } = renderWithQuery(<SessionBackgroundTasksNotice sessionId="s1" platform="fake" count={0} />);
  await screen.findByRole('button', { name: '1 background task running' });
  vi.mocked(prWorkflowApi.links).mockResolvedValue({ pullRequests: [] });
  rerender(<SessionBackgroundTasksNotice sessionId="s1" platform="r-box:fake" count={0} />);
  expect(screen.queryByRole('status')).not.toBeInTheDocument();
  await waitFor(() => expect(prWorkflowApi.links).toHaveBeenLastCalledWith('s1', 'r-box:fake'));
  expect(screen.queryByRole('status')).not.toBeInTheDocument();
});

it('shows an initial watch-list failure even without native tasks and retries the read', async () => {
  vi.mocked(prWorkflowApi.links).mockRejectedValue(new Error('Owner disconnected'));
  renderWithQuery(<SessionBackgroundTasksNotice sessionId="s1" platform="r-box:fake" count={0} />);
  expect(await screen.findByRole('alert')).toHaveTextContent('PR watch status unavailable');
  expect(screen.queryByRole('status')).not.toBeInTheDocument();
  vi.mocked(prWorkflowApi.links).mockResolvedValue({ pullRequests: [watch] });
  fireEvent.click(screen.getByRole('button', { name: 'Retry PR watch status' }));
  await screen.findByRole('button', { name: '1 background task running' });
  expect(screen.queryByRole('alert')).not.toBeInTheDocument();
});

it('warns on refresh failure without hiding watches or native tasks and clears the warning on retry', async () => {
  const { client } = renderWithQuery(<SessionBackgroundTasksNotice sessionId="s1" platform="fake" count={1}
    tasks={[{ id: 'shell', description: 'Run tests' }]} />);
  fireEvent.click(await screen.findByRole('button', { name: '2 background tasks running' }));
  vi.mocked(prWorkflowApi.links).mockRejectedValue(new Error('PR storage unavailable'));
  await act(async () => { await client.refetchQueries({ queryKey: ['session-pr-background-tasks', 'fake', 's1'] }); });
  expect(await screen.findByRole('alert')).toHaveTextContent('PR watch status may be outdated');
  expect(screen.getByText('Watching team/repo#42: Fix login')).toBeVisible();
  expect(screen.getByText('Run tests')).toBeVisible();
  vi.mocked(prWorkflowApi.links).mockResolvedValue({ pullRequests: [] });
  fireEvent.click(screen.getByRole('button', { name: 'Retry PR watch status' }));
  await screen.findByRole('button', { name: '1 background task running' });
  expect(screen.queryByRole('alert')).not.toBeInTheDocument();
  expect(screen.getByText('Run tests')).toBeVisible();
});

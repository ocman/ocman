// @vitest-environment jsdom
import { act, fireEvent, screen, waitFor } from '@testing-library/react';
import { beforeEach, expect, it, vi } from 'vitest';
import { renderWithQuery } from '../../test/renderWithQuery';
import { prWorkflowApi, type PRLink } from '../../lib/prWorkflowApi';
import { SessionBackgroundTasksNotice } from './SessionBackgroundTasksNotice';
import { __handleSessionChangedForTests } from '../../lib/useGlobalEvents';
import { ComposerFooter } from '../../components/assistant/ComposerChrome';
import { StatusBadge } from '../../components/StatusBadge';

vi.mock('../../lib/prWorkflowApi', () => ({ prWorkflowApi: { background: vi.fn(), change: vi.fn() } }));
const watch = { watching: true, number: 42, remote: { host: 'forge.example', repo: 'team/repo' },
  pr: { title: 'Fix login' } } as PRLink;
beforeEach(() => { vi.resetAllMocks(); vi.mocked(prWorkflowApi.background).mockResolvedValue({ pullRequests: [watch] }); });

it('shows the background count in an idle composer and explains the watch in its details', async () => {
  renderWithQuery(<>
    <StatusBadge status="done" compact backgroundTaskCount={1} />
    <ComposerFooter isRunning={false} effectiveAgent="" effectiveModel="" visibleDurationMs={0}
      status={<SessionBackgroundTasksNotice sessionId="s1" platform="fake" count={0} />} />
  </>);
  expect(screen.getByLabelText('Session status: Waiting for 1 background task')).toBeVisible();
  const task = await screen.findByRole('button', { name: '1 background task running' });
  expect(task).toHaveTextContent(/^1$/);
  expect(screen.queryByTestId('running-dots')).not.toBeInTheDocument();
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
  fireEvent.click(task);
  expect(screen.getByText('Watching team/repo#42: Fix login')).toBeVisible();
});

it('updates background watches immediately on live start and stop events', async () => {
  vi.mocked(prWorkflowApi.background).mockResolvedValue({ pullRequests: [] });
  renderWithQuery(<SessionBackgroundTasksNotice sessionId="s1" platform="r-box:fake" count={0} />);
  await act(async () => {});
  vi.mocked(prWorkflowApi.background).mockResolvedValue({ pullRequests: [watch] });
  act(() => __handleSessionChangedForTests(JSON.stringify({ sessionID: 's1', platform: 'r-box:fake' })));
  await screen.findByRole('button', { name: '1 background task running' });
  vi.mocked(prWorkflowApi.background).mockResolvedValue({ pullRequests: [] });
  act(() => __handleSessionChangedForTests(JSON.stringify({ sessionID: 's1', platform: 'r-box:fake' })));
  await waitFor(() => expect(screen.queryByRole('status')).not.toBeInTheDocument());
});

it('counts a queued PR update after watching stops and removes it after delivery', async () => {
  vi.mocked(prWorkflowApi.background).mockResolvedValue({ pullRequests: [{ ...watch, watching: false, backgroundTask: true }] });
  const { client } = renderWithQuery(<SessionBackgroundTasksNotice sessionId="s1" platform="fake" count={2}
    tasks={[{ id: 'one', description: 'Run tests' }, { id: 'two', description: 'Build' }]} />);
  fireEvent.click(await screen.findByRole('button', { name: '3 background tasks running' }));
  expect(screen.getByText('PR update awaiting delivery for team/repo#42: Fix login')).toBeVisible();
  vi.mocked(prWorkflowApi.background).mockResolvedValue({ pullRequests: [{ ...watch, watching: false, backgroundTask: false }] });
  await act(() => client.refetchQueries({ queryKey: ['session-pr-background-tasks', 'fake', 's1'] }));
  expect(await screen.findByRole('button', { name: '2 background tasks running' })).toHaveTextContent(/^2$/);
  expect(screen.queryByText(/PR update awaiting delivery/)).not.toBeInTheDocument();
});

it('counts a watch with queued updates only once and respects the authoritative inactive flag', async () => {
  vi.mocked(prWorkflowApi.background).mockResolvedValue({ pullRequests: [
    { ...watch, backgroundTask: true }, { ...watch, number: 43, backgroundTask: false },
  ] });
  renderWithQuery(<SessionBackgroundTasksNotice sessionId="s1" platform="fake" count={0} />);
  expect(await screen.findByRole('button', { name: '1 background task running' })).toHaveTextContent(/^1$/);
});

it('lists PR watches alongside native tasks and stops them through the hub', async () => {
  const { rerender } = renderWithQuery(<SessionBackgroundTasksNotice sessionId="s1" platform="r-box:fake" count={1}
    tasks={[{ id: 'shell', description: 'Run tests' }]} />);
  const trigger = await screen.findByRole('button', { name: '2 background tasks running' });
  expect(trigger).toHaveTextContent(/^2$/);
  fireEvent.click(trigger);
  expect(screen.getByText('Run tests')).toBeVisible();
  expect(screen.getByText('Watching team/repo#42: Fix login')).toBeVisible();
  expect(screen.queryByRole('button', { name: /Mark Watching/ })).not.toBeInTheDocument();
  vi.mocked(prWorkflowApi.background).mockResolvedValue({ pullRequests: [{ ...watch, watching: false }] });
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
  vi.mocked(prWorkflowApi.background).mockResolvedValue({ pullRequests: [
    watch, { ...watch, remote: { ...watch.remote, repo: 'other/repo' } },
    { ...watch, number: 43, watching: false },
  ] });
  renderWithQuery(<SessionBackgroundTasksNotice sessionId="s1" platform="fake" count={0} />);
  const trigger = await screen.findByRole('button', { name: '2 background tasks running' });
  expect(trigger).toHaveTextContent(/^2$/);
  fireEvent.click(trigger);
  expect(screen.getByText('Watching team/repo#42: Fix login')).toBeVisible();
  expect(screen.getByText('Watching other/repo#42: Fix login')).toBeVisible();
  expect(screen.queryByText(/#43/)).not.toBeInTheDocument();
});

it('does not carry watch state between session owners', async () => {
  const { rerender } = renderWithQuery(<SessionBackgroundTasksNotice sessionId="s1" platform="fake" count={0} />);
  await screen.findByRole('button', { name: '1 background task running' });
  vi.mocked(prWorkflowApi.background).mockResolvedValue({ pullRequests: [] });
  rerender(<SessionBackgroundTasksNotice sessionId="s1" platform="r-box:fake" count={0} />);
  expect(screen.queryByRole('status')).not.toBeInTheDocument();
  await waitFor(() => expect(prWorkflowApi.background).toHaveBeenLastCalledWith('s1', 'r-box:fake', expect.any(AbortSignal)));
  expect(screen.queryByRole('status')).not.toBeInTheDocument();
});

it('shows an initial watch-list failure even without native tasks and retries the read', async () => {
  vi.mocked(prWorkflowApi.background).mockRejectedValue(new Error('Owner disconnected'));
  renderWithQuery(<SessionBackgroundTasksNotice sessionId="s1" platform="r-box:fake" count={0} />);
  expect(await screen.findByRole('alert')).toHaveTextContent('PR watch status unavailable');
  expect(screen.queryByRole('status')).not.toBeInTheDocument();
  vi.mocked(prWorkflowApi.background).mockResolvedValue({ pullRequests: [watch] });
  fireEvent.click(screen.getByRole('button', { name: 'Retry PR watch status' }));
  await screen.findByRole('button', { name: '1 background task running' });
  expect(screen.queryByRole('alert')).not.toBeInTheDocument();
});

it('warns on refresh failure without hiding watches or native tasks and clears the warning on retry', async () => {
  const { client } = renderWithQuery(<SessionBackgroundTasksNotice sessionId="s1" platform="fake" count={1}
    tasks={[{ id: 'shell', description: 'Run tests' }]} />);
  fireEvent.click(await screen.findByRole('button', { name: '2 background tasks running' }));
  vi.mocked(prWorkflowApi.background).mockRejectedValue(new Error('PR storage unavailable'));
  await act(async () => { await client.refetchQueries({ queryKey: ['session-pr-background-tasks', 'fake', 's1'] }); });
  expect(await screen.findByRole('alert')).toHaveTextContent('PR watch status may be outdated');
  expect(screen.getByText('Watching team/repo#42: Fix login')).toBeVisible();
  expect(screen.getByText('Run tests')).toBeVisible();
  vi.mocked(prWorkflowApi.background).mockResolvedValue({ pullRequests: [] });
  fireEvent.click(screen.getByRole('button', { name: 'Retry PR watch status' }));
  await screen.findByRole('button', { name: '1 background task running' });
  expect(screen.queryByRole('alert')).not.toBeInTheDocument();
  expect(screen.getByText('Run tests')).toBeVisible();
});

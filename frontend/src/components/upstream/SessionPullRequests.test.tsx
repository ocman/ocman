// @vitest-environment jsdom
import { act, fireEvent, screen, waitFor } from '@testing-library/react';
import { renderWithQuery as render } from '../../test/renderWithQuery';
import { MemoryRouter } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { ConfirmContext } from '../../lib/useConfirm';
import { prWorkflowApi, type PRLink } from '../../lib/prWorkflowApi';
import { SessionPullRequests } from './SessionPullRequests';

vi.mock('../../lib/prWorkflowApi', () => ({ prWorkflowApi: { links: vi.fn(), change: vi.fn(), feedback: vi.fn() } }));
const confirm = vi.fn();
const upstream = { remote: 'origin', host: 'github.com', type: 'github' as const, repo: 'team/repo' };
const link: PRLink = { platform: 'r-box:opencode', sessionId: 's1', remoteId: 'box', directory: '/repo', remote: upstream, number: 42, watching: false, pr: { number: 42, title: 'Improve authentication', body: '', author: 'dries', status: 'open', url: 'https://github.com/team/repo/pull/42', branch: 'feature', updatedAt: '', host: 'github.com', repo: 'team/repo', labels: [], assignees: [], requestedReviewers: [], crossFork: false } };
function mount() { return render(<MemoryRouter><ConfirmContext.Provider value={confirm}><SessionPullRequests sessionId="s1" platform="r-box:opencode" upstreams={[upstream]} /></ConfirmContext.Provider></MemoryRouter>); }
beforeEach(() => { vi.resetAllMocks(); vi.mocked(prWorkflowApi.links).mockResolvedValue({ pullRequests: [link] }); vi.mocked(prWorkflowApi.change).mockResolvedValue(link); vi.mocked(prWorkflowApi.feedback).mockResolvedValue({ prompt: 'Investigate the failed tests. Do not merge.' }); confirm.mockResolvedValue(true); });

describe('SessionPullRequests', () => {
  it('loads owner-qualified links and confirms a watch', async () => {
    mount(); fireEvent.click(await screen.findByRole('button', { name: 'Watch PR' }));
    await waitFor(() => expect(prWorkflowApi.change).toHaveBeenCalledWith('s1', 'r-box:opencode', { action: 'watch', number: 42, host: 'github.com', repo: 'team/repo' })); expect(confirm).toHaveBeenCalled();
    confirm.mockResolvedValue(false); vi.mocked(prWorkflowApi.change).mockClear(); await waitFor(() => expect(screen.getByRole('button', { name: 'Watch PR' })).toBeEnabled()); fireEvent.click(screen.getByRole('button', { name: 'Watch PR' })); await act(async () => {}); expect(prWorkflowApi.change).not.toHaveBeenCalled();
  });
  it('stops and unlinks without starting new agent work', async () => {
    vi.mocked(prWorkflowApi.links).mockResolvedValue({ pullRequests: [{ ...link, watching: true, lastError: 'Retrying with backoff.' }] });
    mount(); fireEvent.click(await screen.findByRole('button', { name: 'Stop watching' })); await waitFor(() => expect(prWorkflowApi.change).toHaveBeenCalledWith('s1', 'r-box:opencode', expect.objectContaining({ action: 'unwatch' })));
    await waitFor(() => expect(screen.getByRole('button', { name: 'Unlink' })).toBeEnabled()); fireEvent.click(screen.getByRole('button', { name: 'Unlink' })); await waitFor(() => expect(prWorkflowApi.change).toHaveBeenCalledWith('s1', 'r-box:opencode', expect.objectContaining({ action: 'unlink' }))); expect(screen.getByText('Retrying with backoff.')).toBeInTheDocument();
  });
  it('links a PR manually and retains the selected upstream', async () => {
    vi.mocked(prWorkflowApi.links).mockResolvedValue({ pullRequests: [] }); mount(); expect(await screen.findByText(/No linked PRs/)).toBeInTheDocument();
    fireEvent.change(screen.getByLabelText('PR upstream'), { target: { value: 'origin' } }); fireEvent.change(screen.getByLabelText('PR number to link'), { target: { value: '42' } }); fireEvent.click(screen.getByRole('button', { name: 'Link PR' }));
    await waitFor(() => expect(prWorkflowApi.change).toHaveBeenCalledWith('s1', 'r-box:opencode', { action: 'link', number: 42, remote: 'origin' })); await waitFor(() => expect(screen.getByLabelText('PR number to link')).toHaveValue(null));
  });
  it.each([['Address feedback', 'feedback'], ['Fix failing checks', 'checks'], ['Resolve conflicts', 'conflicts']])('prepares %s as an editable task without sending', async (label, action) => {
    mount(); fireEvent.click(await screen.findByRole('button', { name: label }));
    expect(await screen.findByRole('dialog', { name: 'Review feedback task' })).toBeInTheDocument(); expect(prWorkflowApi.feedback).toHaveBeenCalledWith('s1', 'r-box:opencode', link, action); expect(prWorkflowApi.change).not.toHaveBeenCalled();
    fireEvent.change(screen.getByLabelText('PR feedback task'), { target: { value: 'Edited task' } }); expect(screen.getByLabelText('PR feedback task')).toHaveValue('Edited task'); fireEvent.click(screen.getByRole('button', { name: /^Close$/ })); expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
  });
  it('shows read and mutation failures with retry', async () => {
    vi.mocked(prWorkflowApi.links).mockRejectedValueOnce(new Error('offline')).mockResolvedValue({ pullRequests: [link] }); mount(); expect(await screen.findByRole('alert')).toHaveTextContent('Could not load'); fireEvent.click(screen.getByRole('button', { name: 'Retry links' })); await screen.findByRole('button', { name: 'Watch PR' });
    vi.mocked(prWorkflowApi.change).mockRejectedValue(new Error('owner disconnected')); fireEvent.click(screen.getByRole('button', { name: 'Unlink' })); expect(await screen.findByRole('alert')).toHaveTextContent('owner disconnected');
  });
  it('refreshes visible watch status without polling hidden documents', async () => {
    vi.useFakeTimers();
    const visible = vi.spyOn(document, 'visibilityState', 'get').mockReturnValue('hidden');
    try {
      mount(); await act(async () => {});
      vi.mocked(prWorkflowApi.links).mockClear();
      await act(() => vi.advanceTimersByTimeAsync(60_000)); expect(prWorkflowApi.links).not.toHaveBeenCalled();
      visible.mockReturnValue('visible');
      await act(() => vi.advanceTimersByTimeAsync(60_000)); expect(prWorkflowApi.links).toHaveBeenCalledTimes(1);
    } finally { visible.mockRestore(); vi.useRealTimers(); }
  });
  it('uses cached reads on mount and only forces a refresh on request', async () => {
    mount(); await screen.findByRole('button', { name: 'Watch PR' });
    expect(prWorkflowApi.links).toHaveBeenCalledWith('s1', 'r-box:opencode', false);
    vi.mocked(prWorkflowApi.links).mockClear();
    fireEvent.click(screen.getByRole('button', { name: 'Refresh links' }));
    await waitFor(() => expect(prWorkflowApi.links).toHaveBeenCalledWith('s1', 'r-box:opencode', true));
  });
});

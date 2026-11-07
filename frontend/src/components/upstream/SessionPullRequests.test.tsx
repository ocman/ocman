// @vitest-environment jsdom
import { act, fireEvent, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { renderWithQuery as render } from '../../test/renderWithQuery';
import { MemoryRouter } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { ConfirmContext } from '../../lib/useConfirm';
import { prWorkflowApi, type PRLink } from '../../lib/prWorkflowApi';
import { SessionPullRequests } from './SessionPullRequests';
import { __handleSessionChangedForTests } from '../../lib/useGlobalEvents';

vi.mock('../../lib/prWorkflowApi', () => ({ prWorkflowApi: { links: vi.fn(), change: vi.fn(), feedback: vi.fn() } }));
const confirm = vi.fn();
const upstream = { remote: 'origin', host: 'github.com', type: 'github' as const, repo: 'team/repo' };
const link: PRLink = { platform: 'r-box:opencode', sessionId: 's1', remoteId: 'box', directory: '/repo', remote: upstream, number: 42, watching: false, pr: { number: 42, title: 'Improve authentication', body: '', author: 'dries', status: 'open', url: 'https://github.com/team/repo/pull/42', branch: 'feature', updatedAt: '', host: 'github.com', repo: 'team/repo', labels: [], assignees: [], requestedReviewers: [], crossFork: false } };
function mount() { return render(<MemoryRouter><ConfirmContext.Provider value={confirm}><SessionPullRequests sessionId="s1" platform="r-box:opencode" /></ConfirmContext.Provider></MemoryRouter>); }
beforeEach(() => { vi.resetAllMocks(); vi.mocked(prWorkflowApi.links).mockResolvedValue({ pullRequests: [link] }); vi.mocked(prWorkflowApi.change).mockResolvedValue(link); vi.mocked(prWorkflowApi.feedback).mockResolvedValue({ prompt: 'Investigate the failed tests. Do not merge.' }); confirm.mockResolvedValue(true); });

describe('SessionPullRequests', () => {
  it('keeps unmerged PRs visible and puts merged PRs in a collapsed drawer', async () => {
    const merged = { ...link, number: 43, pr: { ...link.pr, number: 43, title: 'Already shipped', status: 'merged' as const } };
    const closed = { ...link, number: 44, pr: { ...link.pr, number: 44, title: 'Not merged', status: 'closed' as const } };
    const draft = { ...link, number: 45, pr: { ...link.pr, number: 45, title: 'Work in progress', status: 'draft' as const } };
    vi.mocked(prWorkflowApi.links).mockResolvedValue({ pullRequests: [merged, link, closed, draft] });
    const user = userEvent.setup();
    mount();
    const toggle = await screen.findByRole('button', { name: 'Merged PRs (1)' });
    expect(toggle).toHaveAttribute('aria-expanded', 'false');
    expect(screen.queryByRole('link', { name: '#43 Already shipped' })).not.toBeInTheDocument();
    for (const name of ['#42 Improve authentication', '#44 Not merged', '#45 Work in progress']) {
      expect(screen.getByRole('link', { name })).toBeVisible();
    }
    toggle.focus();
    await user.keyboard('{Enter}');
    expect(toggle).toHaveAttribute('aria-expanded', 'true');
    const drawer = screen.getByRole('region', { name: 'Merged PRs' });
    expect(within(drawer).getByRole('link', { name: '#43 Already shipped' })).toBeVisible();
    expect(within(drawer).getByRole('button', { name: 'Watch PR' })).toBeDisabled();
    await user.click(toggle);
    expect(toggle).toHaveAttribute('aria-expanded', 'false');
    expect(screen.queryByRole('region', { name: 'Merged PRs' })).not.toBeInTheDocument();
  });
  it('moves a PR into the drawer when a live update reports it merged', async () => {
    mount();
    await screen.findByRole('link', { name: '#42 Improve authentication' });
    expect(screen.queryByRole('button', { name: /Merged PRs/ })).not.toBeInTheDocument();
    vi.mocked(prWorkflowApi.links).mockResolvedValue({ pullRequests: [{ ...link, pr: { ...link.pr, status: 'merged' } }] });
    act(() => __handleSessionChangedForTests(JSON.stringify({ sessionID: 's1', platform: link.platform })));
    expect(await screen.findByRole('button', { name: 'Merged PRs (1)' })).toHaveAttribute('aria-expanded', 'false');
    expect(screen.queryByRole('link', { name: '#42 Improve authentication' })).not.toBeInTheDocument();
  });
  it('counts merged links and allows unlinking them from an expanded drawer', async () => {
    const merged = { ...link, pr: { ...link.pr, status: 'merged' as const } };
    vi.mocked(prWorkflowApi.links).mockResolvedValue({ pullRequests: [merged, { ...merged, number: 43, pr: { ...merged.pr, number: 43 } }] });
    mount();
    fireEvent.click(await screen.findByRole('button', { name: 'Merged PRs (2)' }));
    const drawer = screen.getByRole('region', { name: 'Merged PRs' });
    expect(drawer).toHaveAttribute('id', screen.getByRole('button', { name: 'Merged PRs (2)' }).getAttribute('aria-controls'));
    expect(within(drawer).getAllByRole('link')).toHaveLength(2);
    vi.mocked(prWorkflowApi.links).mockResolvedValue({ pullRequests: [] });
    fireEvent.click(within(drawer).getAllByRole('button', { name: 'Unlink' })[0]);
    await waitFor(() => expect(prWorkflowApi.change).toHaveBeenCalledWith('s1', 'r-box:opencode', { action: 'unlink', number: 42, host: 'github.com', repo: 'team/repo' }));
    await waitFor(() => expect(screen.queryByRole('region', { name: 'Linked pull requests' })).not.toBeInTheDocument());
  });
  it('refreshes watch starts and stops from owner-qualified live events', async () => {
    mount();
    await screen.findByRole('button', { name: 'Watch PR' });
    vi.mocked(prWorkflowApi.links).mockClear();
    await act(async () => {
      __handleSessionChangedForTests(JSON.stringify({ sessionID: 's1', platform: 'opencode' }));
      __handleSessionChangedForTests(JSON.stringify({ sessionID: 'other', platform: link.platform }));
    });
    expect(prWorkflowApi.links).not.toHaveBeenCalled();
    vi.mocked(prWorkflowApi.links).mockResolvedValue({ pullRequests: [{ ...link, watching: true }] });
    act(() => __handleSessionChangedForTests(JSON.stringify({ sessionID: 's1', platform: link.platform })));
    await screen.findByRole('button', { name: 'Stop watching' });
    vi.mocked(prWorkflowApi.links).mockResolvedValue({ pullRequests: [link] });
    act(() => __handleSessionChangedForTests(JSON.stringify({ sessionID: 's1', platform: link.platform })));
    await screen.findByRole('button', { name: 'Watch PR' });
  });
  it('loads owner-qualified links and confirms a watch', async () => {
    mount(); fireEvent.click(await screen.findByRole('button', { name: 'Watch PR' }));
    await waitFor(() => expect(prWorkflowApi.change).toHaveBeenCalledWith('s1', 'r-box:opencode', { action: 'watch', number: 42, host: 'github.com', repo: 'team/repo' })); expect(confirm).toHaveBeenCalled();
    confirm.mockResolvedValue(false); vi.mocked(prWorkflowApi.change).mockClear(); await waitFor(() => expect(screen.getByRole('button', { name: 'Watch PR' })).toBeEnabled()); fireEvent.click(screen.getByRole('button', { name: 'Watch PR' })); await act(async () => {}); expect(prWorkflowApi.change).not.toHaveBeenCalled();
  });
  it('stops and unlinks without starting new agent work', async () => {
    vi.mocked(prWorkflowApi.links).mockResolvedValue({ pullRequests: [{ ...link, watching: true, lastError: 'Retrying with backoff.' }] });
    mount();
    const stop = await screen.findByRole('button', { name: 'Stop watching' });
    expect(stop.querySelector('i')).toHaveClass('bi-sign-stop');
    expect(stop).toHaveTextContent('');
    expect(screen.getByRole('button', { name: 'Unlink' }).querySelector('i')).toHaveClass('bi-x-lg');
    expect(stop.closest('.oc-session-pr-repo')).toHaveTextContent('github.com/team/repo');
    fireEvent.click(stop); await waitFor(() => expect(prWorkflowApi.change).toHaveBeenCalledWith('s1', 'r-box:opencode', expect.objectContaining({ action: 'unwatch' })));
    await waitFor(() => expect(screen.getByRole('button', { name: 'Unlink' })).toBeEnabled()); fireEvent.click(screen.getByRole('button', { name: 'Unlink' })); await waitFor(() => expect(prWorkflowApi.change).toHaveBeenCalledWith('s1', 'r-box:opencode', expect.objectContaining({ action: 'unlink' }))); expect(screen.getByText('Retrying with backoff.')).toBeInTheDocument();
  });
  it('hides the entire section when there are no linked PRs', async () => {
    vi.mocked(prWorkflowApi.links).mockResolvedValue({ pullRequests: [] });
    await act(async () => { mount(); });
    expect(screen.queryByRole('region', { name: 'Linked pull requests' })).not.toBeInTheDocument();
    expect(screen.queryByText('Linked to this session')).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Refresh links' })).not.toBeInTheDocument();
    expect(screen.queryByText(/No linked PRs/)).not.toBeInTheDocument();
    expect(screen.queryByLabelText('PR number to link')).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Link PR' })).not.toBeInTheDocument();
  });
  it('reloads linked PRs after the modal links a pull request', async () => {
    vi.mocked(prWorkflowApi.links).mockResolvedValue({ pullRequests: [] });
    const view = (version: number) => <MemoryRouter><ConfirmContext.Provider value={confirm}><SessionPullRequests sessionId="s1" platform="r-box:opencode" linksVersion={version} /></ConfirmContext.Provider></MemoryRouter>;
    const { rerender } = render(view(0));
    await act(async () => {});
    expect(screen.queryByRole('region', { name: 'Linked pull requests' })).not.toBeInTheDocument();
    vi.mocked(prWorkflowApi.links).mockResolvedValue({ pullRequests: [link] });
    rerender(view(1));
    await screen.findByRole('button', { name: 'Watch PR' });
  });
  it('hides the section after unlinking the last PR', async () => {
    mount();
    const unlink = await screen.findByRole('button', { name: 'Unlink' });
    vi.mocked(prWorkflowApi.links).mockResolvedValue({ pullRequests: [] });
    fireEvent.click(unlink);
    await waitFor(() => expect(screen.queryByRole('region', { name: 'Linked pull requests' })).not.toBeInTheDocument());
  });
  it.each([['Address feedback', 'feedback'], ['Fix failing checks', 'checks'], ['Resolve conflicts', 'conflicts']])('prepares %s as an editable task without sending', async (label, action) => {
    const user = userEvent.setup();
    mount();
    const fix = await screen.findByRole('button', { name: 'Fix' });
    expect(fix).toHaveAttribute('aria-expanded', 'false');
    expect(screen.queryByText(label)).not.toBeInTheDocument();
    await user.click(fix);
    expect(screen.getAllByRole('menuitem')).toHaveLength(3);
    await user.click(screen.getByRole('menuitem', { name: label }));
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
    const view = (refreshVersion: number) => <MemoryRouter><ConfirmContext.Provider value={confirm}><SessionPullRequests sessionId="s1" platform="r-box:opencode" refreshVersion={refreshVersion} /></ConfirmContext.Provider></MemoryRouter>;
    const { rerender } = render(view(0)); await screen.findByRole('button', { name: 'Watch PR' });
    expect(screen.queryByText('Linked to this session')).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Refresh links' })).not.toBeInTheDocument();
    expect(prWorkflowApi.links).toHaveBeenCalledWith('s1', 'r-box:opencode', false);
    vi.mocked(prWorkflowApi.links).mockClear();
    vi.mocked(prWorkflowApi.links).mockResolvedValue({ pullRequests: [{ ...link, pr: { ...link.pr, title: 'Updated PR title' } }] });
    rerender(view(1));
    await waitFor(() => expect(prWorkflowApi.links).toHaveBeenCalledWith('s1', 'r-box:opencode', true));
    expect(await screen.findByRole('link', { name: '#42 Updated PR title' })).toBeInTheDocument();
    vi.mocked(prWorkflowApi.links).mockRejectedValueOnce(new Error('offline'));
    rerender(view(2));
    expect(await screen.findByRole('alert')).toHaveTextContent('Could not refresh linked PRs.');
    expect(screen.getByRole('link', { name: '#42 Updated PR title' })).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Retry links' }));
    await waitFor(() => expect(screen.queryByRole('alert')).not.toBeInTheDocument());
  });
});

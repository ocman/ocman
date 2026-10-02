// @vitest-environment jsdom
import { act, render, screen, waitFor } from '@testing-library/react';
import { MemoryRouter, Route, Routes, useLocation } from 'react-router-dom';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { ComposerProps } from './composerTypes';
import { useWorktreeSubmission } from './worktreeSubmission';

const mocks = vi.hoisted(() => ({ session: vi.fn(), info: vi.fn(), worktrees: vi.fn(), create: vi.fn(), send: vi.fn(), seed: vi.fn() }));
vi.mock('../../lib/api', () => ({
  api: { session: mocks.session, sendMessage: mocks.send },
  fetchJSON: (url: string, signal?: AbortSignal) => url.startsWith('/api/worktree/list') ? mocks.worktrees(url, signal) : mocks.info(url, signal),
  postJSON: mocks.create,
}));
vi.mock('../../lib/apiStore', () => ({ useApiStore: { getState: () => ({ seedNewSession: mocks.seed }) } }));
import { WorktreeStart } from './WorktreeStart';

function Location() { return <output>{useLocation().pathname}</output>; }
let composer: ComposerProps;
const originalSend = vi.fn();
function mount(extra: Partial<ComposerProps> = {}) {
  return render(<MemoryRouter>
    <WorktreeStart sessionId="parent" directory="/repo" isRunning={false} newConversation worktreesSupported
      selectedModel="provider/big" selectedAgent="plan" selectedReasoning="high" onSend={originalSend} {...extra}>
      {(props) => { composer = props; return <span>{props.target}</span>; }}
    </WorktreeStart>
    <Location />
  </MemoryRouter>);
}

describe('automatic worktree start', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    useWorktreeSubmission.setState({ entries: {} });
    mocks.session.mockResolvedValue({ session: { id: 'parent', platform: 'r-machine:opencode', remoteId: 'machine' } });
    mocks.info.mockResolvedValue({ '/repo': { branch: 'main' } });
    mocks.worktrees.mockResolvedValue({ worktrees: [{ path: '/repo', branch: 'main', main: true }] });
    mocks.create.mockResolvedValue({ sessionId: 'child', worktreePath: '/worktrees/fix', branch: 'fix-1234' });
    mocks.send.mockResolvedValue(undefined);
  });

  it('creates on the same owner and sends the original prompt and selections', async () => {
    mount();
    expect(composer.disabled).toBe(true);
    await waitFor(() => expect(composer.disabled).toBe(false));
    expect(composer.target).toBe('worktree');
    expect(mocks.info).toHaveBeenCalledWith('/api/git/info?dir=%2Frepo&remoteId=machine', expect.any(AbortSignal));
    const images = [{ url: 'data:image/png;base64,abc', mime: 'image/png' }];
    await act(() => composer.onSend!('Fix login', images, true));
    expect(mocks.create).toHaveBeenCalledWith('/api/worktree/create-and-launch?platform=r-machine%3Aopencode', {
      projectDir: '/repo', autoName: true, prompt: 'Fix login', parentSessionId: 'parent', remoteId: 'machine', discardEmptyParent: true,
    });
    expect(mocks.send).toHaveBeenCalledWith('child', 'Fix login', images, 'provider/big', 'plan', 'high', 'r-machine:opencode', true);
    expect(mocks.seed).toHaveBeenCalledWith('child', '/worktrees/fix', 'r-machine:opencode', 'fix-1234', 'machine');
    expect(screen.getByText('/session/child')).toBeInTheDocument();
    expect(originalSend).not.toHaveBeenCalled();
  });

  it('delivers a plain first prompt with the create request, without a second send', async () => {
    mocks.create.mockResolvedValue({ sessionId: 'child', worktreePath: '/worktrees/fix', branch: 'fix-1234', firstMessageSent: true });
    mount();
    await waitFor(() => expect(composer.disabled).toBe(false));
    const images = [{ url: 'data:image/png;base64,abc', mime: 'image/png' }];
    await act(() => composer.onSend!('Fix login', images));
    expect(mocks.create).toHaveBeenCalledWith(expect.any(String), expect.objectContaining({
      send: { message: 'Fix login', images, model: 'provider/big', agent: 'plan', reasoning: 'high' },
    }));
    expect(mocks.send).not.toHaveBeenCalled();
    expect(screen.getByText('/session/child')).toBeInTheDocument();
    expect(useWorktreeSubmission.getState().entries.child).toBeUndefined();
  });

  it('retains the submission on the child when sending fails and the user retries', async () => {
    mocks.create.mockResolvedValue({ sessionId: 'child', worktreePath: '/worktrees/fix', branch: 'fix-1234', firstMessageSent: false, firstMessageError: 'send failed' });
    const images = [{ url: 'data:image/png;base64,abc', mime: 'image/png' }];
    const view = mount();
    await waitFor(() => expect(composer.disabled).toBe(false));
    await act(() => composer.onSend!('Fix login', images));
    expect(screen.getByText('/session/child')).toBeInTheDocument();
    view.unmount();
    mount({ sessionId: 'child', directory: '/worktrees/fix', selectedModel: 'other/model', selectedAgent: 'build', selectedReasoning: 'low' });
    await waitFor(() => expect(composer.disabled).toBe(false));
    expect(screen.getByRole('alert')).toHaveTextContent('send failed');
    await userEvent.click(screen.getByRole('button', { name: 'Retry' }));
    await waitFor(() => expect(screen.queryByRole('alert')).not.toBeInTheDocument());
    expect(mocks.create).toHaveBeenCalledTimes(1);
    expect(mocks.send).toHaveBeenCalledTimes(1);
    expect(mocks.send).toHaveBeenLastCalledWith('child', 'Fix login', images, 'provider/big', 'plan', 'high', 'r-machine:opencode', undefined);
  });

  it('keeps a creation error visible without sending to the original checkout', async () => {
    mocks.create.mockRejectedValue(new Error('remote disconnected'));
    mount();
    await waitFor(() => expect(composer.disabled).toBe(false));
    await act(async () => { await expect(composer.onSend!('Fix login')).rejects.toThrow('remote disconnected'); });
    expect(screen.getByRole('alert')).toHaveTextContent('remote disconnected');
    expect(mocks.send).not.toHaveBeenCalled();
    expect(originalSend).not.toHaveBeenCalled();
  });

  it('looks up comma-containing paths as a literal directory', async () => {
    mocks.info.mockResolvedValue({ '/repo/foo,bar': { branch: 'main' } });
    mount({ directory: '/repo/foo,bar' });
    await waitFor(() => expect(composer.disabled).toBe(false));
    expect(mocks.info).toHaveBeenCalledWith('/api/git/info?dir=%2Frepo%2Ffoo%2Cbar&remoteId=machine', expect.any(AbortSignal));
    await act(() => composer.onSend!('Fix login'));
    expect(mocks.create).toHaveBeenCalledWith(expect.any(String), expect.objectContaining({ projectDir: '/repo/foo,bar' }));
  });

  it('allows an explicit current-checkout choice', async () => {
    mount();
    await waitFor(() => expect(composer.disabled).toBe(false));
    act(() => composer.onTargetChange!('current'));
    await act(() => composer.onSend!('hello', undefined, false));
    expect(originalSend).toHaveBeenCalledWith('hello', undefined, false);
    expect(mocks.create).not.toHaveBeenCalled();
  });

  it('uses the current directory for a non-repository', async () => {
    mocks.info.mockResolvedValue({ '/repo': { branch: '', ahead: 0, behind: 0, dirty: false } });
    mount();
    await waitFor(() => expect(composer.disabled).toBe(false));
    expect(composer.worktreesSupported).toBe(false);
    await act(() => composer.onSend!('hello'));
    expect(originalSend).toHaveBeenCalledWith('hello', undefined, undefined);
    expect(mocks.create).not.toHaveBeenCalled();
  });

  it('preserves the workspace selected by manual worktree creation, including after reload', async () => {
    mocks.info.mockResolvedValue({ '/repo/feature': { branch: 'feature', ahead: 0, behind: 0, dirty: false } });
    mocks.worktrees.mockResolvedValue({ worktrees: [
      { path: '/repo', branch: 'main', main: true },
      { path: '/repo/feature', branch: 'feature', main: false },
    ] });
    const view = mount({ directory: '/repo/feature' });
    await waitFor(() => expect(composer.disabled).toBe(false));
    expect(composer.target).toBe('current');
    expect(composer.worktreesSupported).toBe(false);
    await act(() => composer.onSend!('Continue feature'));
    expect(originalSend).toHaveBeenCalledWith('Continue feature', undefined, undefined);
    expect(mocks.create).not.toHaveBeenCalled();
    view.unmount();
    mount({ directory: '/repo/feature' });
    await waitFor(() => expect(composer.disabled).toBe(false));
    expect(composer.target).toBe('current');
    expect(mocks.worktrees).toHaveBeenCalledWith('/api/worktree/list?dir=%2Frepo%2Ffeature&remoteId=machine', expect.any(AbortSignal));
  });

  it('leaves an established conversation on its existing send path', async () => {
    mount({ newConversation: false });
    await act(() => composer.onSend!('continue'));
    expect(originalSend).toHaveBeenCalledWith('continue', undefined, undefined);
    expect(mocks.session).not.toHaveBeenCalled();
    expect(mocks.create).not.toHaveBeenCalled();
  });

  it('offers a retry after owner lookup fails instead of silently using the hub', async () => {
    mocks.session.mockRejectedValueOnce(new Error('owner unavailable'));
    mount();
    expect(await screen.findByRole('alert')).toHaveTextContent('owner unavailable');
    expect(composer.disabled).toBe(true);
    await userEvent.click(screen.getByRole('button', { name: 'Retry' }));
    await waitFor(() => expect(composer.disabled).toBe(false));
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
  });

  it('does not create a worktree from stale session props after a route change', async () => {
    render(<MemoryRouter initialEntries={['/session/other']}><Routes>
      <Route path="/session/:id" element={<WorktreeStart sessionId="parent" directory="/repo" isRunning={false} newConversation>
        {(props) => { composer = props; return null; }}
      </WorktreeStart>} />
    </Routes></MemoryRouter>);
    await waitFor(() => expect(composer.disabled).toBe(false));
    await expect(composer.onSend!('Fix login')).rejects.toThrow('Session changed');
    expect(mocks.create).not.toHaveBeenCalled();
    expect(mocks.send).not.toHaveBeenCalled();
  });
});

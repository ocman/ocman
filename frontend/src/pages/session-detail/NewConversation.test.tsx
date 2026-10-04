// @vitest-environment jsdom
import { act, render, screen, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { ComposerProps } from '../../components/assistant/composerTypes';
import { clearDraft, getDraft, saveDraft } from '../../lib/composerDraft';
import { useLaunchProgressStore } from '../../lib/launchProgressStore';
import { listFailedSends, clearFailedSends } from '../../lib/failedSends';
import { useFirstSubmission } from './firstSubmission';
import { HeaderContext } from '../../lib/headerContext';

const mocks = vi.hoisted(() => ({
  prepare: vi.fn(), start: vi.fn(), info: vi.fn(), worktrees: vi.fn(), post: vi.fn(), seed: vi.fn(),
  openWorktreeForm: vi.fn(), addFavorite: vi.fn(), removeFavorite: vi.fn(), caps: { shellExec: true },
}));
vi.mock('../../lib/api', () => ({
  api: { prepareSession: mocks.prepare, startSession: mocks.start, addFavorite: mocks.addFavorite, removeFavorite: mocks.removeFavorite },
  fetchJSON: (url: string, signal?: AbortSignal) => url.startsWith('/api/worktree/list') ? mocks.worktrees(url, signal) : mocks.info(url, signal),
  postJSON: mocks.post,
}));
vi.mock('../../lib/apiStore', () => ({
  useApiStore: (selector: (s: Record<string, unknown>) => unknown) => selector({ seedNewSession: mocks.seed }),
}));
vi.mock('../../lib/uiStore', () => ({
  useUiStore: (selector: (s: Record<string, unknown>) => unknown) => selector({ openWorktreeForm: mocks.openWorktreeForm }),
}));
vi.mock('../../lib/useCapabilities', () => ({
  useOpencodeLaunch: () => true,
  usePlatformCapabilities: () => mocks.caps,
}));
vi.mock('../../lib/remoteLog', () => ({ remoteLog: { error: vi.fn() } }));
let composer: ComposerProps;
vi.mock('../../components/assistant/Composer', () => ({
  Composer: (props: ComposerProps) => { composer = props; return <span>{props.target}:{String(props.disabled)}</span>; },
}));
import { NewConversation } from './NewConversation';

const navigate = vi.fn();
const navigateToSession = vi.fn();
function mount(params = { directory: '/repo', remoteId: 'machine', platform: 'r-machine:opencode' } as Record<string, string | undefined>) {
  return render(<NewConversation params={{ directory: params.directory!, remoteId: params.remoteId, platform: params.platform, title: params.title }}
    whisperAvailable={false} composerRef={null} navigate={navigate} navigateToSession={navigateToSession} />);
}

describe('NewConversation', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    clearDraft('new');
    clearDraft('child');
    clearFailedSends('s2');
    useFirstSubmission.setState({ entries: {} });
    window.localStorage.removeItem('ocman.projectModels.v1');
    mocks.prepare.mockResolvedValue({
      platform: 'r-machine:opencode', liveConnection: true, defaultAgent: 'plan', defaultModel: 'prov/default',
      agents: [{ name: 'build' }, { name: 'plan', model: 'prov/plan-model' }], commands: [{ name: 'review', description: 'Review', source: 'command' }],
      models: { hasProviders: true, models: [{ provider: 'prov', model: 'big' }] },
    });
    mocks.info.mockResolvedValue({ '/repo': { branch: 'main' } });
    mocks.worktrees.mockResolvedValue({ worktrees: [{ path: '/repo', branch: 'main', main: true }] });
    mocks.start.mockResolvedValue({ sessionId: 'child', platform: 'r-machine:opencode', remoteId: 'machine', directory: '/worktrees/fix', firstMessageSent: true, firstMessageError: '' });
  });

  it('refreshes the owner catalog after changing favorites and preserves manual model selection', async () => {
    mount();
    await waitFor(() => expect(composer.agentsLoaded).toBe(true));
    act(() => composer.onModelChange!('prov/manual'));
    expect(composer.selectedModel).toBe('prov/manual');
    await act(async () => { await composer.onToggleFavorite!('prov', 'manual', true); });
    expect(mocks.addFavorite).toHaveBeenCalledWith('r-machine:opencode', 'prov', 'manual');
    await waitFor(() => expect(composer.agentsLoaded).toBe(true));
    expect(composer.selectedModel).toBe('prov/manual');
    await act(async () => { await composer.onToggleFavorite!('prov', 'manual', false); });
    expect(mocks.removeFavorite).toHaveBeenCalledWith('r-machine:opencode', 'prov', 'manual');
    mocks.addFavorite.mockRejectedValueOnce(new Error('offline'));
    const calls = mocks.prepare.mock.calls.length;
    await act(async () => { await composer.onToggleFavorite!('prov', 'manual', true); });
    expect(mocks.prepare).toHaveBeenCalledTimes(calls);
    act(() => composer.onRefreshModels!());
    await waitFor(() => expect(mocks.prepare).toHaveBeenCalledTimes(calls + 1));
  });

  it('publishes the project (not the worktree) to the header', async () => {
    const setInfo = vi.fn();
    const { unmount } = render(
      <HeaderContext.Provider value={{ info: {}, setInfo }}>
        <NewConversation params={{ directory: '/src/.worktrees/repo/feat', remoteId: 'machine' }}
          whisperAvailable={false} composerRef={null} navigate={navigate} navigateToSession={navigateToSession} />
      </HeaderContext.Provider>,
    );
    expect(setInfo).toHaveBeenCalledWith({
      sessionId: 'new', sessionProject: 'src/repo', sessionProjectFull: '/src/.worktrees/repo/feat', sessionRemoteId: 'machine',
    });
    await waitFor(() => expect(composer.agentsLoaded).toBe(true));
    unmount();
    expect(setInfo).toHaveBeenLastCalledWith({});
  });

  it('reports catalog preparation failures and permits a subsequent refresh', async () => {
    mocks.prepare.mockRejectedValueOnce(new Error('catalog offline'));
    mount();
    await waitFor(() => expect(useLaunchProgressStore.getState().error).toBe('catalog offline'));
    expect(composer.agentsLoaded).toBe(false);
    act(() => composer.onRefreshModels!());
    await waitFor(() => expect(composer.agentsLoaded).toBe(true));
  });

  it('keeps a failed custom command as child-keyed retry state', async () => {
    mocks.post.mockRejectedValueOnce(new Error('command offline'));
    mount();
    await waitFor(() => expect(composer.disabled).toBe(false));
    await act(() => composer.onCommand!('review', 'main'));
    await waitFor(() => expect(useFirstSubmission.getState().entries.child).toMatchObject({ text: '/review main', error: 'command offline' }));
    expect(navigateToSession).toHaveBeenCalledWith('child');
  });

  it('prepares the directory on its owner and offers its catalog before any session exists', async () => {
    mount();
    expect(composer.disabled).toBe(true);
    await waitFor(() => expect(composer.disabled).toBe(false));
    expect(mocks.prepare).toHaveBeenCalledWith({ directory: '/repo', remoteId: 'machine', platform: 'r-machine:opencode' }, expect.any(AbortSignal));
    expect(mocks.info).toHaveBeenCalledWith('/api/git/info?dir=%2Frepo&remoteId=machine', expect.any(AbortSignal));
    await waitFor(() => expect(composer.agentsLoaded).toBe(true));
    expect(composer.target).toBe('worktree');
    expect(composer.worktreesSupported).toBe(true);
    expect(composer.activeAgent).toBe('plan');
    expect(composer.selectedModel).toBe('prov/default');
    expect(composer.models).toEqual(['prov/default', 'prov/big']);
    expect(composer.commands).toEqual([{ name: 'review', description: 'Review', source: 'command' }]);
    expect(composer.sessionId).toBeUndefined();
    expect(composer.draftKey).toBe('new');
    expect(mocks.start).not.toHaveBeenCalled();
  });

  it('does not overwrite a deliberate model or agent selection with a delayed catalog', async () => {
    let finish!: (catalog: unknown) => void;
    mocks.prepare.mockReturnValueOnce(new Promise((resolve) => { finish = resolve; }));
    mount();
    act(() => composer.onModelChange!('p/manual'));
    act(() => composer.onAgentChange!('plan'));
    await act(async () => finish({ platform: 'r-machine:opencode', agents: [], commands: [], models: { models: [] }, projectDefaultModel: 'p/configured' }));
    expect(composer.selectedModel).toBe('p/manual');
    expect(composer.selectedAgent).toBe('plan');
  });

  it('creates the session at the target with the first prompt and moves there', async () => {
    mount({ directory: '/repo', remoteId: 'machine', platform: 'r-machine:opencode', title: 'Login' });
    await waitFor(() => expect(composer.disabled).toBe(false));
    await waitFor(() => expect(composer.agentsLoaded).toBe(true));
    act(() => composer.onAgentChange!('plan'));
    expect(composer.selectedModel).toBe('prov/plan-model');
    const images = [{ url: 'data:image/png;base64,abc', mime: 'image/png' }];
    saveDraft('new', 'Fix login');
    await act(() => composer.onSend!('Fix login', images));
    expect(mocks.start).toHaveBeenCalledWith({
      directory: '/repo', remoteId: 'machine', platform: 'r-machine:opencode', title: 'Login', prompt: 'Fix login', worktree: true,
      send: { message: 'Fix login', images, model: 'prov/plan-model', agent: 'plan', reasoning: undefined },
    });
    expect(mocks.seed).toHaveBeenCalledWith('child', '/worktrees/fix', 'r-machine:opencode', 'Login', 'machine');
    expect(navigateToSession).toHaveBeenCalledWith('child');
    // The shared new-conversation draft now belongs to the session.
    expect(getDraft('new')).toBe('');
    expect(getDraft('child')).toBe('');
  });

  it('honours the current-checkout target and preserves an unsent prompt for retry', async () => {
    mocks.start.mockResolvedValue({ sessionId: 's2', platform: 'r-machine:opencode', remoteId: 'machine', directory: '/repo', firstMessageSent: false, firstMessageError: 'boom' });
    mount();
    await waitFor(() => expect(composer.disabled).toBe(false));
    act(() => composer.onTargetChange!('current'));
    act(() => composer.onReasoningChange!('high'));
    await act(() => composer.onSend!('Fix login'));
    expect(mocks.start).toHaveBeenCalledWith(expect.objectContaining({ worktree: false }));
    expect(listFailedSends('s2')).toEqual([expect.objectContaining({ text: 'Fix login', error: 'boom', reasoning: 'high' })]);
    expect(navigateToSession).toHaveBeenCalledWith('s2');
  });

  it('starts in an existing worktree selected from the owner’s list', async () => {
    mocks.worktrees.mockResolvedValue({ worktrees: [
      { path: '/repo', branch: 'main', main: true },
      { path: '/feature', branch: 'feature', main: false },
    ] });
    mount();
    await waitFor(() => expect(composer.disabled).toBe(false));
    expect(composer.worktrees).toEqual([{ path: '/feature', branch: 'feature' }]);
    act(() => composer.onTargetChange!('dir:/feature'));
    await act(() => composer.onSend!('Fix login'));
    expect(mocks.start).toHaveBeenCalledWith(expect.objectContaining({ directory: '/feature', worktree: false }));
  });

  it('cannot start in a worktree from a linked worktree or a non-repository', async () => {
    mocks.prepare.mockResolvedValue({ platform: 'opencode', agents: [], commands: [], models: { models: [] }, liveConnection: true });
    mocks.worktrees.mockResolvedValue({ worktrees: [{ path: '/repo', branch: 'main', main: true }, { path: '/wt/feat', branch: 'feat' }] });
    mount({ directory: '/wt/feat/sub', remoteId: 'local', platform: undefined });
    await waitFor(() => expect(composer.disabled).toBe(false));
    expect(composer.worktreesSupported).toBe(false);
    expect(composer.target).toBe('current');
    await act(() => composer.onSend!('hi'));
    expect(mocks.start).toHaveBeenCalledWith(expect.objectContaining({ worktree: false, platform: 'opencode' }));
  });

  it('runs a platform command on the new session itself, and surfaces start failures', async () => {
    mocks.post.mockResolvedValue(undefined);
    mount();
    await waitFor(() => expect(composer.disabled).toBe(false));
    await act(() => composer.onCommand!('review', 'main'));
    expect(mocks.start).toHaveBeenCalledWith(expect.objectContaining({ prompt: '/review main', send: undefined }));
    await waitFor(() => expect(mocks.post).toHaveBeenCalledWith('/api/session/child/command?platform=r-machine%3Aopencode',
      expect.objectContaining({ command: 'review', arguments: 'main' }), { parseJSON: false }));
    await act(() => composer.onShell!('ls'));
    expect(mocks.post).toHaveBeenCalledWith('/api/session/child/shell?platform=r-machine%3Aopencode', { command: 'ls', agent: 'plan' }, { parseJSON: false });

    mocks.start.mockRejectedValueOnce(new Error('worktree create/launch failed'));
    let failure: unknown;
    await act(async () => { await Promise.resolve(composer.onSend!('again')).catch((err: unknown) => { failure = err; }); });
    expect(String(failure)).toContain('worktree create/launch failed');
    expect(screen.getByRole('alert')).toHaveTextContent('worktree create/launch failed');
  });

  it('keeps ocman built-ins out of the first submission and opens the worktree form for /wt', async () => {
    mount();
    await waitFor(() => expect(composer.disabled).toBe(false));
    await act(() => composer.onCommand!('wt', 'feature-x'));
    expect(mocks.openWorktreeForm).toHaveBeenCalledWith({ projectDir: '/repo', branch: 'feature-x', remoteId: 'machine' });
    await act(() => composer.onCommand!('rename', 'x'));
    expect(mocks.start).not.toHaveBeenCalled();
    expect(screen.getByRole('alert')).toHaveTextContent('/rename needs an existing conversation');
  });

  it('switches machines by re-pointing the route', async () => {
    mount();
    await waitFor(() => expect(composer.disabled).toBe(false));
    await act(() => composer.onMachineChange!({ remoteId: 'box', remoteName: 'Box', platform: 'r-box:opencode', dir: '/other/repo' }));
    expect(navigate).toHaveBeenCalledWith('/session/new?dir=%2Fother%2Frepo&remoteId=box&platform=r-box%3Aopencode');
    expect(mocks.start).not.toHaveBeenCalled();
  });

  it('does not let an older completion unlock the current generation’s start', async () => {
    let finishOld!: (result: unknown) => void;
    let finishNew!: (result: unknown) => void;
    mocks.start.mockReturnValueOnce(new Promise((resolve) => { finishOld = resolve; }))
      .mockReturnValueOnce(new Promise((resolve) => { finishNew = resolve; }));
    const view = mount({ directory: '/repo', platform: 'r-machine:opencode', remoteId: 'machine', title: 'old' });
    await waitFor(() => expect(composer.disabled).toBe(false));
    let oldRequest!: void | Promise<void>;
    act(() => { oldRequest = composer.onSend!('old prompt'); });
    view.rerender(<NewConversation params={{ directory: '/repo', platform: 'r-machine:opencode', remoteId: 'machine', title: 'new' }}
      whisperAvailable={false} composerRef={null} navigate={navigate} navigateToSession={navigateToSession} />);
    let newRequest!: void | Promise<void>;
    act(() => { newRequest = composer.onSend!('new prompt'); });
    const child = { sessionId: 'child', platform: 'r-machine:opencode', remoteId: 'machine', directory: '/repo', firstMessageSent: true };
    await act(async () => { finishOld(child); await oldRequest; });
    await expect(composer.onSend!('duplicate prompt')).rejects.toThrow('already in progress');
    expect(mocks.start).toHaveBeenCalledTimes(2);
    await act(async () => { finishNew(child); await newRequest; });
  });

  it('reports a failed target lookup with a retry', async () => {
    mocks.info.mockRejectedValueOnce(new Error('offline'));
    mount();
    expect(await screen.findByRole('alert')).toHaveTextContent('offline');
    // The target is unknown, so sending is blocked until the lookup succeeds.
    expect(composer.worktreesSupported).toBe(false);
    mocks.info.mockResolvedValue({ '/repo': { branch: 'main' } });
    act(() => screen.getByRole('button', { name: 'Retry' }).click());
    await waitFor(() => expect(composer.worktreesSupported).toBe(true));
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
  });
});

// @vitest-environment jsdom
import { act, render, renderHook, screen, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { ComposerProps } from '../../components/assistant/composerTypes';
import { clearDraft, getDraft, saveDraft } from '../../lib/composerDraft';
import { useLaunchProgressStore } from '../../lib/launchProgressStore';
import { listFailedSends, clearFailedSends } from '../../lib/failedSends';
import { useFirstSubmission } from './firstSubmission';
import { HeaderContext } from '../../lib/headerContext';
import { clearSettingsCache } from '../../lib/projectSettingsCache';
import { ModelPicker } from '../../components/assistant/ModelPicker';
import { describeModel } from '../../components/assistant/composerModel';
import { useComposerModel } from './useComposerModel';
import type { Message } from '../../lib/api';
import type { SessionMetadata } from '../../lib/sessionReducer';
import { rememberConversationDraft, useNewConversationDrafts } from '../../lib/newConversationDrafts';

const mocks = vi.hoisted(() => ({
  prepare: vi.fn(), start: vi.fn(), info: vi.fn(), worktrees: vi.fn(), baseRef: vi.fn(), post: vi.fn(), seed: vi.fn(),
  openWorktreeForm: vi.fn(), addFavorite: vi.fn(), removeFavorite: vi.fn(), settings: vi.fn(), caps: { shellExec: true },
  progress: new Set<(id: string, step: string, state: string) => void>(),
}));
vi.mock('../../lib/useGlobalEvents', () => ({
  onSessionStartProgress: (cb: (id: string, step: string, state: string) => void) => {
    mocks.progress.add(cb);
    return () => mocks.progress.delete(cb);
  },
}));
vi.mock('../../lib/api', () => ({
  api: { prepareSession: mocks.prepare, startSession: mocks.start, addFavorite: mocks.addFavorite, removeFavorite: mocks.removeFavorite },
  fetchJSON: (url: string, signal?: AbortSignal) => url.startsWith('/api/project/settings') ? mocks.settings(url) : url.startsWith('/api/worktree/list') ? mocks.worktrees(url, signal) : url.startsWith('/api/worktree/default-base-ref') ? mocks.baseRef(url, signal) : mocks.info(url, signal),
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
import { startHandoffs, startModels } from './startHandoffs';

const navigate = vi.fn();
const navigateToSession = vi.fn();
function mount(params = { directory: '/repo', remoteId: 'machine', platform: 'r-machine:opencode' } as Record<string, string | undefined>) {
  return render(<NewConversation params={{ directory: params.directory!, remoteId: params.remoteId, platform: params.platform, title: params.title, draftId: params.draftId }}
    whisperAvailable={false} composerRef={null} navigate={navigate} navigateToSession={navigateToSession} />);
}
const ready = () => waitFor(() => {
  expect(composer.agentsLoaded).toBe(true);
  expect(composer.worktrees).toBeDefined();
});

describe('NewConversation', () => {
  beforeEach(() => {
    vi.resetAllMocks();
    startModels.clear();
    window.localStorage.removeItem('ocman.newSessionCatalogs.v1');
    useNewConversationDrafts.setState({ drafts: [] });
    clearSettingsCache();
    mocks.settings.mockResolvedValue({ models: [], off: false, defaultAgent: 'build' });
    mocks.progress.clear();
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
    mocks.baseRef.mockResolvedValue({ baseRef: 'main' });
    mocks.worktrees.mockResolvedValue({ worktrees: [{ path: '/repo', branch: 'main', main: true }] });
    mocks.start.mockResolvedValue({ sessionId: 'child', platform: 'r-machine:opencode', remoteId: 'machine', directory: '/worktrees/fix', firstMessageSent: true, firstMessageError: '' });
  });

  it('uses the current checkout when the owner has no usable worktree base', async () => {
    mocks.baseRef.mockResolvedValue({ baseRef: '' });
    mount();
    await ready();
    expect(mocks.baseRef).toHaveBeenCalledWith('/api/worktree/default-base-ref?dir=%2Frepo&remoteId=machine', expect.any(AbortSignal));
    expect(composer.worktreesSupported).toBe(false);
    expect(composer.target).toBe('current');
    await act(() => composer.onSend!('First commit'));
    expect(mocks.start).toHaveBeenCalledWith(expect.objectContaining({ worktree: false }));
  });

  it('keeps base lookup failures retryable instead of assuming worktrees are available', async () => {
    mocks.baseRef.mockRejectedValueOnce(new Error('base lookup offline'));
    mount();
    expect(await screen.findByRole('alert')).toHaveTextContent('base lookup offline');
    expect(composer.worktreesSupported).toBe(false);
    await act(async () => screen.getByRole('button', { name: 'Retry' }).click());
    await ready();
    expect(composer.worktreesSupported).toBe(true);
  });

  it('shows the cached catalog immediately, refreshes it, and preserves picks made during refresh', async () => {
    const first = mount();
    await ready();
    first.unmount();
    let finish!: (catalog: unknown) => void;
    mocks.prepare.mockReturnValueOnce(new Promise((resolve) => { finish = resolve; }));
    mount();
    expect(composer.agentsLoaded).toBe(true);
    expect(composer.models).toContain('prov/big');
    expect(composer.selectedModel).toBe('prov/default');
    act(() => composer.onModelChange!('prov/manual'));
    act(() => composer.onAgentChange!('build'));
    await act(async () => finish({ platform: 'r-machine:opencode', agents: [{ name: 'fresh' }], commands: [],
      models: { models: [{ provider: 'prov', model: 'fresh' }] }, defaultModel: 'prov/fresh' }));
    expect(composer.models).toContain('prov/fresh');
    expect(composer.selectedModel).toBe('prov/manual');
    expect(composer.selectedAgent).toBe('build');
  });

  it('does not reuse another machine or directory catalog', async () => {
    const first = mount();
    await ready();
    first.unmount();
    mocks.prepare.mockReturnValue(new Promise(() => {}));
    const otherMachine = mount({ directory: '/repo', remoteId: 'other' });
    expect(composer.agentsLoaded).toBe(false);
    expect(composer.models).not.toContain('prov/big');
    otherMachine.unmount();
    mount({ directory: '/other', remoteId: 'machine', platform: 'r-machine:opencode' });
    expect(composer.agentsLoaded).toBe(false);
  });

  it('keeps cached choices visible on refresh failure but waits for fresh preparation before sending', async () => {
    const first = mount();
    await ready();
    first.unmount();
    let fail!: (error: Error) => void;
    mocks.prepare.mockReturnValueOnce(new Promise((_resolve, reject) => { fail = reject; }));
    mount();
    let submission!: Promise<void>;
    act(() => { submission = composer.onSend!('hello') as Promise<void>; });
    expect(mocks.start).not.toHaveBeenCalled();
    const rejected = expect(submission).rejects.toThrow('offline');
    await act(async () => { fail(new Error('offline')); });
    await rejected;
    expect(composer.agentsLoaded).toBe(true);
    expect(composer.models).toContain('prov/big');
    expect(mocks.start).not.toHaveBeenCalled();
  });

  it('restores each prepared draft selection and replaces only the draft that starts', async () => {
    const first = mount({ directory: '/repo', draftId: 'first' });
    await ready();
    act(() => {
      composer.onAgentChange!('plan');
      composer.onReasoningChange!('high');
      composer.onTargetChange!('current');
    });
    saveDraft('first', 'first prompt');
    first.unmount();
    const second = mount({ directory: '/repo', draftId: 'second' });
    await ready();
    expect(composer.selectedAgent).toBe('');
    act(() => composer.onModelChange!('prov/big'));
    saveDraft('second', 'second prompt');
    second.unmount();
    mount({ directory: '/repo', draftId: 'first' });
    await ready();
    expect(composer.selectedAgent).toBe('plan');
    expect(composer.selectedModel).toBe('prov/plan-model');
    expect(composer.selectedReasoning).toBe('high');
    expect(composer.target).toBe('current');
    expect(composer.draftKey).toBe('first');
    await act(async () => { await composer.onSend!('first prompt'); });
    expect(useNewConversationDrafts.getState().drafts.map((draft) => draft.draftId)).toEqual(['second']);
    expect(getDraft('second')).toBe('second prompt');
    expect(getDraft('first')).toBe('');
    expect(navigateToSession).toHaveBeenCalledWith('child');
  });

  it('submits the current checkout when a restored worktree is no longer available', async () => {
    rememberConversationDraft({ draftId: 'first', directory: '/repo', target: 'dir:/old/worktree' });
    mount({ directory: '/repo', draftId: 'first' });
    await ready();
    expect(composer.target).toBe('current');
    await act(async () => { await composer.onSend!('run here'); });
    expect(mocks.start).toHaveBeenCalledWith(expect.objectContaining({ directory: '/repo', worktree: false }));
  });

  it('does not carry an existing-worktree target to a different owner', async () => {
    mocks.worktrees.mockResolvedValue({ worktrees: [{ path: '/repo', main: true }, { path: '/repo/wt', branch: 'feat', main: false }] });
    const view = mount({ directory: '/repo', draftId: 'first' });
    await ready();
    act(() => composer.onTargetChange!('dir:/repo/wt'));
    await act(() => composer.onMachineChange!({ dir: '/repo', remoteId: 'box', remoteName: 'Box', platform: 'r-box:opencode' }));
    view.rerender(<NewConversation params={{ directory: '/repo', draftId: 'first', remoteId: 'box', platform: 'r-box:opencode' }}
      whisperAvailable={false} composerRef={null} navigate={navigate} navigateToSession={navigateToSession} />);
    await ready();
    expect(composer.target).toBe('current');
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
    expect(await screen.findByRole('alert')).toHaveTextContent('catalog offline');
    // Preparing only reads catalogs: it never reports an instance launch.
    expect(useLaunchProgressStore.getState().error).not.toBe('catalog offline');
    expect(composer.agentsLoaded).toBe(false);
    act(() => composer.onRefreshModels!());
    await waitFor(() => expect(composer.agentsLoaded).toBe(true));
  });

  it('does not flag historical models when prepared provider availability is unknown', async () => {
    mocks.prepare.mockResolvedValue({
      platform: 'r-machine:opencode', agents: [], commands: [], defaultModel: 'prov/custom',
      models: { hasProviders: false, models: [{ provider: 'prov', model: 'custom', isAvailable: false }] },
    });
    mount();
    await ready();
    expect(describeModel('prov/custom', composer.modelEntries).unavailable).toBe(false);
    Element.prototype.scrollIntoView = vi.fn();
    render(<ModelPicker open models={composer.models ?? []} modelEntries={composer.modelEntries} onSelect={vi.fn()} onClose={vi.fn()} />);
    expect(screen.queryByText('provider disconnected')).not.toBeInTheDocument();
    expect(screen.queryByText('Disconnected providers')).not.toBeInTheDocument();
    expect(screen.getByRole('option', { name: /custom/ })).toBeInTheDocument();
  });

  it('keeps a failed custom command as child-keyed retry state', async () => {
    mocks.post.mockRejectedValueOnce(new Error('command offline'));
    mount();
    await ready();
    await act(() => composer.onCommand!('review', 'main'));
    await waitFor(() => expect(useFirstSubmission.getState().entries.child).toMatchObject({ text: '/review main', error: 'command offline' }));
    expect(navigateToSession).toHaveBeenCalledWith('child');
  });

  it('prepares the directory on its owner and offers its catalog before any session exists', async () => {
    mount();
    // Usable immediately: a submission waits for the catalog instead.
    expect(composer.disabled).toBeFalsy();
    await ready();
    expect(mocks.prepare).toHaveBeenCalledWith({ directory: '/repo', remoteId: 'machine', platform: 'r-machine:opencode' }, expect.any(AbortSignal));
    expect(mocks.info).toHaveBeenCalledWith('/api/git/info?dir=%2Frepo&remoteId=machine', expect.any(AbortSignal));
    await waitFor(() => expect(composer.agentsLoaded).toBe(true));
    expect(composer.target).toBe('worktree');
    expect(composer.worktreesSupported).toBe(true);
    expect(composer.activeAgent).toBe('build');
    await waitFor(() => expect(composer.selectedModel).toBe('prov/default'));
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

  it('uses the configured agent, refreshes on invalidation, and preserves an explicit pick', async () => {
    mocks.settings.mockResolvedValue({ defaultAgent: 'custom' });
    mount();
    await ready();
    expect(composer.activeAgent).toBe('custom');
    mocks.settings.mockResolvedValue({ defaultAgent: 'plan' });
    act(() => clearSettingsCache());
    await waitFor(() => expect(composer.activeAgent).toBe('plan'));
    act(() => composer.onAgentChange!('build'));
    await act(() => composer.onSend!('implement'));
    expect(mocks.start).toHaveBeenCalledWith(expect.objectContaining({ send: expect.objectContaining({ agent: 'build' }) }));
  });

  it('creates the session at the target with the first prompt and moves there', async () => {
    mount({ directory: '/repo', remoteId: 'machine', platform: 'r-machine:opencode', title: 'Login' });
    await ready();
    await waitFor(() => expect(composer.agentsLoaded).toBe(true));
    act(() => composer.onAgentChange!('plan'));
    expect(composer.selectedModel).toBe('prov/plan-model');
    const images = [{ url: 'data:image/png;base64,abc', mime: 'image/png' }];
    saveDraft('new', 'Fix login');
    await act(() => composer.onSend!('Fix login', images));
    expect(mocks.start).toHaveBeenCalledWith({
      directory: '/repo', remoteId: 'machine', platform: 'r-machine:opencode', title: 'Login', prompt: 'Fix login', worktree: true, startId: expect.any(String),
      send: { message: 'Fix login', images, model: 'prov/plan-model', agent: 'plan', reasoning: undefined },
    });
    expect(mocks.seed).toHaveBeenCalledWith('child', '/worktrees/fix', 'r-machine:opencode', 'Login', 'machine');
    expect(startModels.get('child')).toBe('prov/plan-model');
    expect(navigateToSession).toHaveBeenCalledWith('child');
    // The shared new-conversation draft now belongs to the session.
    expect(getDraft('new')).toBe('');
    expect(getDraft('child')).toBe('');
  });

  it('keeps the chosen model for follow-ups before the first response arrives', async () => {
    mount();
    await ready();
    act(() => composer.onModelChange!('prov/manual'));
    await act(() => composer.onSend!('First prompt'));
    expect(mocks.start).toHaveBeenCalledWith(expect.objectContaining({
      send: expect.objectContaining({ model: 'prov/manual' }),
    }));

    const setSelectedModel = vi.fn();
    const options = {
      id: 'child',
      session: { id: 'child', directory: '/worktrees/fix', defaultModel: 'prov/default', projectDefaultModel: 'prov/project' } as SessionMetadata,
      messages: [] as Message[], parts: [], modelOptions: [], agents: [],
      setSelectedModel, setSelectedAgent: vi.fn(), setSelectedReasoning: vi.fn(),
    };
    const { rerender } = renderHook(useComposerModel, { initialProps: options });
    expect(setSelectedModel).toHaveBeenLastCalledWith('prov/manual');
    expect(startModels.has('child')).toBe(false);
    rerender({ ...options, messages: [{
      id: 'first', sessionId: 'child', timeCreated: 1,
      data: { role: 'user', model: { providerID: 'prov', modelID: 'manual' } },
    } as Message] });
    expect(setSelectedModel).toHaveBeenCalledTimes(1);
    expect(setSelectedModel).toHaveBeenLastCalledWith('prov/manual');
  });

  it('honours the current-checkout target and preserves an unsent prompt for retry', async () => {
    mocks.start.mockResolvedValue({ sessionId: 's2', platform: 'r-machine:opencode', remoteId: 'machine', directory: '/repo', firstMessageSent: false, firstMessageError: 'boom' });
    mount();
    await ready();
    act(() => composer.onTargetChange!('current'));
    act(() => composer.onReasoningChange!('high'));
    await act(() => composer.onSend!('Fix login'));
    expect(mocks.start).toHaveBeenCalledWith(expect.objectContaining({ worktree: false }));
    expect(listFailedSends('s2')).toEqual([expect.objectContaining({ text: 'Fix login', error: 'boom', reasoning: 'high' })]);
    expect(startModels.get('s2')).toBe('prov/default');
    expect(navigateToSession).toHaveBeenCalledWith('s2');
    // Failed-send recovery owns the prompt; a handoff would resurrect it on Dismiss.
    expect(startHandoffs.has('s2')).toBe(false);
  });

  it('starts in an existing worktree selected from the owner’s list', async () => {
    mocks.worktrees.mockResolvedValue({ worktrees: [
      { path: '/repo', branch: 'main', main: true },
      { path: '/feature', branch: 'feature', main: false },
    ] });
    mount();
    await ready();
    expect(composer.worktrees).toEqual([{ path: '/feature', branch: 'feature' }]);
    act(() => composer.onTargetChange!('dir:/feature'));
    await act(() => composer.onSend!('Fix login'));
    expect(mocks.start).toHaveBeenCalledWith(expect.objectContaining({ directory: '/feature', worktree: false }));
  });

  it('cannot start in a worktree from a linked worktree or a non-repository', async () => {
    mocks.prepare.mockResolvedValue({ platform: 'opencode', agents: [], commands: [], models: { models: [] }, liveConnection: true });
    mocks.worktrees.mockResolvedValue({ worktrees: [{ path: '/repo', branch: 'main', main: true }, { path: '/wt/feat', branch: 'feat' }] });
    mount({ directory: '/wt/feat/sub', remoteId: 'local', platform: undefined });
    await ready();
    expect(composer.worktreesSupported).toBe(false);
    expect(composer.target).toBe('current');
    await act(() => composer.onSend!('hi'));
    expect(mocks.start).toHaveBeenCalledWith(expect.objectContaining({ worktree: false, platform: 'opencode' }));
  });

  it('runs a platform command on the new session itself, and surfaces start failures', async () => {
    mocks.post.mockResolvedValue(undefined);
    mount();
    await ready();
    await act(() => composer.onCommand!('review', 'main'));
    expect(startModels.get('child')).toBe('prov/default');
    expect(mocks.start).toHaveBeenCalledWith(expect.objectContaining({ prompt: '/review main', send: undefined }));
    await waitFor(() => expect(mocks.post).toHaveBeenCalledWith('/api/session/child/command?platform=r-machine%3Aopencode',
      expect.objectContaining({ command: 'review', arguments: 'main' }), { parseJSON: false }));
    await act(() => composer.onShell!('ls'));
    expect(startModels.get('child')).toBe('prov/default');
    expect(mocks.post).toHaveBeenCalledWith('/api/session/child/shell?platform=r-machine%3Aopencode', { command: 'ls', agent: 'build' }, { parseJSON: false });

    mocks.start.mockRejectedValueOnce(new Error('worktree create/launch failed'));
    let failure: unknown;
    await act(async () => { await Promise.resolve(composer.onSend!('again')).catch((err: unknown) => { failure = err; }); });
    expect(String(failure)).toContain('worktree create/launch failed');
    expect(screen.getByRole('alert')).toHaveTextContent('worktree create/launch failed');
    // A failed start removes the pending prompt; the draft stays in the composer.
    expect(screen.queryByTestId('pending-prompt')).not.toBeInTheDocument();
  });

  it('starts without crypto.randomUUID (plain-HTTP, non-secure context)', async () => {
    vi.stubGlobal('crypto', { getRandomValues: (values: Uint32Array) => values.fill(1) });
    try {
      mount();
      await ready();
      await act(() => composer.onSend!('Fix login'));
      expect(mocks.start).toHaveBeenCalledWith(expect.objectContaining({ startId: '00000001'.repeat(4) }));
      expect(navigateToSession).toHaveBeenCalledWith('child');
    } finally {
      vi.unstubAllGlobals();
    }
  });

  it('keeps a newer start’s prompt when an older start fails afterwards', async () => {
    let failOld!: (err: Error) => void;
    mocks.start.mockReturnValueOnce(new Promise((_, reject) => { failOld = reject; }));
    mocks.start.mockReturnValueOnce(new Promise(() => {}));
    const view = mount({ directory: '/repo', remoteId: 'machine', platform: 'r-machine:opencode', title: 'A' });
    await ready();
    let old!: Promise<void>;
    act(() => { old = Promise.resolve(composer.onSend!('first')).catch(() => {}); });
    await waitFor(() => expect(mocks.start).toHaveBeenCalledTimes(1));
    // Re-point the same mounted page (new title), then start again.
    view.rerender(<NewConversation params={{ directory: '/repo', remoteId: 'machine', platform: 'r-machine:opencode', title: 'B' }}
      whisperAvailable={false} composerRef={null} navigate={navigate} navigateToSession={navigateToSession} />);
    await ready();
    act(() => { void Promise.resolve(composer.onSend!('second')).catch(() => {}); });
    await waitFor(() => expect(mocks.start).toHaveBeenCalledTimes(2));
    expect(screen.getByTestId('pending-prompt')).toHaveTextContent('second');
    await act(async () => { failOld(new Error('old failed')); await old; });
    expect(screen.getByTestId('pending-prompt')).toHaveTextContent('second');
  });

  it('shows the prompt as the first message with the server-reported steps until the session exists', async () => {
    let finish!: (value: unknown) => void;
    mocks.start.mockReturnValueOnce(new Promise((resolve) => { finish = resolve; }));
    mount();
    await ready();
    let sent!: Promise<void>;
    act(() => { sent = Promise.resolve(composer.onSend!('Fix login')); });
    expect(screen.getByTestId('pending-prompt')).toHaveTextContent('Fix login');
    expect(screen.getByTestId('start-progress')).toHaveTextContent('Starting session');
    await waitFor(() => expect(mocks.start).toHaveBeenCalled());
    const { startId } = mocks.start.mock.calls[0][0];
    act(() => {
      for (const cb of mocks.progress) {
        cb('someone-else', 'session', 'active');
        cb(startId, 'opencode', 'active');
        cb(startId, 'worktree', 'active');
        cb(startId, 'worktree', 'done');
      }
    });
    // Parallel steps render together, in a fixed order.
    expect(screen.getByTestId('start-step-opencode')).toHaveTextContent('Starting OpenCode');
    expect(screen.getByTestId('start-step-worktree')).toHaveTextContent('Worktree ready');
    expect(screen.queryByTestId('start-step-session')).not.toBeInTheDocument();
    await act(async () => { finish({ sessionId: 'child', platform: 'r-machine:opencode', remoteId: 'machine', directory: '/wt', firstMessageSent: true }); await sent; });
    expect(navigateToSession).toHaveBeenCalledWith('child');
    expect(mocks.progress.size).toBe(0);
    // The session view keeps showing them until the first message arrives.
    expect(startHandoffs.get('child')).toEqual({ prompt: 'Fix login', steps: { opencode: 'active', worktree: 'done' } });
  });

  it('keeps ocman built-ins out of the first submission and opens the worktree form for /wt', async () => {
    mount();
    await ready();
    await act(() => composer.onCommand!('wt', 'feature-x'));
    expect(mocks.openWorktreeForm).toHaveBeenCalledWith({ projectDir: '/repo', branch: 'feature-x', remoteId: 'machine' });
    await act(() => composer.onCommand!('rename', 'x'));
    expect(mocks.start).not.toHaveBeenCalled();
    expect(screen.getByRole('alert')).toHaveTextContent('/rename needs an existing conversation');
  });

  it('switches machines by re-pointing the route', async () => {
    mount();
    await ready();
    await act(() => composer.onMachineChange!({ remoteId: 'box', remoteName: 'Box', platform: 'r-box:opencode', dir: '/other/repo' }));
    expect(navigate).toHaveBeenCalledWith('/session/new?dir=%2Fother%2Frepo&remoteId=box&platform=r-box%3Aopencode&draftId=new');
    expect(mocks.start).not.toHaveBeenCalled();
  });

  it('does not let an older completion unlock the current generation’s start', async () => {
    let finishOld!: (result: unknown) => void;
    let finishNew!: (result: unknown) => void;
    mocks.start.mockReturnValueOnce(new Promise((resolve) => { finishOld = resolve; }))
      .mockReturnValueOnce(new Promise((resolve) => { finishNew = resolve; }));
    const view = mount({ directory: '/repo', platform: 'r-machine:opencode', remoteId: 'machine', title: 'old' });
    await ready();
    let oldRequest!: void | Promise<void>;
    act(() => { oldRequest = composer.onSend!('old prompt'); });
    await waitFor(() => expect(mocks.start).toHaveBeenCalledTimes(1));
    view.rerender(<NewConversation params={{ directory: '/repo', platform: 'r-machine:opencode', remoteId: 'machine', title: 'new' }}
      whisperAvailable={false} composerRef={null} navigate={navigate} navigateToSession={navigateToSession} />);
    await ready();
    let newRequest!: void | Promise<void>;
    act(() => { newRequest = composer.onSend!('new prompt'); });
    await waitFor(() => expect(mocks.start).toHaveBeenCalledTimes(2));
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

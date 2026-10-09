// @vitest-environment jsdom
import { useRef, useState } from 'react';
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { BackendUnavailableError, api } from '../../lib/api';
import { clearDraft, getDraft, resetDraftTextsForTests, saveDraft } from '../../lib/composerDraft';
import { clearFailedSends, listFailedSends } from '../../lib/failedSends';
import { saveProjectModel } from '../../lib/projectModel';
import { useApiStore } from '../../lib/apiStore';
import { Composer } from '../../components/assistant/Composer';
import { SessionComposerSlot } from './SessionComposerSlot';
import { NewConversation } from './NewConversation';
import { useFailedSendRehydrate } from './useFailedSendRehydrate';
import { usePendingSend } from './usePendingSend';
import { useSessionActions, type UseSessionActionsOptions } from './useSessionActions';
import { resetFirstSubmissionForTests } from './firstSubmission';
import type { NewSessionParams } from '../../lib/newSessionPath';
import { forgetConversationDraft, rememberConversationDraft, useNewConversationDrafts } from '../../lib/newConversationDrafts';
import { resetDraftPayloadsForTests } from '../../lib/pendingDraftPayloads';
import { SidebarConversationDrafts } from './SidebarConversationDrafts';
import { MemoryRouter, useLocation, useNavigate } from 'react-router-dom';
import { newSessionPath, parseNewSessionParams } from '../../lib/newSessionPath';

vi.mock('../../lib/useCapabilities', () => ({
  useOpencodeLaunch: () => true,
  usePlatformCapabilities: () => ({ shellExec: true }),
}));
vi.mock('../../components/FactoryPlanApproval', () => ({ FactoryPlanApproval: () => null }));
vi.mock('../../components/FactorySessionRecovery', () => ({ FactorySessionRecovery: () => null }));
vi.mock('../../lib/remoteLog', () => ({ remoteLog: { error: vi.fn() } }));
vi.mock('../../lib/api', async (original) => {
  const actual = await original<typeof import('../../lib/api')>();
  return {
    ...actual,
    fetchJSON: vi.fn(async (url: string) => url.startsWith('/api/worktree/default-base-ref') ? { baseRef: 'main' } : url.startsWith('/api/worktree/list')
      ? { worktrees: [{ path: '/repo', branch: 'main', main: true }] }
      : { '/repo': { branch: 'main' } }),
    postJSON: vi.fn(),
    api: { ...actual.api, prepareSession: vi.fn(), startSession: vi.fn(), sendMessage: vi.fn(), uploadComposerAttachment: vi.fn(),
      resolveTargets: vi.fn(async () => ({ candidates: [], remotes: [] })), commands: vi.fn(async () => []) },
  };
});

import { postJSON } from '../../lib/api';

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (error: Error) => void;
  const promise = new Promise<T>((res, rej) => { resolve = res; reject = rej; });
  return { promise, resolve, reject };
}

const prepared = { platform: 'opencode', agents: [], commands: [], models: { hasProviders: true, models: [] }, liveConnection: true };
const created = { sessionId: 'child', platform: 'opencode', remoteId: 'local', directory: '/repo', firstMessageSent: true, firstMessageError: '' };

/** Another tab over the same database. */
async function peerTab() {
  vi.resetModules();
  const peer = await import('../../lib/newConversationDrafts');
  await peer.hydrateDrafts();
  return { peerForget: async (id: string) => {
    peer.forgetConversationDraft(id);
    await waitFor(() => expect(useNewConversationDrafts.getState().drafts.some((draft) => draft.draftId === id)).toBe(false));
  } };
}

function DraftWorkspace() {
  const location = useLocation();
  const navigate = useNavigate();
  const params = parseNewSessionParams(new URLSearchParams(location.search));
  return <>
    <output data-testid="draft-route">{location.pathname}{location.search}</output>
    <button onClick={() => navigate('/session/new?dir=%2Frepo&draftId=second&title=Second')}>Another draft</button>
    <SidebarConversationDrafts searchQuery="" />
    {params && <NewConversation params={params} composerRef={null} whisperAvailable={false}
      navigate={navigate} navigateToSession={(id) => navigate(`/session/${id}`)} />}
  </>;
}

function Child() {
  const pending = usePendingSend('child');
  const { failedSends, setFailedSends } = useFailedSendRehydrate({ id: 'child', sessionLoaded: true, messages: [], parts: [], pending });
  const busy = useRef(false);
  const noop = () => {};
  const actions = useSessionActions({
    session: { id: 'child', platform: 'opencode', directory: '/repo', timeUpdated: 0 },
    portAvailable: true, caps: {} as UseSessionActionsOptions['caps'], pendingPermission: null, pendingQuestion: null,
    selectedModel: '', selectedAgent: '', selectedReasoning: '', activeAgent: '',
    recentSessionsRef: useRef([]), messagesRef: useRef([]), partsRef: useRef([]), isRunningRef: busy,
    failedSends, setFailedSends, pending, navigate: noop, navigateToSession: noop, openWorktreeForm: noop,
    handleCompact: async () => {}, handleNewSession: async () => {}, handleTmuxShortcut: noop,
    setShowRenameModal: noop, setShowForkPicker: noop, setShowMovePicker: noop, setShowRenameToast: noop,
    setShowDisconnectedToast: noop, setRestartToastMessage: noop, setCopyToastMessage: noop,
  });
  return <>
    {failedSends.map((entry) => <div role="alert" key={entry.id}>
      {entry.error}<button onClick={() => actions.handleRetrySend(entry.id)}>Retry message</button>
    </div>)}
    <SessionComposerSlot sessionId="child" platformId="opencode" factoryEpicID=""
    firstUnreadMessageId={null} unreadMessageCount={0} onJumpToUnread={() => {}}
    composer={{ sessionId: 'child', isRunning: false, onSend: actions.handleSend }} />
  </>;
}

function Flow({ params = { directory: '/repo', platform: 'opencode' } }: { params?: NewSessionParams } = {}) {
  const [route, setRoute] = useState('new');
  return <>
    <output data-testid="route">{route}</output>
    <button onClick={() => setRoute('other')}>Leave draft</button>
    {route === 'new' ? <NewConversation params={{ draftId: 'new', ...params }} composerRef={null}
      whisperAvailable={false} navigate={(path) => setRoute(path.startsWith('/session/') && !path.startsWith('/session/new') ? path.slice('/session/'.length) : path)} navigateToSession={setRoute} /> : route === 'child' ? <Child />
      : <Composer draftKey="new" isRunning={false} />}
  </>;
}

beforeEach(() => {
  vi.clearAllMocks();
  resetFirstSubmissionForTests();
  window.localStorage.clear();
  resetDraftTextsForTests();
  resetDraftPayloadsForTests();
  useNewConversationDrafts.setState({ drafts: [], starts: {} });
  clearFailedSends('child');
  clearDraft('new');
  vi.spyOn(useApiStore.getState(), 'seedNewSession').mockImplementation(() => {});
  vi.mocked(api.prepareSession).mockResolvedValue(prepared);
  vi.mocked(api.startSession).mockReset().mockResolvedValue(created);
  vi.mocked(api.sendMessage).mockResolvedValue(undefined);
  vi.mocked(postJSON).mockResolvedValue(undefined);
});

describe('new-conversation submission lifecycle', () => {
  it('retires a pre-prepare submission when its model is automatically seeded', async () => {
    const catalog = deferred<typeof prepared & { defaultModel: string }>();
    vi.mocked(api.prepareSession).mockReturnValue(catalog.promise);
    render(<MemoryRouter initialEntries={['/session/new?dir=%2Frepo&draftId=first&title=First']}><DraftWorkspace /></MemoryRouter>);
    fireEvent.input(screen.getByRole('textbox'), { target: { value: 'submitted' } });
    fireEvent.keyDown(screen.getByRole('textbox'), { key: 'Enter' });
    await act(async () => catalog.resolve({ ...prepared, defaultModel: 'provider/default' }));
    await waitFor(() => expect(api.startSession).toHaveBeenCalledTimes(1));
    await waitFor(() => expect(screen.getByTestId('draft-route')).toHaveTextContent('/session/child'));
    expect(useNewConversationDrafts.getState().drafts).toHaveLength(0);
  });

  it('shows and submits late attachments when reopened before image conversion completes', async () => {
    const callbacks: (() => void)[] = [];
    const original = globalThis.FileReader;
    class DelayedReader {
      result = 'data:image/png;base64,bm90ZQ==';
      onload: (() => void) | null = null;
      readAsDataURL() { callbacks.push(() => this.onload?.()); }
    }
    const file = new File(['note'], 'late-reopened.txt', { type: 'text/plain' });
    vi.stubGlobal('FileReader', DelayedReader);
    vi.mocked(api.uploadComposerAttachment).mockResolvedValue({ path: '/tmp/late-reopened.txt', name: file.name, mime: file.type, size: file.size });
    try {
      render(<MemoryRouter initialEntries={['/session/new?dir=%2Frepo&draftId=first&title=First']}><DraftWorkspace /></MemoryRouter>);
      fireEvent.drop(screen.getByRole('textbox'), { dataTransfer: { files: [new File(['image'], 'late.png', { type: 'image/png' }), file] } });
      fireEvent.click(screen.getByRole('button', { name: 'Another draft' }));
      fireEvent.click(screen.getByRole('button', { name: /First/ }));
      fireEvent.input(screen.getByRole('textbox'), { target: { value: 'submit attachments' } });
      fireEvent.keyDown(screen.getByRole('textbox'), { key: 'Enter' });
      expect(api.startSession).not.toHaveBeenCalled();
      expect(screen.getByRole('button', { name: 'Send message' })).toBeDisabled();
      await act(async () => callbacks.forEach((finish) => finish()));
      expect(await screen.findByText(file.name)).toBeInTheDocument();
      expect(screen.getByRole('img', { name: 'Attachment 1' })).toBeInTheDocument();
      fireEvent.input(screen.getByRole('textbox'), { target: { value: 'submit attachments' } });
      fireEvent.keyDown(screen.getByRole('textbox'), { key: 'Enter' });
      await waitFor(() => expect(api.uploadComposerAttachment).toHaveBeenCalledWith('child', file, 'opencode'));
      await waitFor(() => expect(api.sendMessage).toHaveBeenCalled());
      expect(vi.mocked(api.sendMessage).mock.calls.at(-1)?.[2]).toEqual([expect.objectContaining({ url: 'data:image/png;base64,bm90ZQ==' })]);
    } finally { vi.stubGlobal('FileReader', original); }
  });

  it('retains peer selections changed before this composer submits its own selections', async () => {
    render(<MemoryRouter initialEntries={['/session/new?dir=%2Frepo&draftId=first&title=First']}><DraftWorkspace /></MemoryRouter>);
    await waitFor(() => expect(api.prepareSession).toHaveBeenCalled());
    act(() => rememberConversationDraft({ draftId: 'first', directory: '/repo', agent: 'plan' }));
    fireEvent.input(screen.getByRole('textbox'), { target: { value: 'submitted' } });
    fireEvent.keyDown(screen.getByRole('textbox'), { key: 'Enter' });
    await waitFor(() => expect(api.startSession).toHaveBeenCalledTimes(1));
    expect(vi.mocked(api.startSession).mock.calls[0][0].send?.agent).not.toBe('plan');
    await waitFor(() => expect(useNewConversationDrafts.getState().drafts.some((draft) => draft.agent === 'plan' && draft.draftId !== 'first')).toBe(true));
  });

  it('transfers attachment payloads to a retained replacement', async () => {
    const request = deferred<typeof created>();
    vi.mocked(api.startSession).mockReturnValue(request.promise);
    render(<MemoryRouter initialEntries={['/session/new?dir=%2Frepo&draftId=first&title=First']}><DraftWorkspace /></MemoryRouter>);
    fireEvent.drop(screen.getByRole('textbox'), { dataTransfer: { files: [new File(['note'], 'retained.txt', { type: 'text/plain' })] } });
    await screen.findByText('retained.txt');
    fireEvent.input(screen.getByRole('textbox'), { target: { value: 'submitted' } });
    fireEvent.keyDown(screen.getByRole('textbox'), { key: 'Enter' });
    await waitFor(() => expect(api.startSession).toHaveBeenCalledTimes(1));
    act(() => saveDraft('first', 'retained task'));
    await act(async () => request.resolve(created));
    await waitFor(() => expect(screen.getByRole('textbox')).toHaveValue('retained task'));
    expect(screen.getByText('retained.txt')).toBeInTheDocument();
  });

  it('replaces a retired draft history entry so Back reaches the preceding page', async () => {
    const request = deferred<typeof created>();
    vi.mocked(api.startSession).mockReturnValue(request.promise);
    function History() {
      const navigate = useNavigate();
      return <><button onClick={() => navigate(-1)}>Back</button><DraftWorkspace /></>;
    }
    render(<MemoryRouter initialEntries={['/before', '/session/new?dir=%2Frepo&draftId=first&title=First']}><History /></MemoryRouter>);
    fireEvent.input(screen.getByRole('textbox'), { target: { value: 'submitted' } });
    fireEvent.keyDown(screen.getByRole('textbox'), { key: 'Enter' });
    await waitFor(() => expect(api.startSession).toHaveBeenCalledTimes(1));
    act(() => saveDraft('first', 'retained task'));
    await act(async () => request.resolve(created));
    await waitFor(() => expect(screen.getByRole('textbox')).toHaveValue('retained task'));
    fireEvent.click(screen.getByRole('button', { name: /^Back$/ }));
    await waitFor(() => expect(screen.getByTestId('draft-route')).toHaveTextContent('/before'));
  });

  it('retains a mixed drop when image reading finishes after navigation', async () => {
    const callbacks: (() => void)[] = [];
    const original = globalThis.FileReader;
    class DelayedReader {
      result = 'data:image/png;base64,bm90ZQ==';
      onload: (() => void) | null = null;
      readAsDataURL() { callbacks.push(() => this.onload?.()); }
    }
    vi.stubGlobal('FileReader', DelayedReader);
    try {
      render(<MemoryRouter initialEntries={['/session/new?dir=%2Frepo&draftId=first&title=First']}><DraftWorkspace /></MemoryRouter>);
      fireEvent.drop(screen.getByRole('textbox'), { dataTransfer: { files: [new File(['image'], 'late.png', { type: 'image/png' }), new File(['note'], 'mixed.txt', { type: 'text/plain' })] } });
      fireEvent.click(screen.getByRole('button', { name: 'Another draft' }));
      await act(async () => callbacks.forEach((finish) => finish()));
      fireEvent.click(screen.getByRole('button', { name: /First/ }));
      expect(await screen.findByText('mixed.txt')).toBeInTheDocument();
      expect(screen.getByRole('img', { name: 'Attachment 1' })).toBeInTheDocument();
    } finally { vi.stubGlobal('FileReader', original); }
  });
  it('preserves model/agent/reasoning/target edits made while the API request is unresolved', async () => {
    const request = deferred<typeof created>();
    vi.mocked(api.startSession).mockReturnValue(request.promise);
    render(<MemoryRouter initialEntries={['/session/new?dir=%2Frepo&draftId=first&title=First']}><DraftWorkspace /></MemoryRouter>);
    fireEvent.input(screen.getByRole('textbox'), { target: { value: 'submitted' } });
    fireEvent.keyDown(screen.getByRole('textbox'), { key: 'Enter' });
    await waitFor(() => expect(api.startSession).toHaveBeenCalledTimes(1));
    act(() => rememberConversationDraft({ draftId: 'first', directory: '/repo', title: 'First', remoteId: 'local',
      model: 'p/new', agent: 'plan', reasoning: 'high', target: 'current' }));
    await act(async () => request.resolve(created));
    await waitFor(() => expect(useNewConversationDrafts.getState().starts.first.replacementDraftId).toBeTruthy());
    const replacement = useNewConversationDrafts.getState().starts.first.replacementDraftId;
    expect(useNewConversationDrafts.getState().drafts.find((draft) => draft.draftId === replacement))
      .toMatchObject({ model: 'p/new', agent: 'plan', reasoning: 'high', target: 'current' });
  });
  it('restores attachments submitted before leaving a pending first start', async () => {
    const first = deferred<typeof created>();
    vi.mocked(api.startSession).mockReturnValue(first.promise);
    render(<MemoryRouter initialEntries={['/session/new?dir=%2Frepo&draftId=first&title=First']}><DraftWorkspace /></MemoryRouter>);
    fireEvent.drop(screen.getByRole('textbox'), { dataTransfer: { files: [new File(['note'], 'original.txt', { type: 'text/plain' })] } });
    await screen.findByText('original.txt');
    fireEvent.input(screen.getByRole('textbox'), { target: { value: 'original prompt' } });
    fireEvent.keyDown(screen.getByRole('textbox'), { key: 'Enter' });
    await waitFor(() => expect(api.startSession).toHaveBeenCalledTimes(1));
    fireEvent.click(screen.getByRole('button', { name: 'Another draft' }));
    fireEvent.click(screen.getByRole('button', { name: /First/ }));
    await act(async () => first.reject(new Error('creation failed')));
    await waitFor(() => expect(screen.getByRole('textbox')).toHaveValue('original prompt'));
    expect(screen.getByText('original.txt')).toBeInTheDocument();
  });
  it('keeps retry attachments through repeated failures after reopening a pending draft', async () => {
    const first = deferred<typeof created>();
    vi.mocked(api.startSession).mockReturnValueOnce(first.promise).mockRejectedValue(new Error('retry failed'));
    render(<MemoryRouter initialEntries={['/session/new?dir=%2Frepo&draftId=first&title=First']}><DraftWorkspace /></MemoryRouter>);
    fireEvent.input(screen.getByRole('textbox'), { target: { value: 'first prompt' } });
    fireEvent.keyDown(screen.getByRole('textbox'), { key: 'Enter' });
    await waitFor(() => expect(api.startSession).toHaveBeenCalledTimes(1));
    fireEvent.click(screen.getByRole('button', { name: 'Another draft' }));
    fireEvent.click(screen.getByRole('button', { name: /First/ }));
    await act(async () => first.reject(new Error('first failed')));
    await waitFor(() => expect(screen.getByRole('textbox')).toHaveValue('first prompt'));
    fireEvent.drop(screen.getByRole('textbox'), { dataTransfer: { files: [new File(['note'], 'retry.txt', { type: 'text/plain' })] } });
    expect(await screen.findByText('retry.txt')).toBeInTheDocument();
    fireEvent.keyDown(screen.getByRole('textbox'), { key: 'Enter' });
    await waitFor(() => expect(api.startSession).toHaveBeenCalledTimes(2));
    await waitFor(() => expect(screen.getByRole('textbox')).not.toBeDisabled());
    expect(screen.getByText('retry.txt')).toBeInTheDocument();
  });
  it('moves retained newer text to a usable fresh identity and submits that task', async () => {
    const request = deferred<typeof created>();
    vi.mocked(api.startSession).mockReturnValueOnce(request.promise).mockResolvedValue({ ...created, sessionId: 'next-child' });
    render(<MemoryRouter initialEntries={['/session/new?dir=%2Frepo&draftId=first&title=First']}><DraftWorkspace /></MemoryRouter>);
    fireEvent.input(screen.getByRole('textbox'), { target: { value: 'first task' } });
    fireEvent.keyDown(screen.getByRole('textbox'), { key: 'Enter' });
    await waitFor(() => expect(api.startSession).toHaveBeenCalledTimes(1));
    act(() => saveDraft('first', 'retained next task'));
    await act(async () => request.resolve(created));
    await waitFor(() => expect(screen.getByTestId('draft-route')).not.toHaveTextContent('draftId=first'));
    await waitFor(() => expect(screen.getByRole('textbox')).toHaveValue('retained next task'));
    fireEvent.keyDown(screen.getByRole('textbox'), { key: 'Enter' });
    await waitFor(() => expect(api.startSession).toHaveBeenCalledTimes(2));
    expect(vi.mocked(api.startSession).mock.calls[1][0].prompt).toBe('retained next task');
  });

  it('shows a safe receipt retry when the draft state cannot be read', async () => {
    const read = vi.spyOn(IDBObjectStore.prototype, 'get').mockImplementation(() => { throw new DOMException('receipt read failed', 'UnknownError'); });
    try {
      render(<MemoryRouter initialEntries={['/session/new?dir=%2Frepo&draftId=first']}><DraftWorkspace /></MemoryRouter>);
      expect((await screen.findAllByRole('alert'))[0]).toHaveTextContent('receipt read failed');
      expect(await screen.findByText(/Draft selections not saved:/)).toBeInTheDocument();
      read.mockRestore();
      screen.getAllByRole('button', { name: 'Retry' }).forEach((button) => fireEvent.click(button));
      await waitFor(() => expect(screen.queryAllByRole('alert')).toHaveLength(0));
      // The draft metadata that could not be written while reads failed stays live.
      expect(await screen.findByRole('textbox')).toBeInTheDocument();
      expect(screen.getByTestId('draft-route')).toHaveTextContent('draftId=first');
      expect(api.startSession).not.toHaveBeenCalled();
    } finally { read.mockRestore(); }
  });
  it('canonicalizes a legacy bookmarked target and migrates its text without reusing the permanent new claim', async () => {
    saveDraft('new', 'legacy prompt');
    useNewConversationDrafts.setState({ starts: { new: { version: 0, text: '', sessionId: 'old-session' } } });
    render(<MemoryRouter initialEntries={['/session/new?dir=%2Fother']}><DraftWorkspace /></MemoryRouter>);
    await waitFor(() => expect(screen.getByTestId('draft-route')).toHaveTextContent('draftId='));
    expect(screen.getByTestId('draft-route')).not.toHaveTextContent('draftId=new');
    expect(screen.getByRole('textbox')).toHaveValue('legacy prompt');
    expect(getDraft('new')).toBe('');
    expect(useNewConversationDrafts.getState().drafts).toHaveLength(1);
    expect(api.startSession).not.toHaveBeenCalled();
  });

  it('keeps the only legacy text copy when its canonical-key migration hits quota', async () => {
    saveDraft('new', 'only legacy copy');
    const original = IDBObjectStore.prototype.put;
    const write = vi.spyOn(IDBObjectStore.prototype, 'put').mockImplementation(function (this: IDBObjectStore, value, key) {
      if (this.name === 'texts' && key !== 'new') throw new DOMException('quota', 'QuotaExceededError');
      return original.call(this, value, key);
    });
    try {
      render(<MemoryRouter initialEntries={['/session/new?dir=%2Frepo']}><DraftWorkspace /></MemoryRouter>);
      expect(await screen.findByRole('alert')).toHaveTextContent('draft');
      expect(getDraft('new')).toBe('only legacy copy');
      write.mockRestore();
      fireEvent.click(screen.getByRole('button', { name: 'Retry' }));
      await waitFor(() => expect(screen.getByRole('textbox')).toHaveValue('only legacy copy'));
      await waitFor(() => expect(getDraft('new')).toBe(''));
    } finally { write.mockRestore(); }
  });
  it('moves a mounted composer to a fresh identity after another tab discards it', async () => {
    render(<MemoryRouter initialEntries={['/session/new?dir=%2Frepo&draftId=first&title=First']}><DraftWorkspace /></MemoryRouter>);
    fireEvent.input(screen.getByRole('textbox'), { target: { value: 'old text' } });
    await waitFor(() => expect(useNewConversationDrafts.getState().drafts).toHaveLength(1));
    // Another tab discards it: this tab learns it from the database.
    const { peerForget } = await peerTab();
    await act(async () => { await peerForget('first'); });
    await waitFor(() => expect(screen.getByTestId('draft-route')).not.toHaveTextContent('draftId=first'));
    expect(screen.getByRole('textbox')).toHaveValue('');
    fireEvent.input(screen.getByRole('textbox'), { target: { value: 'new text' } });
    fireEvent.click(screen.getByRole('button', { name: 'Another draft' }));
    fireEvent.click(screen.getByRole('button', { name: /First/ }));
    expect(screen.getByRole('textbox')).toHaveValue('new text');
    expect(getDraft('first')).toBe('');
    expect(useNewConversationDrafts.getState().drafts.map((draft) => draft.draftId)).not.toContain('first');
  });

  it('keeps a pending draft locked across reopen and retires it after a background start', async () => {
    const request = deferred<typeof created>();
    vi.mocked(api.startSession).mockReturnValue(request.promise);
    render(<MemoryRouter initialEntries={['/session/new?dir=%2Frepo&draftId=first&title=First']}><DraftWorkspace /></MemoryRouter>);
    fireEvent.input(screen.getByRole('textbox'), { target: { value: 'start first' } });
    fireEvent.keyDown(screen.getByRole('textbox'), { key: 'Enter' });
    await waitFor(() => expect(api.startSession).toHaveBeenCalledTimes(1));
    fireEvent.click(screen.getByRole('button', { name: 'Another draft' }));
    fireEvent.input(screen.getByRole('textbox'), { target: { value: 'prepare second' } });
    fireEvent.click(screen.getByRole('button', { name: /First/ }));
    expect(screen.getByRole('textbox')).toBeDisabled();
    expect(screen.getByTestId('pending-prompt')).toHaveTextContent('start first');
    fireEvent.keyDown(screen.getByRole('textbox'), { key: 'Enter' });
    expect(api.startSession).toHaveBeenCalledTimes(1);
    fireEvent.click(screen.getByRole('button', { name: /Second/ }));
    await act(async () => request.resolve(created));
    await waitFor(() => expect(screen.queryByRole('button', { name: /First/ })).not.toBeInTheDocument());
    expect(screen.getByTestId('draft-route')).toHaveTextContent('draftId=second');
    expect(screen.getByRole('textbox')).toHaveValue('prepare second');
    expect(useNewConversationDrafts.getState().drafts.map((draft) => draft.draftId)).toEqual(['second']);
  });

  it('restores a reopened pending draft when its background start fails', async () => {
    const request = deferred<typeof created>();
    vi.mocked(api.startSession).mockReturnValue(request.promise);
    render(<MemoryRouter initialEntries={['/session/new?dir=%2Frepo&draftId=first&title=First']}><DraftWorkspace /></MemoryRouter>);
    fireEvent.input(screen.getByRole('textbox'), { target: { value: 'retry first' } });
    fireEvent.keyDown(screen.getByRole('textbox'), { key: 'Enter' });
    await waitFor(() => expect(api.startSession).toHaveBeenCalledTimes(1));
    fireEvent.click(screen.getByRole('button', { name: 'Another draft' }));
    fireEvent.click(screen.getByRole('button', { name: /First/ }));
    await act(async () => request.reject(new Error('start failed')));
    await waitFor(() => expect(screen.getByRole('textbox')).toHaveValue('retry first'));
    await waitFor(() => expect(screen.getByRole('textbox')).not.toBeDisabled());
    expect(screen.getByRole('textbox')).not.toBeDisabled();
    expect(screen.getByRole('alert')).toHaveTextContent('start failed');
  });
  it('does not restore immediately discarded text during unmount or a delayed start failure', async () => {
    const props = { params: { directory: '/repo', draftId: 'discarded' }, composerRef: null, whisperAvailable: false,
      navigate: vi.fn(), navigateToSession: vi.fn() };
    const view = render(<NewConversation {...props} />);
    fireEvent.input(screen.getByRole('textbox'), { target: { value: 'discard this text' } });
    await act(async () => forgetConversationDraft('discarded'));
    view.unmount();
    expect(getDraft('discarded')).toBe('');
    const request = deferred<typeof created>();
    vi.mocked(api.startSession).mockReturnValue(request.promise);
    const second = render(<NewConversation {...props} params={{ directory: '/repo', draftId: 'pending-discard' }} />);
    fireEvent.input(screen.getByRole('textbox'), { target: { value: 'discard this failed prompt' } });
    fireEvent.keyDown(screen.getByRole('textbox'), { key: 'Enter' });
    await waitFor(() => expect(api.startSession).toHaveBeenCalled());
    await act(async () => forgetConversationDraft('pending-discard'));
    second.unmount();
    await act(async () => request.reject(new Error('lost connection')));
    expect(getDraft('pending-discard')).toBe('');
  });
  it('keeps multiple unstarted sidebar conversations and their text independent', async () => {
    const first = newSessionPath({ directory: '/repo', title: 'First' });
    const second = newSessionPath({ directory: '/repo', title: 'Second' });
    function DraftWorkspace() {
      const location = useLocation();
      const navigate = useNavigate();
      const params = parseNewSessionParams(new URLSearchParams(location.search))!;
      return <>
        <button onClick={() => navigate(second)}>Prepare another</button>
        <SidebarConversationDrafts searchQuery="" />
        <NewConversation params={params} composerRef={null} whisperAvailable={false}
          navigate={navigate} navigateToSession={(id) => navigate(`/session/${id}`)} />
      </>;
    }
    render(<MemoryRouter initialEntries={[first]}><DraftWorkspace /></MemoryRouter>);
    fireEvent.input(screen.getByRole('textbox'), { target: { value: 'first prompt' } });
    fireEvent.click(screen.getByRole('button', { name: 'Prepare another' }));
    expect(screen.getByRole('textbox')).toHaveValue('');
    fireEvent.input(screen.getByRole('textbox'), { target: { value: 'second prompt' } });
    fireEvent.click(screen.getByRole('button', { name: /First/ }));
    expect(screen.getByRole('textbox')).toHaveValue('first prompt');
    fireEvent.click(screen.getByRole('button', { name: /Second/ }));
    expect(screen.getByRole('textbox')).toHaveValue('second prompt');
    expect(screen.getAllByRole('button', { name: 'Discard draft' })).toHaveLength(2);
    expect(api.startSession).not.toHaveBeenCalled();
    fireEvent.click(screen.getAllByRole('button', { name: 'Discard draft' })[0]);
    await waitFor(() => expect(screen.queryByRole('button', { name: /First/ })).not.toBeInTheDocument());
    expect(screen.getByRole('textbox')).toHaveValue('second prompt');
  });
  it('preserves an explicit remote owner with no platform and references its uploaded path', async () => {
    vi.mocked(api.prepareSession).mockResolvedValue({ ...prepared, platform: 'r-box:opencode' });
    vi.mocked(api.startSession).mockResolvedValue({ ...created, platform: 'r-box:opencode', remoteId: 'box' });
    vi.mocked(api.uploadComposerAttachment).mockResolvedValue({ path: '/box-cache/note.txt', name: 'note.txt', mime: 'text/plain', size: 4 });
    render(<Flow params={{ directory: '/repo', remoteId: 'box' }} />);
    const input = screen.getByRole('textbox');
    await waitFor(() => expect(input).not.toBeDisabled());
    expect(api.prepareSession).toHaveBeenCalledWith({ directory: '/repo', remoteId: 'box', platform: undefined }, expect.any(AbortSignal));
    const file = new File(['note'], 'note.txt', { type: 'text/plain' });
    fireEvent.drop(input, { dataTransfer: { files: [file] } });
    await screen.findByText('note.txt');
    fireEvent.input(input, { target: { value: 'read on the build box' } });
    fireEvent.keyDown(input, { key: 'Enter' });
    await waitFor(() => expect(api.sendMessage).toHaveBeenCalled());
    expect(api.startSession).toHaveBeenCalledWith(expect.objectContaining({ remoteId: 'box', platform: 'r-box:opencode', send: undefined }));
    expect(api.uploadComposerAttachment).toHaveBeenCalledWith('child', file, 'r-box:opencode');
    expect(api.sendMessage).toHaveBeenCalledWith('child', expect.stringContaining('/box-cache/note.txt'), undefined, '', 'build', undefined, 'r-box:opencode');
  });
  it('accepts input before the catalog loads and submits once it arrives', async () => {
    const catalog = deferred<typeof prepared & { defaultAgent: string; defaultModel: string }>();
    vi.mocked(api.prepareSession).mockReturnValue(catalog.promise);
    saveDraft('new', 'start in plan mode');
    render(<Flow />);
    await waitFor(() => expect(screen.getByRole('combobox', { name: 'Session target' })).toHaveTextContent('New worktree'));
    const input = screen.getByRole('textbox');
    expect(input).not.toBeDisabled();
    fireEvent.keyDown(input, { key: 'Enter' });
    await act(async () => {});
    expect(api.startSession).not.toHaveBeenCalled();
    await act(async () => catalog.resolve({ ...prepared, defaultAgent: 'plan', defaultModel: 'p/model' }));
    await waitFor(() => expect(api.startSession).toHaveBeenCalledWith(expect.objectContaining({
      send: expect.objectContaining({ message: 'start in plan mode', agent: 'build', model: 'p/model' }),
    })));
    expect(api.startSession).toHaveBeenCalledTimes(1);
  });

  it('applies the catalog defaults to a file sent before the catalog loads', async () => {
    const catalog = deferred<typeof prepared & { defaultAgent: string; defaultModel: string }>();
    vi.mocked(api.prepareSession).mockReturnValue(catalog.promise);
    vi.mocked(api.uploadComposerAttachment).mockResolvedValue({ path: '/child/note.txt', name: 'note.txt', mime: 'text/plain', size: 4 });
    render(<Flow />);
    const input = screen.getByRole('textbox');
    fireEvent.drop(input, { dataTransfer: { files: [new File(['note'], 'note.txt', { type: 'text/plain' })] } });
    await screen.findByText('note.txt');
    fireEvent.input(input, { target: { value: 'read early' } });
    fireEvent.keyDown(input, { key: 'Enter' });
    await act(async () => catalog.resolve({ ...prepared, defaultAgent: 'plan', defaultModel: 'p/model' }));
    await waitFor(() => expect(api.sendMessage).toHaveBeenCalled());
    const [, , , model, agent] = vi.mocked(api.sendMessage).mock.calls[0];
    expect({ model, agent }).toEqual({ model: 'p/model', agent: 'build' });
  });

  it('restores the draft when the page is left while a submission waits', async () => {
    vi.mocked(api.prepareSession).mockReturnValue(new Promise(() => {}));
    render(<Flow />);
    const input = screen.getByRole('textbox');
    fireEvent.input(input, { target: { value: 'do not lose me' } });
    fireEvent.keyDown(input, { key: 'Enter' });
    fireEvent.click(screen.getByText('Leave draft'));
    await waitFor(() => expect(getDraft('new')).toBe('do not lose me'));
    expect(api.startSession).not.toHaveBeenCalled();
  });

  it('fails a waiting submission on a prepare error and keeps the draft for retry', async () => {
    const catalog = deferred<typeof prepared>();
    vi.mocked(api.prepareSession).mockReturnValueOnce(catalog.promise);
    saveDraft('new', 'keep my draft');
    render(<Flow />);
    const input = screen.getByRole('textbox');
    expect(input).not.toBeDisabled();
    fireEvent.keyDown(input, { key: 'Enter' });
    await act(async () => catalog.reject(new Error('prepare offline')));
    expect((await screen.findAllByRole('alert'))[0]).toHaveTextContent('prepare offline');
    await waitFor(() => expect(input).toHaveValue('keep my draft'));
    expect(api.startSession).not.toHaveBeenCalled();
    fireEvent.click(screen.getAllByRole('button', { name: 'Retry' })[0]);
    await waitFor(() => expect(api.prepareSession).toHaveBeenCalledTimes(2));
    // The failed attempt's receipt is stored before the composer accepts a retry.
    await screen.findByRole('button', { name: 'Send message' });
    fireEvent.keyDown(input, { key: 'Enter' });
    await waitFor(() => expect(api.startSession).toHaveBeenCalledTimes(1));
  });

  it('does not let a follow-up overtake a pending first upload', async () => {
    const upload = deferred<Awaited<ReturnType<typeof api.uploadComposerAttachment>>>();
    vi.mocked(api.uploadComposerAttachment).mockReturnValueOnce(upload.promise);
    render(<Flow />);
    const input = screen.getByRole('textbox');
    await waitFor(() => expect(input).not.toBeDisabled());
    fireEvent.drop(input, { dataTransfer: { files: [new File(['note'], 'note.txt', { type: 'text/plain' })] } });
    await screen.findByText('note.txt');
    fireEvent.input(input, { target: { value: 'first prompt' } });
    fireEvent.keyDown(input, { key: 'Enter' });
    await waitFor(() => expect(screen.getByTestId('route')).toHaveTextContent('child'));
    const childInput = screen.getByRole('textbox');
    expect(childInput).toBeDisabled();
    fireEvent.input(childInput, { target: { value: 'follow-up' } });
    fireEvent.keyDown(childInput, { key: 'Enter' });
    expect(api.sendMessage).not.toHaveBeenCalled();
    await act(async () => upload.resolve({ path: '/child/note.txt', name: 'note.txt', mime: 'text/plain', size: 4 }));
    await waitFor(() => expect(childInput).not.toBeDisabled());
    expect(api.sendMessage).toHaveBeenCalledTimes(1);
    expect(vi.mocked(api.sendMessage).mock.calls[0][1]).toContain('first prompt');
    fireEvent.keyDown(childInput, { key: 'Enter' });
    await waitFor(() => expect(api.sendMessage).toHaveBeenCalledTimes(2));
    expect(vi.mocked(api.sendMessage).mock.calls[1][1]).toBe('follow-up');
  });

  it('does not automatically replay an uncertain creation', async () => {
    vi.mocked(api.startSession).mockRejectedValue(new BackendUnavailableError());
    render(<Flow />);
    const input = screen.getByRole('textbox');
    await waitFor(() => expect(input).not.toBeDisabled());
    fireEvent.input(input, { target: { value: 'create once' } });
    fireEvent.keyDown(input, { key: 'Enter' });
    await screen.findByRole('alert');
    await waitFor(() => expect(input).not.toBeDisabled());
    expect(api.startSession).toHaveBeenCalledTimes(1);
    expect(input).toHaveValue('create once');
  });

  it('keeps non-image files until the new session exists, then uploads and sends', async () => {
    const launch = deferred<typeof created>();
    vi.mocked(api.startSession).mockReturnValue(launch.promise);
    vi.mocked(api.uploadComposerAttachment).mockResolvedValue({ path: '/child/note.txt', name: 'note.txt', mime: 'text/plain', size: 4 });
    render(<Flow />);
    const input = screen.getByRole('textbox');
    await waitFor(() => expect(input).not.toBeDisabled());
    const file = new File(['note'], 'note.txt', { type: 'text/plain' });
    fireEvent.drop(input, { dataTransfer: { files: [file] } });
    await screen.findByText('note.txt');
    expect(api.uploadComposerAttachment).not.toHaveBeenCalled();
    fireEvent.input(input, { target: { value: 'read the note' } });
    fireEvent.keyDown(input, { key: 'Enter' });
    await waitFor(() => expect(api.startSession).toHaveBeenCalledTimes(1));
    expect(vi.mocked(api.startSession).mock.calls[0][0].send).toBeUndefined();
    await act(async () => launch.resolve(created));
    await waitFor(() => expect(api.uploadComposerAttachment).toHaveBeenCalledWith('child', file, 'opencode'));
    await waitFor(() => expect(api.sendMessage).toHaveBeenCalledWith('child', expect.stringContaining('/child/note.txt'), undefined,
      '', 'build', undefined, 'opencode'));
  });

  it('retains an image-only failed send and its exact selections before navigation', async () => {
    vi.mocked(api.prepareSession).mockResolvedValue({ ...prepared, defaultAgent: 'plan', defaultModel: 'p/m' });
    vi.mocked(api.startSession).mockResolvedValue({ ...created, firstMessageSent: false, firstMessageError: 'upstream failed' });
    render(<Flow />);
    const input = screen.getByRole('textbox');
    await waitFor(() => expect(input).not.toBeDisabled());
    fireEvent.drop(input, { dataTransfer: { files: [new File(['image'], 'pic.png', { type: 'image/png' })] } });
    await screen.findByAltText('Attachment 1');
    fireEvent.keyDown(input, { key: 'Enter' });
    await waitFor(() => expect(screen.getByTestId('route')).toHaveTextContent('child'));
    expect(listFailedSends('child')).toEqual([expect.objectContaining({ text: '', error: 'upstream failed', model: 'p/m', agent: 'build',
      images: [expect.objectContaining({ mime: 'image/png', url: expect.stringContaining('data:image/png') })] })]);
    expect(await screen.findByRole('alert')).toHaveTextContent('upstream failed');
    fireEvent.click(screen.getByRole('button', { name: 'Retry message' }));
    await waitFor(() => expect(api.sendMessage).toHaveBeenCalledWith('child', '',
      [expect.objectContaining({ mime: 'image/png' })], 'p/m', 'build', undefined, 'opencode', undefined));
    await waitFor(() => expect(listFailedSends('child')).toEqual([]));
  });

  it('does not navigate or erase a newer draft when an old start finishes', async () => {
    const launch = deferred<typeof created>();
    vi.mocked(api.startSession).mockReturnValue(launch.promise);
    render(<Flow />);
    const input = screen.getByRole('textbox');
    await waitFor(() => expect(input).not.toBeDisabled());
    fireEvent.input(input, { target: { value: 'old task' } });
    fireEvent.keyDown(input, { key: 'Enter' });
    await waitFor(() => expect(api.startSession).toHaveBeenCalledTimes(1));
    fireEvent.click(screen.getByText('Leave draft'));
    const newer = screen.getByRole('textbox');
    fireEvent.input(newer, { target: { value: 'newer task' } });
    saveDraft('new', 'newer task');
    await act(async () => launch.resolve(created));
    await waitFor(() => expect(useNewConversationDrafts.getState().starts.new?.replacementDraftId).toBeTruthy());
    expect(screen.getByTestId('route')).toHaveTextContent('other');
    expect(newer).toHaveValue('newer task');
    expect(getDraft(useNewConversationDrafts.getState().starts.new.replacementDraftId!)).toBe('newer task');
  });

  it('does not navigate to the created session when the draft is re-pointed during creation', async () => {
    const launch = deferred<typeof created>();
    vi.mocked(api.startSession).mockReturnValue(launch.promise);
    const navigate = vi.fn();
    const props = { params: { directory: '/repo', platform: 'opencode', title: 'old', draftId: 'new' }, composerRef: null,
      whisperAvailable: false, navigate, navigateToSession: vi.fn() };
    const view = render(<NewConversation {...props} />);
    const input = screen.getByRole('textbox');
    await waitFor(() => expect(input).not.toBeDisabled());
    fireEvent.input(input, { target: { value: 'old task' } });
    fireEvent.keyDown(input, { key: 'Enter' });
    view.rerender(<NewConversation {...props} params={{ ...props.params, title: 'new' }} />);
    saveDraft('new', 'new task');
    await act(async () => launch.resolve(created));
    await waitFor(() => expect(useNewConversationDrafts.getState().starts.new?.replacementDraftId).toBeTruthy());
    expect(navigate).not.toHaveBeenCalledWith('/session/child', { replace: true });
    expect(getDraft(useNewConversationDrafts.getState().starts.new.replacementDraftId!)).toBe('new task');
  });

  it('accepts an independent draft while the previous draft is still starting', async () => {
    const oldStart = deferred<typeof created>();
    const newStart = deferred<typeof created>();
    vi.mocked(api.startSession).mockReturnValueOnce(oldStart.promise).mockReturnValueOnce(newStart.promise);
    const navigate = vi.fn();
    const props = { params: { directory: '/repo', platform: 'opencode', title: 'old', draftId: 'new' }, composerRef: null,
      whisperAvailable: false, navigate, navigateToSession: vi.fn() };
    const view = render(<NewConversation {...props} />);
    const oldInput = screen.getByRole('textbox');
    await waitFor(() => expect(screen.getByRole('combobox', { name: 'Session target' })).toHaveTextContent('New worktree'));
    await act(async () => {});
    fireEvent.input(oldInput, { target: { value: 'old prompt' } });
    fireEvent.keyDown(oldInput, { key: 'Enter' });
    view.rerender(<NewConversation {...props} params={{ ...props.params, title: 'new', draftId: 'second' }} />);
    const newInput = screen.getByRole('textbox');
    fireEvent.input(newInput, { target: { value: 'new prompt' } });
    fireEvent.keyDown(newInput, { key: 'Enter' });
    // Claims are IndexedDB transactions: wait for both requests before settling them.
    await waitFor(() => expect(api.startSession).toHaveBeenCalledTimes(2));
    await act(async () => {
      oldStart.resolve({ ...created, sessionId: 'old-child' });
      newStart.resolve({ ...created, sessionId: 'new-child' });
    });
    expect(api.startSession).toHaveBeenCalledTimes(2);
    expect(vi.mocked(api.startSession).mock.calls[1][0]).toMatchObject({ title: 'new', send: { message: 'new prompt' } });
    await waitFor(() => expect(navigate).toHaveBeenCalled());
    expect(navigate).toHaveBeenCalledExactlyOnceWith('/session/new-child', { replace: true });
  });

  it('keeps a failed first prompt visible and retryable when localStorage is full', async () => {
    vi.mocked(api.startSession).mockResolvedValue({ ...created, firstMessageSent: false, firstMessageError: 'upstream failed' });
    render(<Flow />);
    const input = screen.getByRole('textbox');
    await waitFor(() => expect(input).not.toBeDisabled());
    fireEvent.input(input, { target: { value: 'unsent prompt' } });
    const write = vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => { throw new DOMException('full', 'QuotaExceededError'); });
    try {
      fireEvent.keyDown(input, { key: 'Enter' });
      expect(await screen.findByRole('alert')).toHaveTextContent('upstream failed');
      fireEvent.click(screen.getByRole('button', { name: 'Retry message' }));
      await waitFor(() => expect(api.sendMessage).toHaveBeenCalledWith('child', 'unsent prompt', undefined,
        '', 'build', undefined, 'opencode', undefined));
      await waitFor(() => expect(listFailedSends('child')).toEqual([]));
    } finally {
      write.mockRestore();
    }
  });

  it('retains large image-only failures in memory while capping the persisted copy', async () => {
    vi.mocked(api.startSession).mockResolvedValue({ ...created, firstMessageSent: false, firstMessageError: 'upstream failed' });
    render(<Flow />);
    const input = screen.getByRole('textbox');
    await waitFor(() => expect(input).not.toBeDisabled());
    const image = new File([new Uint8Array(3 * 1024 * 1024 + 64 * 1024)], 'large.png', { type: 'image/png' });
    fireEvent.drop(input, { dataTransfer: { files: [image] } });
    await screen.findByAltText('Attachment 1');
    fireEvent.keyDown(input, { key: 'Enter' });
    expect(await screen.findByRole('alert')).toHaveTextContent('upstream failed');
    expect(listFailedSends('child')[0].images?.[0].url.length).toBeGreaterThan(4 * 1024 * 1024);
    const persisted = JSON.parse(window.localStorage.getItem('ocman.failedSends.v1')!);
    expect(persisted.child[0].imagesDropped).toBe(true);
    expect(persisted.child[0].images).toBeUndefined();
    fireEvent.click(screen.getByRole('button', { name: 'Retry message' }));
    await waitFor(() => expect(api.sendMessage).toHaveBeenCalled());
    expect(vi.mocked(api.sendMessage).mock.calls[0][2]?.[0].url.length).toBeGreaterThan(4 * 1024 * 1024);
  });

  it('retains successful uploads and retries a failed delivery on the same child', async () => {
    const send = deferred<void>();
    vi.mocked(api.uploadComposerAttachment).mockResolvedValue({ path: '/child/note.txt', name: 'note.txt', mime: 'text/plain', size: 4 });
    vi.mocked(api.sendMessage).mockReturnValueOnce(send.promise);
    render(<Flow />);
    const input = screen.getByRole('textbox');
    await waitFor(() => expect(input).not.toBeDisabled());
    fireEvent.drop(input, { dataTransfer: { files: [new File(['note'], 'note.txt', { type: 'text/plain' })] } });
    await screen.findByText('note.txt');
    fireEvent.keyDown(input, { key: 'Enter' });
    await waitFor(() => expect(api.sendMessage).toHaveBeenCalledTimes(1));
    // The child mounts once completion commits, which can trail the delivery's start.
    expect(await screen.findByText('Sending first submission…')).toHaveAttribute('role', 'status');
    await act(async () => send.reject(new Error('delivery failed')));
    expect(await screen.findByRole('alert')).toHaveTextContent('delivery failed');
    fireEvent.click(screen.getByRole('button', { name: 'Retry' }));
    await waitFor(() => expect(api.sendMessage).toHaveBeenCalledTimes(2));
    await waitFor(() => expect(screen.queryByRole('alert')).not.toBeInTheDocument());
    expect(api.startSession).toHaveBeenCalledTimes(1);
    expect(api.uploadComposerAttachment).toHaveBeenCalledTimes(1);
  });

  it('keeps a failed file upload retryable after the child mounts', async () => {
    vi.mocked(api.uploadComposerAttachment).mockRejectedValueOnce(new Error('upload failed'))
      .mockResolvedValue({ path: '/child/note.txt', name: 'note.txt', mime: 'text/plain', size: 4 });
    render(<Flow />);
    const input = screen.getByRole('textbox');
    await waitFor(() => expect(input).not.toBeDisabled());
    const file = new File(['note'], 'note.txt', { type: 'text/plain' });
    fireEvent.drop(input, { dataTransfer: { files: [file] } });
    await screen.findByText('note.txt');
    fireEvent.keyDown(input, { key: 'Enter' });
    expect(await screen.findByRole('alert')).toHaveTextContent('upload failed');
    expect(screen.getByTestId('route')).toHaveTextContent('child');
    expect(api.sendMessage).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole('button', { name: 'Retry' }));
    await waitFor(() => expect(api.sendMessage).toHaveBeenCalled());
    expect(api.startSession).toHaveBeenCalledTimes(1);
    expect(api.uploadComposerAttachment).toHaveBeenLastCalledWith('child', file, 'opencode');
  });

  it('shows a delayed shell failure on the child and retries without overwriting its draft', async () => {
    const command = deferred<void>();
    vi.mocked(postJSON).mockReturnValueOnce(command.promise);
    render(<Flow />);
    const input = screen.getByRole('textbox');
    await waitFor(() => expect(input).not.toBeDisabled());
    fireEvent.input(input, { target: { value: '!echo original' } });
    fireEvent.keyDown(input, { key: 'Enter' });
    await waitFor(() => expect(screen.getByTestId('route')).toHaveTextContent('child'));
    const childInput = screen.getByRole('textbox');
    fireEvent.input(childInput, { target: { value: 'new follow-up' } });
    await act(async () => command.reject(new Error('shell failed')));
    expect(await screen.findByRole('alert')).toHaveTextContent('shell failed');
    expect(childInput).toHaveValue('new follow-up');
    fireEvent.click(screen.getByRole('button', { name: 'Retry' }));
    await waitFor(() => expect(postJSON).toHaveBeenCalledTimes(2));
    await waitFor(() => expect(screen.queryByRole('alert')).not.toBeInTheDocument());
    expect(childInput).toHaveValue('new follow-up');
  });

  it('waits for the configured model instead of latching the stored pick', async () => {
    saveProjectModel('/repo', 'p/stored');
    const catalog = deferred<typeof prepared & { projectDefaultModel: string }>();
    vi.mocked(api.prepareSession).mockReturnValue(catalog.promise);
    render(<Flow />);
    await act(async () => catalog.resolve({ ...prepared, projectDefaultModel: 'p/configured' }));
    const input = screen.getByRole('textbox');
    await waitFor(() => expect(input).not.toBeDisabled());
    fireEvent.input(input, { target: { value: 'use configured model' } });
    fireEvent.keyDown(input, { key: 'Enter' });
    await waitFor(() => expect(api.startSession).toHaveBeenCalled());
    expect(vi.mocked(api.startSession).mock.calls[0][0].send?.model).toBe('p/configured');
  });
});

// @vitest-environment jsdom
import { useRef, useState } from 'react';
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { BackendUnavailableError, api } from '../../lib/api';
import { clearDraft, getDraft, saveDraft } from '../../lib/composerDraft';
import { clearFailedSends, listFailedSends } from '../../lib/failedSends';
import { saveProjectModel } from '../../lib/projectModel';
import { useApiStore } from '../../lib/apiStore';
import { Composer } from '../../components/assistant/Composer';
import { SessionComposerSlot } from './SessionComposerSlot';
import { NewConversation } from './NewConversation';
import { useFailedSendRehydrate } from './useFailedSendRehydrate';
import { usePendingSend } from './usePendingSend';
import { useSessionActions, type UseSessionActionsOptions } from './useSessionActions';
import { useFirstSubmission } from './firstSubmission';
import type { NewSessionParams } from '../../lib/newSessionPath';

vi.mock('../../lib/useCapabilities', () => ({
  useOpencodeLaunch: () => true,
  usePlatformCapabilities: () => ({ shellExec: true }),
}));
vi.mock('../../components/FactoryPlanApproval', () => ({ FactoryPlanApproval: () => null }));
vi.mock('../../lib/remoteLog', () => ({ remoteLog: { error: vi.fn() } }));
vi.mock('../../lib/api', async (original) => {
  const actual = await original<typeof import('../../lib/api')>();
  return {
    ...actual,
    fetchJSON: vi.fn(async (url: string) => url.startsWith('/api/worktree/list')
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
    firstUnreadMessageId={null} unreadMessageCount={0} onJumpToUnread={() => {}} permission={null} question={null}
    composer={{ sessionId: 'child', isRunning: false, onSend: actions.handleSend }} />
  </>;
}

function Flow({ params = { directory: '/repo', platform: 'opencode' } }: { params?: NewSessionParams } = {}) {
  const [route, setRoute] = useState('new');
  return <>
    <output data-testid="route">{route}</output>
    <button onClick={() => setRoute('other')}>Leave draft</button>
    {route === 'new' ? <NewConversation params={params} composerRef={null}
      whisperAvailable={false} navigate={setRoute} navigateToSession={setRoute} /> : route === 'child' ? <Child />
      : <Composer draftKey="new" isRunning={false} />}
  </>;
}

beforeEach(() => {
  vi.clearAllMocks();
  useFirstSubmission.setState({ entries: {} });
  window.localStorage.clear();
  clearFailedSends('child');
  clearDraft('new');
  vi.spyOn(useApiStore.getState(), 'seedNewSession').mockImplementation(() => {});
  vi.mocked(api.prepareSession).mockResolvedValue(prepared);
  vi.mocked(api.startSession).mockReset().mockResolvedValue(created);
  vi.mocked(api.sendMessage).mockResolvedValue(undefined);
  vi.mocked(postJSON).mockResolvedValue(undefined);
});

describe('new-conversation submission lifecycle', () => {
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
    expect(api.sendMessage).toHaveBeenCalledWith('child', expect.stringContaining('/box-cache/note.txt'), undefined, '', undefined, undefined, 'r-box:opencode');
  });
  it('waits for the catalog even when workspace eligibility resolves first', async () => {
    const catalog = deferred<typeof prepared & { defaultAgent: string; defaultModel: string }>();
    vi.mocked(api.prepareSession).mockReturnValue(catalog.promise);
    saveDraft('new', 'start in plan mode');
    render(<Flow />);
    await waitFor(() => expect(screen.getByRole('combobox', { name: 'Session target' })).toHaveTextContent('New worktree'));
    const input = screen.getByRole('textbox');
    expect(input).toBeDisabled();
    fireEvent.keyDown(input, { key: 'Enter' });
    expect(api.startSession).not.toHaveBeenCalled();
    await act(async () => catalog.resolve({ ...prepared, defaultAgent: 'plan', defaultModel: 'p/model' }));
    await waitFor(() => expect(input).not.toBeDisabled());
    fireEvent.keyDown(input, { key: 'Enter' });
    await waitFor(() => expect(api.startSession).toHaveBeenCalledWith(expect.objectContaining({
      send: expect.objectContaining({ message: 'start in plan mode', agent: 'plan', model: 'p/model' }),
    })));
  });

  it('offers a prepare retry while leaving the draft intact and submission disabled', async () => {
    vi.mocked(api.prepareSession).mockRejectedValueOnce(new Error('prepare offline'));
    saveDraft('new', 'keep my draft');
    render(<Flow />);
    expect(await screen.findByRole('alert')).toHaveTextContent('prepare offline');
    const input = screen.getByRole('textbox');
    expect(input).toBeDisabled();
    expect(input).toHaveValue('keep my draft');
    fireEvent.click(screen.getByRole('button', { name: 'Retry' }));
    await waitFor(() => expect(input).not.toBeDisabled());
    expect(input).toHaveValue('keep my draft');
    expect(api.prepareSession).toHaveBeenCalledTimes(2);
    expect(api.startSession).not.toHaveBeenCalled();
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
      '', undefined, undefined, 'opencode'));
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
    expect(listFailedSends('child')).toEqual([expect.objectContaining({ text: '', error: 'upstream failed', model: 'p/m', agent: 'plan',
      images: [expect.objectContaining({ mime: 'image/png', url: expect.stringContaining('data:image/png') })] })]);
    expect(await screen.findByRole('alert')).toHaveTextContent('upstream failed');
    fireEvent.click(screen.getByRole('button', { name: 'Retry message' }));
    await waitFor(() => expect(api.sendMessage).toHaveBeenCalledWith('child', '',
      [expect.objectContaining({ mime: 'image/png' })], 'p/m', 'plan', undefined, 'opencode', undefined));
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
    fireEvent.click(screen.getByText('Leave draft'));
    const newer = screen.getByRole('textbox');
    fireEvent.input(newer, { target: { value: 'newer task' } });
    saveDraft('new', 'newer task');
    await act(async () => launch.resolve(created));
    expect(screen.getByTestId('route')).toHaveTextContent('other');
    expect(newer).toHaveValue('newer task');
    expect(getDraft('new')).toBe('newer task');
  });

  it('does not navigate when the same draft component is re-pointed during creation', async () => {
    const launch = deferred<typeof created>();
    vi.mocked(api.startSession).mockReturnValue(launch.promise);
    const navigate = vi.fn();
    const props = { params: { directory: '/repo', platform: 'opencode', title: 'old' }, composerRef: null,
      whisperAvailable: false, navigate: vi.fn(), navigateToSession: navigate };
    const view = render(<NewConversation {...props} />);
    const input = screen.getByRole('textbox');
    await waitFor(() => expect(input).not.toBeDisabled());
    fireEvent.input(input, { target: { value: 'old task' } });
    fireEvent.keyDown(input, { key: 'Enter' });
    view.rerender(<NewConversation {...props} params={{ ...props.params, title: 'new' }} />);
    saveDraft('new', 'new task');
    await act(async () => launch.resolve(created));
    expect(navigate).not.toHaveBeenCalled();
    expect(getDraft('new')).toBe('new task');
  });

  it('accepts a re-pointed draft while the previous generation is still starting', async () => {
    const oldStart = deferred<typeof created>();
    const newStart = deferred<typeof created>();
    vi.mocked(api.startSession).mockReturnValueOnce(oldStart.promise).mockReturnValueOnce(newStart.promise);
    const navigate = vi.fn();
    const props = { params: { directory: '/repo', platform: 'opencode', title: 'old' }, composerRef: null,
      whisperAvailable: false, navigate: vi.fn(), navigateToSession: navigate };
    const view = render(<NewConversation {...props} />);
    const oldInput = screen.getByRole('textbox');
    await waitFor(() => expect(oldInput).not.toBeDisabled());
    fireEvent.input(oldInput, { target: { value: 'old prompt' } });
    fireEvent.keyDown(oldInput, { key: 'Enter' });
    view.rerender(<NewConversation {...props} params={{ ...props.params, title: 'new' }} />);
    const newInput = screen.getByRole('textbox');
    fireEvent.input(newInput, { target: { value: 'new prompt' } });
    fireEvent.keyDown(newInput, { key: 'Enter' });
    await act(async () => {
      oldStart.resolve({ ...created, sessionId: 'old-child' });
      newStart.resolve({ ...created, sessionId: 'new-child' });
    });
    expect(api.startSession).toHaveBeenCalledTimes(2);
    expect(vi.mocked(api.startSession).mock.calls[1][0]).toMatchObject({ title: 'new', send: { message: 'new prompt' } });
    expect(navigate).toHaveBeenCalledExactlyOnceWith('new-child');
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
        '', undefined, undefined, 'opencode', undefined));
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
    expect(screen.getByText('Sending first submission…')).toHaveAttribute('role', 'status');
    await act(async () => send.reject(new Error('delivery failed')));
    expect(await screen.findByRole('alert')).toHaveTextContent('delivery failed');
    fireEvent.click(screen.getByRole('button', { name: 'Retry' }));
    await waitFor(() => expect(screen.queryByRole('alert')).not.toBeInTheDocument());
    expect(api.startSession).toHaveBeenCalledTimes(1);
    expect(api.uploadComposerAttachment).toHaveBeenCalledTimes(1);
    expect(api.sendMessage).toHaveBeenCalledTimes(2);
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
    await waitFor(() => expect(screen.queryByRole('alert')).not.toBeInTheDocument());
    expect(postJSON).toHaveBeenCalledTimes(2);
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

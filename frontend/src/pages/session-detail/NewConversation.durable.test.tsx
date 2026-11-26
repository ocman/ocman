// @vitest-environment jsdom
import { act, fireEvent, render, renderHook, screen, waitFor } from '@testing-library/react';
import { StrictMode } from 'react';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { NewConversation } from './NewConversation';
import { api, fetchJSON } from '../../lib/api';
import { persistBackendDraft, subscribeBackendDraftSync, useBackendConversationDrafts, type BackendConversationDraft } from '../../lib/backendConversationDrafts';
import { getConversationDraft, useNewConversationDrafts } from '../../lib/newConversationDrafts';
import { resetDraftTextsForTests } from '../../lib/composerDraft';
import { clearSettingsCache } from '../../lib/projectSettingsCache';
import { useApiStore } from '../../lib/apiStore';
import { startModels } from './startHandoffs';
import { useComposerModel } from './useComposerModel';
import { saveSessionModel } from '../../lib/sessionModel';
import type { SessionMetadata } from '../../lib/sessionReducer';

vi.mock('../../lib/useCapabilities', () => ({
  useCapabilities: () => ({ platforms: [] }), useMultiPlatform: () => false, useMultiHost: () => false,
  useWorktreeLaunch: () => true, usePlatformCapabilities: () => ({ shellExec: true }), useMachinePlatforms: () => [],
}));
vi.mock('../../lib/remoteLog', () => ({ remoteLog: { error: vi.fn(), warn: vi.fn() } }));
vi.mock('../../lib/api', async (original) => {
  const actual = await original<typeof import('../../lib/api')>();
  return { ...actual, fetchJSON: vi.fn(), api: { ...actual.api, prepareSession: vi.fn(), startSession: vi.fn() } };
});

const id = 'ocm_01234567-89ab-4cde-8fab-0123456789ab';
let record: BackendConversationDraft;
let release: (response: Response) => void;
let payload: unknown;
const queued: unknown[] = [];
const json = (value: unknown, status = 200) => new Response(JSON.stringify(value), { status, headers: { 'Content-Type': 'application/json' } });

beforeEach(() => {
  HTMLElement.prototype.scrollIntoView = vi.fn();
  window.localStorage.clear();
  clearSettingsCache();
  resetDraftTextsForTests();
  useNewConversationDrafts.setState({ drafts: [], starts: {} });
  useBackendConversationDrafts.setState({ drafts: {}, conflicts: {}, saveErrors: {}, recovered: {}, recoveryTargets: {} });
  queued.length = 0;
  payload = undefined;
  record = { id, metadata: { draftId: id, directory: '/repo', platform: 'opencode', remoteId: 'local' }, phase: 'draft', steps: {}, createdAt: Date.now() };
  vi.mocked(fetchJSON).mockImplementation(async (url) => (url.startsWith('/api/project/settings') ? { models: [], defaults: { worktree: 'current' } }
    : url.startsWith('/api/worktree/default-base-ref') ? { baseRef: 'main' }
      : url.startsWith('/api/worktree/list') ? { worktrees: [{ path: '/repo', main: true }] } : { '/repo': { branch: 'main' } }) as never);
  vi.mocked(api.prepareSession).mockResolvedValue({ platform: 'opencode', defaultAgent: 'build', agents: [], commands: [], models: { hasProviders: true, models: [] }, liveConnection: true });
  vi.spyOn(useApiStore.getState(), 'seedNewSession').mockImplementation(() => {});
  vi.stubGlobal('fetch', vi.fn(async (url: string, options?: RequestInit) => {
    if (url.endsWith('/queue')) return json(queued);
    if (url.endsWith('/messages')) {
      const body = JSON.parse(String(options?.body));
      queued.push({ id: `q${queued.length}`, text: body.message, hasImages: false });
      return new Response(null, { status: 204 });
    }
    if (url.endsWith('/start')) {
      payload = JSON.parse(String(options?.body));
      return new Promise<Response>((resolve) => { release = resolve; });
    }
    if (options?.method === 'PUT') {
      record = { ...record, metadata: JSON.parse(String(options.body)) };
      return new Response(null, { status: 204 });
    }
    return json(url === '/api/session-drafts' ? [record] : record);
  }));
});
afterEach(() => { startModels.clear(); vi.unstubAllGlobals(); vi.restoreAllMocks(); });

it.each([false, true])('restores the admitted model as the draft becomes a session (later pick: %s)', async (laterPick) => {
  const platform = 'cnx_work';
  const directory = '/repo';
  record = { ...record, phase: 'ready', request: { directory, platform, send: { message: 'first', model: `${platform}:openai/gpt-6.2-sol` } },
    result: { sessionId: id, directory, platform, remoteId: 'local', firstMessageSent: true, firstMessageError: '' } };
  if (laterPick) saveSessionModel(id, 'openai/later-pick');
  else {
    const setItem = Storage.prototype.setItem;
    vi.spyOn(Storage.prototype, 'setItem').mockImplementation(function (this: Storage, key: string, value: string) {
      if (key === 'ocman.sessionModels.v1') throw new DOMException('Storage is full', 'QuotaExceededError');
      setItem.call(this, key, value);
    });
  }
  const navigate = vi.fn();
  const draft = render(<NewConversation params={record.metadata} composerRef={null} navigate={navigate} navigateToSession={vi.fn()} />);
  await waitFor(() => expect(navigate).toHaveBeenCalledWith(`/session/${id}`, { replace: true }));
  draft.unmount();

  const setSelectedModel = vi.fn();
  const session = { id, directory, platform, defaultModel: 'openai/gpt-6.1-sol' } as SessionMetadata;
  renderHook(() => useComposerModel({ id, session, messages: [], parts: [], modelOptions: [], agents: [],
    setSelectedModel, setSelectedAgent: vi.fn(), setSelectedReasoning: vi.fn() }), { wrapper: StrictMode });
  expect(setSelectedModel).toHaveBeenLastCalledWith(laterPick ? 'openai/later-pick' : 'openai/gpt-6.2-sol');
});

it('shows the saved message after a failed setup even when only send.message was persisted', async () => {
  record = { ...record, phase: 'failed', error: 'Session setup failed. git worktree add: context deadline exceeded',
    request: { directory: '/repo', platform: 'opencode', send: { message: 'do not lose my investigation' } } };
  render(<NewConversation params={record.metadata} composerRef={null} navigate={vi.fn()} navigateToSession={vi.fn()} />);
  expect(await screen.findByTestId('pending-prompt')).toHaveTextContent('do not lose my investigation');
});

it('offers retry for failed setup without a native session and preserves the accepted payload', async () => {
  record = { ...record, phase: 'failed', setupRetryable: true, error: 'Session setup failed. checkout timed out',
    request: { directory: '/repo', platform: 'opencode', prompt: 'saved investigation', send: { message: 'saved investigation' } } };
  const request = record.request;
  const fetcher = vi.mocked(fetch);
  const original = fetcher.getMockImplementation()!;
  let finish!: (response: Response) => void;
  fetcher.mockImplementation(async (url, options) => {
    if (String(url).endsWith('/retry-setup')) return new Promise<Response>((resolve) => { finish = resolve; });
    return original(url, options);
  });
  render(<NewConversation params={record.metadata} composerRef={null} navigate={vi.fn()} navigateToSession={vi.fn()} />);
  const retry = await screen.findByRole('button', { name: 'Retry' });
  expect(screen.getByTestId('pending-prompt')).toHaveTextContent('saved investigation');
  fireEvent.click(retry);
  await waitFor(() => expect(retry).toBeDisabled());
  record = { ...record, phase: 'accepted', error: '', setupRetryable: false };
  await act(async () => finish(new Response(null, { status: 202 })));
  await waitFor(() => expect(fetch).toHaveBeenCalledWith(`/api/session-drafts/${id}/retry-setup`, expect.objectContaining({ method: 'POST' })));
  expect(useBackendConversationDrafts.getState().drafts[id].request).toEqual(request);
  expect(screen.getByTestId('pending-prompt')).toHaveTextContent('saved investigation');
  expect(api.startSession).not.toHaveBeenCalled();
});

it.each(['failed', 'interrupted'] as const)('/archive discards an unbound %s allocation', async (phase) => {
  record = { ...record, phase, error: 'allocation failed', request: { directory: '/repo', platform: 'opencode', prompt: 'first', send: { message: 'first' } } };
  const navigate = vi.fn();
  render(<NewConversation params={record.metadata} composerRef={null} navigate={navigate} navigateToSession={vi.fn()} />);
  const input = await screen.findByRole('textbox');
  fireEvent.input(input, { target: { value: '/archive' } });
  fireEvent.click(screen.getByRole('button', { name: 'Send message' }));
  await waitFor(() => expect(fetch).toHaveBeenCalledWith(`/api/session-drafts/${id}`, expect.objectContaining({ method: 'DELETE' })));
  expect(vi.mocked(fetch).mock.calls.some(([url]) => String(url).endsWith('/archive'))).toBe(false);
  await waitFor(() => expect(navigate).toHaveBeenCalled());
});

it('waits for the chosen server route before mounting or autosaving recovered selections', async () => {
  vi.mocked(api.prepareSession).mockResolvedValue({ platform: 'opencode', agents: [{ name: 'peer-agent' }], commands: [],
    models: { hasProviders: true, models: [{ provider: 'peer', model: 'model' }] }, liveConnection: true });
  const navigate = vi.fn();
  const params = { ...record.metadata };
  const view = render(<NewConversation params={params} composerRef={null} navigate={navigate} navigateToSession={vi.fn()} />);
  await screen.findByRole('textbox');
  record = { ...record, revision: 'peer', metadata: { draftId: id, directory: '/peer/repo', remoteId: 'peer', platform: 'opencode',
    text: 'Peer prompt', model: 'peer/model', agent: 'peer-agent', reasoning: 'high', target: 'current',
    clone: { url: 'https://example.com/peer.git', source: { directory: '/source', remoteId: 'peer' } } } };
  const chosen = record.metadata;
  const puts: unknown[] = [];
  const fetcher = vi.mocked(fetch);
  const original = fetcher.getMockImplementation()!;
  fetcher.mockImplementation(async (url, options) => {
    if (options?.method === 'PUT') puts.push(JSON.parse(String(options.body)));
    return original(url, options);
  });
  const unsubscribe = subscribeBackendDraftSync();
  try {
    act(() => useBackendConversationDrafts.setState({ conflicts: { [id]: true }, saveErrors: { [id]: 'conflict' } }));
    fireEvent.click(screen.getByRole('button', { name: 'Resolve' }));
    await screen.findByRole('textbox', { name: 'Server version' });
    fireEvent.click(screen.getByRole('button', { name: 'Use server version' }));
    await waitFor(() => expect(navigate).toHaveBeenCalled());
    expect(getConversationDraft(id)).toMatchObject(chosen);
    expect(puts).toEqual([]);
    expect(screen.queryByTestId('conversation-composer')).toBeNull();
    view.rerender(<NewConversation params={chosen} composerRef={null} navigate={navigate} navigateToSession={vi.fn()} />);
    await waitFor(() => expect(screen.getByTestId('conversation-composer').querySelector('textarea')).toHaveValue('Peer prompt'));
    expect(await screen.findByTitle('Model (click to change)')).toHaveTextContent('model');
    expect(screen.getByTitle('Agent (click to change)')).toHaveTextContent('peer-agent');
    await waitFor(() => expect(puts.length).toBeGreaterThan(0));
    for (const metadata of puts) expect(metadata).toMatchObject(chosen);
    expect(getConversationDraft(id)).toMatchObject(chosen);
    expect(useBackendConversationDrafts.getState().recoveryTargets[id]).toBeUndefined();
  } finally { unsubscribe(); }
});

it('clears only after backend persistence, then accepts a follow-up while setup is still running', async () => {
  const navigate = vi.fn();
  render(<NewConversation params={record.metadata} composerRef={null} navigate={navigate} navigateToSession={vi.fn()} />);
  const input = await screen.findByRole('textbox');
  fireEvent.input(input, { target: { value: 'first prompt' } });
  fireEvent.keyDown(input, { key: 'Enter' });
  await waitFor(() => expect(payload).toEqual(expect.objectContaining({ send: expect.objectContaining({ message: 'first prompt' }) })));
  expect(input).toHaveValue('first prompt');
  expect(input).toBeDisabled();
  record = { ...record, phase: 'accepted', submittedAt: Date.now(), request: payload as BackendConversationDraft['request'] };
  await act(async () => release(json(record, 202)));
  await waitFor(() => { expect(input).toHaveValue(''); expect(input).not.toBeDisabled(); });
  expect(screen.getByText('Setup ocman session').closest('details')!.open).toBe(true);
  fireEvent.input(input, { target: { value: 'follow-up during setup' } });
  fireEvent.keyDown(input, { key: 'Enter' });
  await waitFor(() => expect(queued).toEqual([expect.objectContaining({ text: 'follow-up during setup' })]));
  await waitFor(() => expect(input).toHaveValue(''));
  expect(navigate).not.toHaveBeenCalled();
  expect(api.startSession).not.toHaveBeenCalled();
  expect(useNewConversationDrafts.getState().drafts.map((draft) => draft.draftId)).toEqual([id]);
  expect(useBackendConversationDrafts.getState().drafts[id].id).toBe(id);
});

it('retains the prompt when the start request fails', async () => {
  const fetcher = vi.mocked(fetch);
  const original = fetcher.getMockImplementation()!;
  fetcher.mockImplementation(async (url, options) => String(url).endsWith('/start')
    ? new Response('disk full', { status: 500 }) : original(url, options));
  render(<NewConversation params={record.metadata} composerRef={null} navigate={vi.fn()} navigateToSession={vi.fn()} />);
  const input = await screen.findByRole('textbox');
  fireEvent.input(input, { target: { value: 'do not lose this' } });
  fireEvent.keyDown(input, { key: 'Enter' });
  expect(await screen.findByRole('alert')).toHaveTextContent('disk full');
  await waitFor(() => expect(input).not.toBeDisabled());
  expect(input).toHaveValue('do not lose this');
  expect(api.startSession).not.toHaveBeenCalled();
});

it.each(['failure before admission', 'failure after admission', 'success after admission'])('sends on Enter despite an autosave warning and pending autosave (%s)', async (outcome) => {
  const fetcher = vi.mocked(fetch);
  const original = fetcher.getMockImplementation()!;
  let finishAutosave: ((response: Response) => void) | undefined;
  let holdAutosave = false;
  fetcher.mockImplementation(async (url, options) => {
    if (options?.method === 'PUT') {
      if (holdAutosave) {
        holdAutosave = false;
        return new Promise<Response>((resolve) => { finishAutosave = resolve; });
      }
      return new Response('autosave unavailable', { status: 503 });
    }
    return original(url, options);
  });
  render(<NewConversation params={record.metadata} composerRef={null} navigate={vi.fn()} navigateToSession={vi.fn()} />);
  const input = await screen.findByRole('textbox');
  await act(async () => { await persistBackendDraft(id).catch(() => {}); });
  await screen.findByText(/Draft not saved on the backend/);
  fireEvent.input(input, { target: { value: 'Send this anyway' } });
  holdAutosave = true;
  const autosave = persistBackendDraft(id).catch(() => {});
  await waitFor(() => expect(finishAutosave).toBeDefined());
  fireEvent.keyDown(input, { key: 'Enter' });
  await vi.waitFor(() => expect(payload).toMatchObject({ send: { message: 'Send this anyway' } }));
  const finish = (response: Response) => act(async () => {
    finishAutosave!(response);
    await autosave;
  });
  const failed = new Response('autosave unavailable', { status: 503 });
  const saved = json(record);
  if (outcome === 'failure before admission') await finish(failed);
  record = { ...record, phase: 'accepted', submittedAt: Date.now(), request: payload as BackendConversationDraft['request'] };
  await act(async () => release(json(record, 202)));
  if (outcome !== 'failure before admission') await finish(outcome === 'success after admission' ? saved : failed);
  await waitFor(() => expect(input).toHaveValue(''));
  expect(screen.getByTestId('pending-prompt')).toHaveTextContent('Send this anyway');
  expect(screen.queryByText(/Draft not saved on the backend/)).toBeNull();
});

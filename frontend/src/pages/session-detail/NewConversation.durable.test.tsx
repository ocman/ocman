// @vitest-environment jsdom
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { NewConversation } from './NewConversation';
import { api, fetchJSON } from '../../lib/api';
import { subscribeBackendDraftSync, useBackendConversationDrafts, type BackendConversationDraft } from '../../lib/backendConversationDrafts';
import { getConversationDraft, useNewConversationDrafts } from '../../lib/newConversationDrafts';
import { resetDraftTextsForTests } from '../../lib/composerDraft';
import { clearSettingsCache } from '../../lib/projectSettingsCache';
import { useApiStore } from '../../lib/apiStore';

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
afterEach(() => { vi.unstubAllGlobals(); vi.restoreAllMocks(); });

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
    fireEvent.click(screen.getByRole('button', { name: /Retry/ }));
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

it('retains the prompt when backend persistence fails', async () => {
  vi.stubGlobal('fetch', vi.fn(async (url: string, options?: RequestInit) => options?.method === 'PUT'
    ? new Response('disk full', { status: 500 }) : json(url === '/api/session-drafts' ? [] : record)));
  render(<NewConversation params={record.metadata} composerRef={null} navigate={vi.fn()} navigateToSession={vi.fn()} />);
  const input = await screen.findByRole('textbox');
  fireEvent.input(input, { target: { value: 'do not lose this' } });
  fireEvent.keyDown(input, { key: 'Enter' });
  await waitFor(() => expect(screen.getAllByRole('alert').length).toBeGreaterThan(0));
  await waitFor(() => expect(input).not.toBeDisabled());
  expect(input).toHaveValue('do not lose this');
  expect(api.startSession).not.toHaveBeenCalled();
});

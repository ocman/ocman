// @vitest-environment jsdom
import { act, renderHook, waitFor } from '@testing-library/react';
import { beforeEach, expect, it, vi } from 'vitest';
import { useDurableConversation } from './useDurableConversation';
import { fetchBackendDraft, retireBackendDraft, useBackendConversationDrafts } from '../../lib/backendConversationDrafts';
import { useApiStore } from '../../lib/apiStore';
import { APIError, postJSON } from '../../lib/api.requests';
import { getDraft, saveDraft } from '../../lib/composerDraft';

vi.mock('../../lib/backendConversationDrafts', async (original) => ({
  ...await original<typeof import('../../lib/backendConversationDrafts')>(),
  refreshBackendDrafts: vi.fn(async () => {}), persistBackendDraft: vi.fn(async () => {}),
  fetchBackendDraft: vi.fn(async () => {}), retireBackendDraft: vi.fn(async () => {}),
}));
vi.mock('../../lib/useMessageQueue', () => ({ useMessageQueue: () => ({ queue: [], refresh: vi.fn() }) }));
vi.mock('../../lib/api.requests', async (original) => ({ ...await original<typeof import('../../lib/api.requests')>(), postJSON: vi.fn(async () => {}) }));
vi.mock('../../lib/remoteLog', () => ({ remoteLog: { error: vi.fn() } }));
const id = 'ocm_01234567-89ab-4cde-8fab-0123456789ab';
const params = { draftId: id, directory: '/repo' };
beforeEach(() => {
  vi.clearAllMocks();
  useBackendConversationDrafts.setState({ drafts: {}, saveErrors: {} });
  vi.spyOn(useApiStore.getState(), 'seedNewSession').mockImplementation(() => {});
});

it('retires completed metadata and navigates using the unchanged identity', async () => {
  useBackendConversationDrafts.setState({ drafts: { [id]: { id, metadata: params, phase: 'ready', createdAt: 1, steps: {},
    result: { sessionId: id, directory: '/worktree', platform: 'opencode', remoteId: 'local', firstMessageSent: true, firstMessageError: '' } } } });
  const navigate = vi.fn();
  renderHook(() => useDurableConversation(params, navigate));
  await waitFor(() => expect(navigate).toHaveBeenCalledWith(`/session/${id}`, { replace: true }));
  expect(retireBackendDraft).toHaveBeenCalledWith(id);
  expect(useApiStore.getState().seedNewSession).toHaveBeenCalledWith(id, '/worktree', 'opencode', undefined, 'local');
});

it('fetches the complete receipt when a list projection says setup finished', async () => {
  useBackendConversationDrafts.setState({ drafts: { [id]: { id, metadata: params, phase: 'ready', createdAt: 1, steps: {} } } });
  renderHook(() => useDurableConversation(params, vi.fn()));
  await waitFor(() => expect(fetchBackendDraft).toHaveBeenCalledWith(id));
});

it('requests explicit delivery retry and exposes save retry', async () => {
  const { result } = renderHook(() => useDurableConversation(params, vi.fn()));
  await act(async () => { await result.current.retry(); await result.current.retrySave(); });
  expect(postJSON).toHaveBeenCalledWith(`/api/session-drafts/${id}/retry-delivery`, undefined, { parseJSON: false });
});

it('keeps navigation unchanged when retiring local metadata fails', async () => {
  vi.mocked(retireBackendDraft).mockRejectedValueOnce(new Error('storage unavailable'));
  useBackendConversationDrafts.setState({ drafts: { [id]: { id, metadata: params, phase: 'ready', createdAt: 1, steps: {},
    result: { sessionId: id, directory: '/repo', platform: 'opencode', remoteId: 'local', firstMessageSent: true, firstMessageError: '' } } } });
  const navigate = vi.fn();
  renderHook(() => useDurableConversation(params, navigate));
  await act(async () => {});
  expect(retireBackendDraft).toHaveBeenCalledWith(id);
  expect(navigate).not.toHaveBeenCalled();
});

it('rejects commands and missing message payloads during setup', async () => {
  useBackendConversationDrafts.setState({ drafts: { [id]: { id, metadata: params, phase: 'starting', createdAt: 1, steps: {} } } });
  const { result } = renderHook(() => useDurableConversation(params, vi.fn()));
  await act(async () => {
    await expect(result.current.send({ directory: '/repo', shell: { command: 'pwd' } })).rejects.toThrow('Commands can run after setup');
    await expect(result.current.send({ directory: '/repo' })).rejects.toThrow('Only messages can be queued');
  });
});

it('reconciles a lost initial response instead of queuing the restored prompt', async () => {
  const draft = { id, metadata: params, phase: 'draft' as const, createdAt: 1, steps: {} };
  useBackendConversationDrafts.setState({ drafts: { [id]: draft } });
  vi.mocked(postJSON).mockRejectedValueOnce(new Error('response lost'));
  const { result } = renderHook(() => useDurableConversation(params, vi.fn()));
  const request = { directory: '/repo', send: { message: 'first' } };
  await act(async () => { await expect(result.current.send(request)).rejects.toThrow('response lost'); });
  await act(async () => { useBackendConversationDrafts.setState({ drafts: { [id]: { ...draft, phase: 'starting' } } }); });
  vi.mocked(postJSON).mockResolvedValueOnce({ ...draft, phase: 'starting' });
  await act(async () => { await result.current.send(request); });
  expect(vi.mocked(postJSON).mock.calls.map(([url]) => url)).toEqual([
    `/api/session-drafts/${id}/start`, `/api/session-drafts/${id}/start`,
  ]);
});

it('admits a new first payload after a definitive rejection', async () => {
  vi.mocked(postJSON).mockRejectedValueOnce(new APIError('invalid first input', 400));
  const { result } = renderHook(() => useDurableConversation(params, vi.fn()));
  await act(async () => { await expect(result.current.send({ directory: '/repo', send: { message: 'bad' } })).rejects.toThrow('invalid'); });
  await act(async () => { await result.current.send({ directory: '/repo', send: { message: 'fixed' } }); });
  expect(postJSON).toHaveBeenLastCalledWith(`/api/session-drafts/${id}/start`, expect.objectContaining({ send: { message: 'fixed' } }));
});

it('retires a restored first prompt when polling confirms its uncertain admission completed', async () => {
  vi.mocked(postJSON).mockRejectedValueOnce(new Error('response lost'));
  const { result } = renderHook(() => useDurableConversation(params, vi.fn()));
  await act(async () => { await expect(result.current.send({ directory: '/repo', send: { message: 'first' } })).rejects.toThrow(); });
  saveDraft(id, 'first');
  await act(async () => { useBackendConversationDrafts.setState({ drafts: { [id]: { id, metadata: params, phase: 'ready', createdAt: 1, steps: {},
    result: { sessionId: id, directory: '/repo', platform: 'opencode', remoteId: 'local', firstMessageSent: true, firstMessageError: '' } } } }); });
  expect(getDraft(id)).toBe('');
});

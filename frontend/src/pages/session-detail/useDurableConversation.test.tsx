// @vitest-environment jsdom
import { act, renderHook, waitFor } from '@testing-library/react';
import { beforeEach, expect, it, vi } from 'vitest';
import { useDurableConversation } from './useDurableConversation';
import { fetchBackendDraft, retireBackendDraft, useBackendConversationDrafts } from '../../lib/backendConversationDrafts';
import { useApiStore } from '../../lib/apiStore';
import { postJSON } from '../../lib/api.requests';

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

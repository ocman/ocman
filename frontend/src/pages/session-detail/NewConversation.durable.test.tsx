// @vitest-environment jsdom
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { NewConversation } from './NewConversation';
import { api, fetchJSON } from '../../lib/api';
import { useBackendConversationDrafts, type BackendConversationDraft } from '../../lib/backendConversationDrafts';
import { useNewConversationDrafts } from '../../lib/newConversationDrafts';
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
  window.localStorage.clear();
  clearSettingsCache();
  resetDraftTextsForTests();
  useNewConversationDrafts.setState({ drafts: [], starts: {} });
  useBackendConversationDrafts.setState({ drafts: {} });
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

it('clears only after backend persistence, then accepts a follow-up while setup is still running', async () => {
  const navigate = vi.fn();
  render(<NewConversation params={record.metadata} composerRef={null} navigate={navigate} navigateToSession={vi.fn()} />);
  const input = screen.getByRole('textbox');
  fireEvent.input(input, { target: { value: 'first prompt' } });
  fireEvent.keyDown(input, { key: 'Enter' });
  await waitFor(() => expect(payload).toEqual(expect.objectContaining({ send: expect.objectContaining({ message: 'first prompt' }) })));
  expect(input).toHaveValue('first prompt');
  expect(input).toBeDisabled();
  record = { ...record, phase: 'accepted', submittedAt: Date.now(), request: payload as BackendConversationDraft['request'] };
  await act(async () => release(json(record, 202)));
  await waitFor(() => { expect(input).toHaveValue(''); expect(input).not.toBeDisabled(); });
  expect(screen.getByText('Setup').closest('details')!.open).toBe(true);
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
  const input = screen.getByRole('textbox');
  fireEvent.input(input, { target: { value: 'do not lose this' } });
  fireEvent.keyDown(input, { key: 'Enter' });
  await waitFor(() => expect(screen.getAllByRole('alert').length).toBeGreaterThan(0));
  await waitFor(() => expect(input).not.toBeDisabled());
  expect(input).toHaveValue('do not lose this');
  expect(api.startSession).not.toHaveBeenCalled();
});

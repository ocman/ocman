import { beforeEach, expect, it, vi } from 'vitest';
import { applyBackendStartProgress, discardBackendDraft, loadBackendDraftConflict, resolveBackendDraftConflict, persistBackendDraft, queueBackendDraft, refreshBackendDrafts, retireBackendDraft, useBackendConversationDrafts } from './backendConversationDrafts';
import { APIError, fetchJSON, postJSON } from './api.requests';
import { rememberConversationDraft, useNewConversationDrafts } from './newConversationDrafts';
import { getDraft, saveDraft } from './composerDraft';

vi.mock('./api.requests', async (original) => ({ ...await original<typeof import('./api.requests')>(), fetchJSON: vi.fn(), postJSON: vi.fn() }));
const id = 'ocm_01234567-89ab-4cde-8fab-0123456789ab';
const metadata = { draftId: id, directory: '/repo', remoteId: 'box' };
beforeEach(() => {
  vi.clearAllMocks();
  useNewConversationDrafts.setState({ drafts: [], starts: {} });
  useBackendConversationDrafts.setState({ drafts: {}, saveErrors: {}, conflicts: {}, recovered: {} });
  vi.mocked(postJSON).mockResolvedValue(undefined);
});

it('restores backend drafts and unsent composer text without replacing existing local drafts', async () => {
  vi.mocked(fetchJSON).mockResolvedValue([{ id, metadata: { ...metadata, text: 'saved text' }, phase: 'draft', createdAt: 100, steps: {} }]);
  await refreshBackendDrafts();
  expect(useNewConversationDrafts.getState().drafts[0]).toMatchObject({ draftId: id, remoteId: 'box', createdAt: 100 });
  expect(getDraft(id)).toBe('saved text');
  rememberConversationDraft({ ...metadata, title: 'Newer local title' });
  await refreshBackendDrafts();
  expect(useNewConversationDrafts.getState().drafts[0].title).toBe('Newer local title');
});

it('keeps save errors visible and clears them only after persistence succeeds', async () => {
  rememberConversationDraft(metadata);
  vi.mocked(postJSON).mockRejectedValueOnce(new Error('disk full'));
  await expect(persistBackendDraft(id)).rejects.toThrow('disk full');
  expect(useBackendConversationDrafts.getState().saveErrors[id]).toBe('disk full');
  await persistBackendDraft(id);
  expect(useBackendConversationDrafts.getState().saveErrors[id]).toBeUndefined();
});

it('serializes saves so a delayed old snapshot cannot overwrite newer text', async () => {
  rememberConversationDraft(metadata);
  let release!: () => void;
  const saved: string[] = [];
  vi.mocked(postJSON).mockImplementationOnce(async (_url, body) => {
    await new Promise<void>((resolve) => { release = resolve; });
    saved.push((body as { text: string }).text);
  }).mockImplementationOnce(async (_url, body) => { saved.push((body as { text: string }).text); });
  saveDraft(id, 'old');
  const old = persistBackendDraft(id);
  await Promise.resolve();
  saveDraft(id, 'new');
  const latest = persistBackendDraft(id);
  await Promise.resolve();
  release();
  await Promise.all([old, latest]);
  expect(saved).toEqual(['old', 'new']);
});

it('does not adopt a peer revision from polling and silently overwrite its changes', async () => {
  rememberConversationDraft(metadata);
  useBackendConversationDrafts.setState({ drafts: { [id]: { id, metadata, phase: 'draft', revision: 'base', createdAt: 1, steps: {} } } });
  vi.mocked(fetchJSON).mockResolvedValue([{ id, metadata: { ...metadata, text: 'peer edit' }, phase: 'draft', revision: 'peer', createdAt: 1, steps: {} }]);
  await refreshBackendDrafts();
  await persistBackendDraft(id);
  expect(postJSON).toHaveBeenCalledWith(`/api/session-drafts/${id}`, expect.anything(), { method: 'PUT', headers: { 'If-Match': 'base' } });
});

it.each(['local', 'server'] as const)('recovers a conflict using the explicitly chosen %s version', async (choice) => {
  rememberConversationDraft({ ...metadata, title: 'Local title', model: 'local-model', agent: 'local-agent', clone: { url: 'https://example.com/repo', source: { directory: '/old', remoteId: 'local' } } });
  saveDraft(id, 'local text');
  const server = { id, metadata: { ...metadata, title: 'Peer title', model: 'peer-model', text: 'peer text' }, phase: 'draft' as const, revision: 'peer', createdAt: 1, steps: {} };
  vi.mocked(postJSON).mockRejectedValueOnce(new APIError('conflict', 409));
  await expect(persistBackendDraft(id)).rejects.toThrow('conflict');
  await expect(persistBackendDraft(id)).rejects.toThrow('Resolve the draft conflict');
  expect(postJSON).toHaveBeenCalledTimes(1);
  vi.mocked(fetchJSON).mockResolvedValueOnce(server);
  const inspected = await loadBackendDraftConflict(id);
  expect(getDraft(id)).toBe('local text');
  await resolveBackendDraftConflict(id, inspected, choice);
  expect(useBackendConversationDrafts.getState().saveErrors[id]).toBeUndefined();
  expect(useBackendConversationDrafts.getState().conflicts[id]).toBe(false);
  if (choice === 'local') {
    expect(getDraft(id)).toBe('local text');
    expect(postJSON).toHaveBeenLastCalledWith(`/api/session-drafts/${id}`, expect.objectContaining({ text: 'local text', model: 'local-model' }), { method: 'PUT', headers: { 'If-Match': 'peer' } });
  } else {
    expect(getDraft(id)).toBe('peer text');
    expect(useNewConversationDrafts.getState().drafts[0]).toMatchObject({ title: 'Peer title', model: 'peer-model' });
    expect(useNewConversationDrafts.getState().drafts[0].agent).toBeUndefined();
    expect(useNewConversationDrafts.getState().drafts[0].clone).toBeUndefined();
    expect(useBackendConversationDrafts.getState().recovered[id]).toBe(1);
  }
});

it('does not adopt or overwrite a peer draft that was submitted while the dialog was open', async () => {
  rememberConversationDraft(metadata);
  saveDraft(id, 'local');
  await expect(resolveBackendDraftConflict(id, { id, metadata, phase: 'starting', createdAt: 1, steps: {} }, 'server')).rejects.toThrow('no longer an editable draft');
  expect(getDraft(id)).toBe('local');
  expect(postJSON).not.toHaveBeenCalled();
});

it('retains follow-up text under the same composer key when draft metadata retires', async () => {
  rememberConversationDraft(metadata);
  saveDraft(id, 'unsent follow-up');
  await retireBackendDraft(id);
  expect(useNewConversationDrafts.getState().drafts).toEqual([]);
  expect(getDraft(id)).toBe('unsent follow-up');
});

it('does not discard local recovery until backend discard succeeds', async () => {
  rememberConversationDraft(metadata);
  vi.mocked(postJSON).mockRejectedValueOnce(new Error('offline'));
  await expect(discardBackendDraft(id)).rejects.toThrow('offline');
  expect(useNewConversationDrafts.getState().drafts).toHaveLength(1);
  await discardBackendDraft(id);
  expect(useNewConversationDrafts.getState().drafts).toHaveLength(0);
});

it('keeps uncertain queue failures out of automatic backend retries', async () => {
  vi.mocked(postJSON).mockRejectedValueOnce(new Error('lost acknowledgement'));
  await expect(queueBackendDraft(id, { message: 'next' })).rejects.toThrow('lost acknowledgement');
  expect(postJSON).toHaveBeenCalledTimes(1);
});

it('applies live steps only to the matching known draft', async () => {
  vi.mocked(fetchJSON).mockResolvedValue([{ id, metadata, phase: 'starting', createdAt: 100, steps: {} }]);
  await refreshBackendDrafts();
  applyBackendStartProgress('other', 'platform', 'done');
  applyBackendStartProgress(id, 'platform', 'active');
  expect(useBackendConversationDrafts.getState().drafts[id].steps).toEqual({ platform: 'active' });
});

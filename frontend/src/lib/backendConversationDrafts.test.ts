import { beforeEach, expect, it, vi } from 'vitest';
import { applyBackendStartProgress, discardBackendDraft, fetchBackendDraft, loadBackendDraftConflict, resolveBackendDraftConflict, persistBackendDraft, queueBackendDraft, refreshBackendDrafts, retireBackendDraft, subscribeBackendDraftSync, useBackendConversationDrafts } from './backendConversationDrafts';
import { APIError, fetchJSON, postJSON } from './api.requests';
import { rememberConversationDraft, useNewConversationDrafts } from './newConversationDrafts';
import { applyTexts, getDraft, saveDraft } from './composerDraft';

vi.mock('./api.requests', async (original) => ({ ...await original<typeof import('./api.requests')>(), fetchJSON: vi.fn(), postJSON: vi.fn() }));
const id = 'ocm_01234567-89ab-4cde-8fab-0123456789ab';
const metadata = { draftId: id, directory: '/repo', remoteId: 'box' };
beforeEach(() => {
  vi.clearAllMocks();
  useNewConversationDrafts.setState({ drafts: [], starts: {} });
  useBackendConversationDrafts.setState({ drafts: {}, saveErrors: {}, conflicts: {}, recovered: {}, recoveryTargets: {} });
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

it('does not save an untouched stale draft when another composer changes', async () => {
  rememberConversationDraft(metadata);
  const other = 'ocm_11234567-89ab-4cde-8fab-0123456789ab';
  rememberConversationDraft({ draftId: other, directory: '/other' });
  vi.mocked(postJSON).mockImplementation(async (url) => {
    if (url === `/api/session-drafts/${id}`) throw new APIError('draft changed in another tab; recover the saved draft before overwriting it', 409);
  });
  const stop = subscribeBackendDraftSync();
  try {
    saveDraft(other, 'new message');
    await persistBackendDraft(other);
    expect(useBackendConversationDrafts.getState().saveErrors[id]).toBeUndefined();
    expect(vi.mocked(postJSON).mock.calls.every(([url]) => url === `/api/session-drafts/${other}`)).toBe(true);
  } finally { stop(); }
});

it('keeps the acknowledged revision when an older detail read finishes after a save', async () => {
  rememberConversationDraft(metadata);
  const base = { id, metadata, phase: 'draft' as const, revision: 'base', createdAt: 1, steps: {} };
  useBackendConversationDrafts.setState({ drafts: { [id]: base } });
  let release!: (value: typeof base) => void;
  vi.mocked(fetchJSON).mockImplementationOnce(() => new Promise((resolve) => { release = resolve; }));
  const reading = fetchBackendDraft(id);
  vi.mocked(postJSON).mockResolvedValue({ ...base, revision: 'saved' });
  await persistBackendDraft(id);
  release(base);
  await reading;
  expect(useBackendConversationDrafts.getState().drafts[id].revision).toBe('saved');
});

it('does not lose a newly saved draft when a stale list response omits it', async () => {
  rememberConversationDraft(metadata);
  let release!: () => void;
  vi.mocked(fetchJSON).mockImplementationOnce(() => new Promise((resolve) => { release = () => resolve([]); }));
  const reading = refreshBackendDrafts();
  vi.mocked(postJSON).mockResolvedValue({ id, metadata, phase: 'draft', revision: 'saved', createdAt: 1, steps: {} });
  await persistBackendDraft(id);
  release();
  await reading;
  expect(useBackendConversationDrafts.getState().drafts[id]?.revision).toBe('saved');
});

it('does not advance an editor revision from a peer detail read', async () => {
  const base = { id, metadata, phase: 'draft' as const, revision: 'base', createdAt: 1, steps: {} };
  useBackendConversationDrafts.setState({ drafts: { [id]: base } });
  vi.mocked(fetchJSON).mockResolvedValue({ ...base, revision: 'peer' });
  await fetchBackendDraft(id);
  expect(useBackendConversationDrafts.getState().drafts[id].revision).toBe('base');
});

it('does not autosave unchanged text notifications or identical metadata', async () => {
  rememberConversationDraft(metadata);
  const stop = subscribeBackendDraftSync();
  try {
    applyTexts([[id, { text: getDraft(id), revision: 0 }]]);
    rememberConversationDraft(metadata);
    await vi.waitFor(() => expect(useNewConversationDrafts.getState().saveErrors[id]).toBeUndefined());
    expect(postJSON).not.toHaveBeenCalled();
  } finally { stop(); }
});

it('defers only changed drafts during a refresh and does not echo restored drafts', async () => {
  rememberConversationDraft(metadata);
  const restored = 'ocm_21234567-89ab-4cde-8fab-0123456789ab';
  let release!: () => void;
  vi.mocked(fetchJSON).mockImplementationOnce(() => new Promise((resolve) => { release = () => resolve([
    { id: restored, metadata: { draftId: restored, directory: '/restored', text: 'server text' }, phase: 'draft', createdAt: 1, steps: {} },
  ]); }));
  const stop = subscribeBackendDraftSync();
  try {
    const reading = refreshBackendDrafts();
    saveDraft(id, 'edited during refresh');
    expect(postJSON).not.toHaveBeenCalled();
    release();
    await reading;
    await persistBackendDraft(id);
    expect(vi.mocked(postJSON).mock.calls.every(([url]) => url === `/api/session-drafts/${id}`)).toBe(true);
    expect(postJSON).toHaveBeenCalledWith(`/api/session-drafts/${id}`, expect.objectContaining({ text: 'edited during refresh' }), expect.anything());
    expect(getDraft(restored)).toBe('server text');
  } finally { stop(); }
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
    expect(useBackendConversationDrafts.getState().recoveryTargets[id]).toEqual(server.metadata);
    await expect(persistBackendDraft(id)).rejects.toThrow('Wait for the recovered draft route');
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

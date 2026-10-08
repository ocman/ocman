// @vitest-environment jsdom
import { beforeEach, expect, it, vi } from 'vitest';
import { waitFor } from '@testing-library/react';
import { getPendingDraftPayload, updateDraftAttachments } from './pendingDraftPayloads';
import { readDraftStart } from './draftStartClaims';
import { getDraft, getDraftEntryId, getDraftVersion, migrateDraft, saveDraft } from './composerDraft';
import { beginConversationStart, completeConversationStart, endConversationStart, failConversationStart, forgetConversationDraft, reconcileConversationStart, rememberConversationDraft, retryDraftRelocation, useNewConversationDrafts } from './newConversationDrafts';
vi.mock('./draftStartClaims', () => ({
  claimDraftStart: async (_id: string, start: import('./draftStartClaims').DraftStart) => ({ claimed: true, start }),
  persistDraftStart: async (_id: string, start: import('./draftStartClaims').DraftStart) => start,
  readDraftStart: vi.fn(async (id: string) => useNewConversationDrafts.getState().starts[id]),
}));

beforeEach(() => {
  vi.mocked(readDraftStart).mockImplementation(async (id) => useNewConversationDrafts.getState().starts[id]);
  localStorage.clear();
  window.dispatchEvent(new StorageEvent('storage', { key: null }));
  useNewConversationDrafts.setState({ drafts: [], starts: {} });
});

it('adopts the authoritative attempt before relocating a completed start with no mirror', async () => {
  rememberConversationDraft({ draftId: 'missing-mirror', directory: '/repo' });
  const draft = useNewConversationDrafts.getState().drafts[0];
  const start = { version: getDraftVersion(draft.draftId), text: '', attemptId: 'authoritative-attempt', sessionId: 'created',
    createdSession: { sessionId: 'created', platform: 'opencode', remoteId: 'local', directory: '/repo' },
    retirement: JSON.stringify([draft, getDraftVersion(draft.draftId), getDraftEntryId(draft.draftId)]) };
  saveDraft(draft.draftId, 'newer text');
  vi.mocked(readDraftStart).mockResolvedValue(start);
  await reconcileConversationStart(draft.draftId);
  const completed = useNewConversationDrafts.getState().starts[draft.draftId];
  expect(completed.attemptId).toBe('authoritative-attempt');
  expect(getDraft(completed.replacementDraftId!)).toBe('newer text');
});

it('does not overwrite another draft during an interleaved text relocation', () => {
  saveDraft('moving-text', 'move me');
  const original = Storage.prototype.setItem;
  let interleaved = false;
  const write = vi.spyOn(Storage.prototype, 'setItem').mockImplementation(function (this: Storage, key, value) {
    if (!interleaved && key.startsWith('ocman.composerDraftText.v1:')) {
      interleaved = true;
      saveDraft('other-text', 'independent edit');
    }
    original.call(this, key, value);
  });
  try {
    expect(migrateDraft('moving-text', 'moved-text')).toBe(true);
    expect(getDraft('other-text')).toBe('independent edit');
  } finally { write.mockRestore(); }
});

it('does not erase an intervening edit to the source during relocation', () => {
  saveDraft('same-source', 'copied revision');
  const original = Storage.prototype.setItem;
  let edited = false;
  const write = vi.spyOn(Storage.prototype, 'setItem').mockImplementation(function (this: Storage, key, value) {
    if (!edited && key.startsWith('ocman.composerDraftClear.v1:same-source:')) {
      edited = true;
      saveDraft('same-source', 'intervening source edit');
    }
    original.call(this, key, value);
  });
  try {
    migrateDraft('same-source', 'same-source-copy');
    expect(getDraft('same-source')).toBe('intervening source edit');
    expect(getDraft('same-source-copy')).toBe('copied revision');
  } finally { write.mockRestore(); }
});

it('keeps a source edit discoverable when it arrives during final retirement', async () => {
  const id = 'retiring-source';
  rememberConversationDraft({ draftId: id, directory: '/repo', remoteId: 'box', agent: 'plan' });
  saveDraft(id, 'submitted');
  useNewConversationDrafts.setState({ starts: { [id]: { version: getDraftVersion(id), text: 'submitted' } } });
  const original = Storage.prototype.setItem;
  let edited = false;
  const write = vi.spyOn(Storage.prototype, 'setItem').mockImplementation(function (this: Storage, key, value) {
    if (!edited && key.startsWith(`ocman.composerDraftClear.v1:${id}:`)) { edited = true; saveDraft(id, 'late source edit'); }
    original.call(this, key, value);
  });
  try {
    await completeConversationStart(id, { sessionId: 'created', platform: 'r-box:opencode', remoteId: 'box', directory: '/repo' });
    expect(getDraft(id)).toBe('late source edit');
    expect(useNewConversationDrafts.getState().drafts).toContainEqual(expect.objectContaining({ draftId: id, remoteId: 'box', agent: 'plan' }));
    await reconcileConversationStart(id);
    const replacement = useNewConversationDrafts.getState().starts[id].replacementDraftId!;
    expect(getDraft(replacement)).toBe('late source edit');
  } finally { write.mockRestore(); }
});

it.each(['discard', 'completion'])('reclaims prompt bytes after %s, including lifecycle mirrors', async (outcome) => {
  const id = `reclaim-${outcome}`;
  const text = `unique reclaimed ${outcome} prompt`;
  rememberConversationDraft({ draftId: id, directory: '/repo' });
  saveDraft(id, text);
  await beginConversationStart(id, text);
  if (outcome === 'discard') {
    await failConversationStart(id, 'creation failed');
    forgetConversationDraft(id);
  } else await completeConversationStart(id, { sessionId: 'created', platform: 'opencode', remoteId: 'local', directory: '/repo' });
  for (let i = 0; i < localStorage.length; i++) expect(localStorage.getItem(localStorage.key(i)!)).not.toContain(text);
});

it.each([false, true])('finalizes interrupted retirement while preserving newer edits: %s', async (edited) => {
  rememberConversationDraft({ draftId: 'interrupted-retirement', directory: '/repo' });
  saveDraft('interrupted-retirement', 'submitted');
  const draft = useNewConversationDrafts.getState().drafts[0];
  const retirement = JSON.stringify([draft, getDraftVersion(draft.draftId), getDraftEntryId(draft.draftId)]);
  const createdSession = { sessionId: 'created', platform: 'opencode', remoteId: 'local', directory: '/repo' };
  useNewConversationDrafts.setState({ starts: { [draft.draftId]: { version: 0, text: '', sessionId: 'created', createdSession, retirement } } });
  if (edited) saveDraft(draft.draftId, 'newer edit');
  await reconcileConversationStart(draft.draftId);
  expect(useNewConversationDrafts.getState().drafts.some((item) => item.draftId === draft.draftId)).toBe(false);
  if (edited) expect(getDraft(useNewConversationDrafts.getState().starts[draft.draftId].replacementDraftId!)).toBe('newer edit');
});

it('replays a saved retirement at startup without opening its composer', async () => {
  rememberConversationDraft({ draftId: 'startup-retirement', directory: '/repo' });
  const draft = useNewConversationDrafts.getState().drafts[0];
  const start = { version: getDraftVersion(draft.draftId), text: '', sessionId: 'created',
    createdSession: { sessionId: 'created', platform: 'opencode', remoteId: 'local', directory: '/repo' },
    retirement: JSON.stringify([draft, getDraftVersion(draft.draftId), getDraftEntryId(draft.draftId)]) };
  useNewConversationDrafts.setState({ starts: { [draft.draftId]: start } });
  localStorage.setItem(`ocman.newConversationStarts.v1:${draft.draftId}`, JSON.stringify(start));
  vi.resetModules();
  const restored = await import('./newConversationDrafts');
  await waitFor(() => expect(restored.useNewConversationDrafts.getState().drafts).toHaveLength(0));
});

it('reconciles an unopened draft at startup when its terminal mirror was never saved', async () => {
  const id = 'startup-missing-mirror';
  rememberConversationDraft({ draftId: id, directory: '/repo' });
  saveDraft(id, 'submitted');
  const draft = useNewConversationDrafts.getState().drafts.find((entry) => entry.draftId === id)!;
  const start = { version: getDraftVersion(id), text: '', sessionId: 'created',
    createdSession: { sessionId: 'created', platform: 'opencode', remoteId: 'local', directory: '/repo' },
    retirement: JSON.stringify([draft, getDraftVersion(id), getDraftEntryId(id)]) };
  vi.mocked(readDraftStart).mockResolvedValue(start);
  expect(localStorage.getItem(`ocman.newConversationStarts.v1:${id}`)).toBeNull();
  vi.resetModules();
  const restored = await import('./newConversationDrafts');
  await waitFor(() => expect(restored.useNewConversationDrafts.getState().drafts).toHaveLength(0));
  expect(restored.useNewConversationDrafts.getState().starts[id]?.sessionId).toBe('created');
});

it('transfers peer-local attachments when another tab retires a replaced draft', async () => {
  rememberConversationDraft({ draftId: 'peer-source', directory: '/repo' });
  rememberConversationDraft({ draftId: 'peer-replacement', directory: '/repo' });
  const payload = { images: [], files: [{ path: '', name: 'peer.txt', mime: 'text/plain', file: new File(['note'], 'peer.txt') }] };
  updateDraftAttachments('peer-source', payload);
  useNewConversationDrafts.setState({ starts: { 'peer-source': { version: 0, text: '', sessionId: 'created', replacementDraftId: 'peer-replacement' } } });
  localStorage.removeItem('ocman.newConversationDrafts.v1:peer-source');
  window.dispatchEvent(new StorageEvent('storage', { key: 'ocman.newConversationDrafts.v1:peer-source', newValue: null }));
  await waitFor(() => expect(getPendingDraftPayload('peer-replacement')).toEqual(payload));
  expect(getPendingDraftPayload('peer-source')).toBeUndefined();
});

it('persists independent targets and selections and only discards the selected draft', async () => {
  rememberConversationDraft({ draftId: 'first', directory: '/repo', model: 'p/m', agent: 'plan', target: 'current' });
  rememberConversationDraft({ draftId: 'second', directory: '/repo', remoteId: 'box' });
  rememberConversationDraft({ draftId: 'first', directory: '/other', reasoning: 'high' });
  saveDraft('first', 'one');
  saveDraft('second', 'two');
  expect(useNewConversationDrafts.getState().drafts[0]).toEqual({
    draftId: 'first', directory: '/other', model: 'p/m', agent: 'plan', reasoning: 'high', target: 'current', createdAt: expect.any(Number),
  });
  vi.resetModules();
  const restored = await import('./newConversationDrafts');
  expect(restored.useNewConversationDrafts.getState().drafts).toEqual(useNewConversationDrafts.getState().drafts);
  forgetConversationDraft('first');
  expect(useNewConversationDrafts.getState().drafts.map((draft) => draft.draftId)).toEqual(['second']);
  expect(getDraft('first')).toBe('');
  expect(getDraft('second')).toBe('two');
});

it('keeps live drafts when storage refuses writes', () => {
  const write = vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => { throw new Error('quota'); });
  rememberConversationDraft({ draftId: 'first', directory: '/repo' });
  expect(useNewConversationDrafts.getState().drafts).toHaveLength(1);
  write.mockRestore();
  rememberConversationDraft({ draftId: 'second', directory: '/repo' });
  expect(useNewConversationDrafts.getState().drafts.map((draft) => draft.draftId)).toEqual(['first', 'second']);
});

it('keeps live draft owners and selections when browser storage cannot be enumerated', () => {
  rememberConversationDraft({ draftId: 'read-blocked', directory: '/repo', remoteId: 'box', agent: 'plan' });
  const read = vi.spyOn(Storage.prototype, 'key').mockImplementation(() => { throw new Error('storage unavailable'); });
  try {
    rememberConversationDraft({ draftId: 'read-blocked', directory: '/repo', reasoning: 'high' });
    expect(useNewConversationDrafts.getState().drafts).toEqual([expect.objectContaining({
      draftId: 'read-blocked', remoteId: 'box', agent: 'plan', reasoning: 'high',
    })]);
  } finally { read.mockRestore(); }
});

it('holds the start guard by draft identity, releases failures and remembers completed sessions', async () => {
  rememberConversationDraft({ draftId: 'pending', directory: '/repo' });
  expect(await beginConversationStart('pending', 'prompt')).toEqual(expect.any(Number));
  expect(await beginConversationStart('pending', 'duplicate')).toBeNull();
  endConversationStart('pending');
  expect(await beginConversationStart('pending', 'retry')).toEqual(expect.any(Number));
  await completeConversationStart('pending', { sessionId: 'session', platform: 'opencode', remoteId: 'local', directory: '/repo' });
  endConversationStart('pending');
  expect(useNewConversationDrafts.getState().drafts).toEqual([]);
  expect(useNewConversationDrafts.getState().starts.pending.sessionId).toBe('session');
  expect(await beginConversationStart('pending', 'duplicate')).toBeNull();
});

it('merges another tab before writing and observes cross-tab discards', async () => {
  vi.resetModules();
  const otherTab = await import('./newConversationDrafts');
  rememberConversationDraft({ draftId: 'first', directory: '/repo' });
  otherTab.rememberConversationDraft({ draftId: 'second', directory: '/repo' });
  rememberConversationDraft({ draftId: 'first', directory: '/updated' });
  expect(useNewConversationDrafts.getState().drafts.map((draft) => draft.draftId)).toEqual(['first', 'second']);
  otherTab.forgetConversationDraft('first');
  window.dispatchEvent(new StorageEvent('storage', { key: 'ocman.newConversationDrafts.v1:first' }));
  expect(useNewConversationDrafts.getState().drafts.map((draft) => draft.draftId)).toEqual(['second']);
  rememberConversationDraft({ draftId: 'third', directory: '/repo' });
  expect(useNewConversationDrafts.getState().drafts.map((draft) => draft.draftId)).toEqual(['second', 'third']);
});

it('keeps both drafts when two tabs interleave their metadata writes', async () => {
  vi.resetModules();
  const otherTab = await import('./newConversationDrafts');
  const original = Storage.prototype.setItem;
  let interleaved = false;
  const write = vi.spyOn(Storage.prototype, 'setItem').mockImplementation(function (this: Storage, key, value) {
    if (key.startsWith('ocman.newConversationDrafts') && !interleaved) {
      interleaved = true;
      otherTab.rememberConversationDraft({ draftId: 'second', directory: '/repo' });
    }
    original.call(this, key, value);
  });
  try {
    rememberConversationDraft({ draftId: 'first', directory: '/repo' });
    vi.resetModules();
    const restored = await import('./newConversationDrafts');
    expect(restored.useNewConversationDrafts.getState().drafts.map((draft) => draft.draftId).sort()).toEqual(['first', 'second']);
  } finally { write.mockRestore(); }
});

it('reconciles a stale mirror from the authoritative failure and restores missing text', async () => {
  rememberConversationDraft({ draftId: 'reload', directory: '/repo' });
  useNewConversationDrafts.setState({ starts: { reload: { version: 0, text: 'prompt' } } });
  const claims = await import('./draftStartClaims');
  const read = vi.spyOn(claims, 'readDraftStart').mockResolvedValue({ version: 0, text: 'prompt', error: 'failed' });
  try {
    await reconcileConversationStart('reload');
    expect(useNewConversationDrafts.getState().starts.reload.error).toBe('failed');
    expect(getDraft('reload')).toBe('prompt');
    read.mockResolvedValue(undefined);
    await reconcileConversationStart('reload');
    expect(useNewConversationDrafts.getState().starts.reload.error).toBe('failed');
  } finally { read.mockRestore(); }
});

it('does not replace a newer local receipt with an outstanding read', async () => {
  const claims = await import('./draftStartClaims');
  let finish!: (value: import('./draftStartClaims').DraftStart) => void;
  const read = vi.spyOn(claims, 'readDraftStart').mockReturnValue(new Promise((resolve) => { finish = resolve; }));
  try {
    const pending = reconcileConversationStart('draft');
    const completed = { version: 0, text: '', sessionId: 'session' };
    useNewConversationDrafts.setState({ starts: { draft: completed } });
    finish({ version: 0, text: 'old prompt' });
    await pending;
    expect(useNewConversationDrafts.getState().starts.draft).toBe(completed);
  } finally { read.mockRestore(); }
});

it('preserves and repairs a known completion when its terminal transaction fails', async () => {
  rememberConversationDraft({ draftId: 'terminal', directory: '/repo' });
  useNewConversationDrafts.setState({ starts: { terminal: { version: 0, text: 'prompt', attemptId: 'one' } } });
  const claims = await import('./draftStartClaims');
  const persist = vi.spyOn(claims, 'persistDraftStart').mockRejectedValueOnce(new Error('terminal aborted'));
  const read = vi.spyOn(claims, 'readDraftStart').mockResolvedValue({ version: 0, text: 'prompt', attemptId: 'one' });
  try {
    await completeConversationStart('terminal', { sessionId: 'created', platform: 'opencode', remoteId: 'local', directory: '/repo' });
    await reconcileConversationStart('terminal');
    expect(useNewConversationDrafts.getState().starts.terminal.sessionId).toBe('created');
    expect(persist).toHaveBeenCalledTimes(2);
  } finally { persist.mockRestore(); read.mockRestore(); }
});

it('does not restore a failed receipt after the user explicitly clears or discards its prompt', async () => {
  rememberConversationDraft({ draftId: 'cleared', directory: '/repo' });
  const version = (await import('./composerDraft')).getDraftVersion('cleared');
  const claims = await import('./draftStartClaims');
  const read = vi.spyOn(claims, 'readDraftStart').mockResolvedValue({ version, text: 'old failed prompt', error: 'failed' });
  try {
    saveDraft('cleared', 'old failed prompt');
    saveDraft('cleared', '');
    await reconcileConversationStart('cleared');
    expect(getDraft('cleared')).toBe('');
    forgetConversationDraft('cleared');
    rememberConversationDraft({ draftId: 'cleared', directory: '/repo' });
    await reconcileConversationStart('cleared');
    expect(getDraft('cleared')).toBe('');
  } finally { read.mockRestore(); }
});

it('publishes a changed post-commit mirror after the pending terminal notification', async () => {
  rememberConversationDraft({ draftId: 'notify', directory: '/repo' });
  useNewConversationDrafts.setState({ starts: { notify: { version: 0, text: 'prompt' } } });
  const claims = await import('./draftStartClaims');
  let finish!: (value: import('./draftStartClaims').DraftStart) => void;
  let terminal!: import('./draftStartClaims').DraftStart;
  const persist = vi.spyOn(claims, 'persistDraftStart').mockImplementation((_id, value) => {
    terminal = value;
    return new Promise((resolve) => { finish = resolve; });
  });
  try {
    const completion = completeConversationStart('notify', { sessionId: 'created', platform: 'opencode', remoteId: 'local', directory: '/repo' });
    const before = localStorage.getItem('ocman.newConversationStarts.v1:notify');
    finish(terminal);
    await completion;
    expect(localStorage.getItem('ocman.newConversationStarts.v1:notify')).not.toBe(before);
  } finally { persist.mockRestore(); }
});

it('preserves edits made while terminal persistence is outstanding', async () => {
  rememberConversationDraft({ draftId: 'late-edit', directory: '/repo', agent: 'plan' });
  useNewConversationDrafts.setState({ starts: { 'late-edit': { version: 0, text: 'submitted' } } });
  const claims = await import('./draftStartClaims');
  let finish!: (value: import('./draftStartClaims').DraftStart) => void;
  let terminal!: import('./draftStartClaims').DraftStart;
  const persist = vi.spyOn(claims, 'persistDraftStart').mockImplementationOnce((_id, value) => {
    terminal = value;
    return new Promise((resolve) => { finish = resolve; });
  });
  try {
    const completion = completeConversationStart('late-edit', { sessionId: 'created', platform: 'opencode', remoteId: 'local', directory: '/repo' }, true);
    saveDraft('late-edit', 'typed during persistence');
    finish(terminal);
    await completion;
    const replacement = useNewConversationDrafts.getState().starts['late-edit'].replacementDraftId!;
    expect(getDraft(replacement)).toBe('typed during persistence');
    expect(useNewConversationDrafts.getState().drafts.find((draft) => draft.draftId === replacement)?.agent).toBe('plan');
  } finally { persist.mockRestore(); }
});

it('retains the source when replacement relocation fails at quota', async () => {
  rememberConversationDraft({ draftId: 'quota-copy', directory: '/repo' });
  saveDraft('quota-copy', 'only retained copy');
  useNewConversationDrafts.setState({ starts: { 'quota-copy': { version: 0, text: 'submitted' } } });
  const original = Storage.prototype.setItem;
  const write = vi.spyOn(Storage.prototype, 'setItem').mockImplementation(function (this: Storage, key, value) {
    if (key.startsWith('ocman.composerDraftText.v1:') && !key.startsWith('ocman.composerDraftText.v1:quota-copy:')) throw new Error('quota');
    original.call(this, key, value);
  });
  try {
    await completeConversationStart('quota-copy', { sessionId: 'created', platform: 'opencode', remoteId: 'local', directory: '/repo' }, false);
    expect(getDraft('quota-copy')).toBe('only retained copy');
    expect(useNewConversationDrafts.getState().drafts.some((draft) => draft.draftId === 'quota-copy')).toBe(true);
    write.mockRestore();
    await retryDraftRelocation('quota-copy');
    const target = useNewConversationDrafts.getState().starts['quota-copy'].replacementDraftId!;
    expect(getDraft(target)).toBe('only retained copy');
    expect(getDraft('quota-copy')).toBe('');
  } finally { write.mockRestore(); }
});

it.each(['commit', 'auxiliary'])('preserves retained text when source clear %s writes fail during relocation', async (failure) => {
  const id = `clear-failure-${failure}`;
  rememberConversationDraft({ draftId: id, directory: '/repo' });
  saveDraft(id, 'only recoverable retained text');
  useNewConversationDrafts.setState({ starts: { [id]: { version: getDraftVersion(id), text: 'submitted' } } });
  const original = Storage.prototype.setItem;
  const write = vi.spyOn(Storage.prototype, 'setItem').mockImplementation(function (this: Storage, key, value) {
    if (failure === 'commit' ? key.startsWith(`ocman.composerDraftClear.v1:${id}:`) : key === `ocman.composerDraftClear.v1:${id}`) throw new Error('source clear failed');
    original.call(this, key, value);
  });
  try {
    await completeConversationStart(id, { sessionId: 'created', platform: 'opencode', remoteId: 'local', directory: '/repo' }, false);
    const target = useNewConversationDrafts.getState().starts[id].replacementDraftId;
    expect(target ? getDraft(target) : getDraft(id)).toBe('only recoverable retained text');
  } finally { write.mockRestore(); }
});

it('retains late edits when their relocation fails after the completion receipt commits', async () => {
  const id = 'late-quota';
  rememberConversationDraft({ draftId: id, directory: '/repo', agent: 'plan' });
  useNewConversationDrafts.setState({ starts: { [id]: { version: 0, text: 'submitted' } } });
  const claims = await import('./draftStartClaims');
  let finish!: (value: import('./draftStartClaims').DraftStart) => void;
  let terminal!: import('./draftStartClaims').DraftStart;
  const persist = vi.spyOn(claims, 'persistDraftStart').mockImplementationOnce((_id, value) => {
    terminal = value;
    return new Promise((resolve) => { finish = resolve; });
  });
  const original = Storage.prototype.setItem;
  const write = vi.spyOn(Storage.prototype, 'setItem').mockImplementation(function (this: Storage, key, value) {
    if (key.startsWith('ocman.newConversationDrafts.v1:') && key !== `ocman.newConversationDrafts.v1:${id}`) throw new Error('metadata quota');
    original.call(this, key, value);
  });
  try {
    const completion = completeConversationStart(id, { sessionId: 'created', platform: 'opencode', remoteId: 'local', directory: '/repo' });
    saveDraft(id, 'late retained text');
    finish(terminal);
    await completion;
    expect(getDraft(id)).toBe('late retained text');
    expect(useNewConversationDrafts.getState().starts[id]).toMatchObject({ sessionId: 'created', relocationError: expect.any(String) });
    write.mockRestore();
    await retryDraftRelocation(id);
    const replacement = useNewConversationDrafts.getState().starts[id].replacementDraftId!;
    expect(getDraft(replacement)).toBe('late retained text');
    expect(useNewConversationDrafts.getState().drafts.find((draft) => draft.draftId === replacement)?.agent).toBe('plan');
  } finally { persist.mockRestore(); write.mockRestore(); }
});

it('does not retire the source when replacement metadata cannot be persisted', async () => {
  rememberConversationDraft({ draftId: 'metadata-quota', directory: '/repo', remoteId: 'box', agent: 'plan' });
  saveDraft('metadata-quota', 'recoverable source');
  const payload = { images: [], files: [{ path: '', name: 'retry.txt', mime: 'text/plain', file: new File(['note'], 'retry.txt') }] };
  updateDraftAttachments('metadata-quota', payload);
  useNewConversationDrafts.setState({ starts: { 'metadata-quota': { version: 0, text: 'submitted' } } });
  const original = Storage.prototype.setItem;
  const write = vi.spyOn(Storage.prototype, 'setItem').mockImplementation(function (this: Storage, key, value) {
    if (key.startsWith('ocman.newConversationDrafts.v1:') && key !== 'ocman.newConversationDrafts.v1:metadata-quota') throw new Error('metadata quota');
    original.call(this, key, value);
  });
  try {
    await completeConversationStart('metadata-quota', { sessionId: 'created', platform: 'r-box:opencode', remoteId: 'box', directory: '/repo' }, false);
    expect(getDraft('metadata-quota')).toBe('recoverable source');
    expect(localStorage.getItem('ocman.newConversationDrafts.v1:metadata-quota')).not.toBeNull();
    write.mockRestore();
    await retryDraftRelocation('metadata-quota');
    const target = useNewConversationDrafts.getState().starts['metadata-quota'].replacementDraftId!;
    expect(JSON.parse(localStorage.getItem(`ocman.newConversationDrafts.v1:${target}`)!)).toMatchObject({ remoteId: 'box', agent: 'plan', directory: '/repo' });
    expect(getDraft(target)).toBe('recoverable source');
    expect(getPendingDraftPayload(target)).toEqual(payload);
  } finally { write.mockRestore(); }
});

it.each(['null', '{}', '[null, {}, {"draftId": 1, "directory": "/repo"}]', 'invalid'])('ignores malformed storage %s', async (raw) => {
  localStorage.setItem('ocman.newConversationDrafts.v1:invalid', raw);
  vi.resetModules();
  const restored = await import('./newConversationDrafts');
  expect(restored.useNewConversationDrafts.getState().drafts).toEqual([]);
});

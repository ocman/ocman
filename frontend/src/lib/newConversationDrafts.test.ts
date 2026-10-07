// @vitest-environment jsdom
import { beforeEach, expect, it, vi } from 'vitest';
import { getDraft, saveDraft } from './composerDraft';
import { beginConversationStart, completeConversationStart, endConversationStart, forgetConversationDraft, reconcileConversationStart, rememberConversationDraft, retryDraftRelocation, useNewConversationDrafts } from './newConversationDrafts';
vi.mock('./draftStartClaims', () => ({
  claimDraftStart: async (_id: string, start: import('./draftStartClaims').DraftStart) => ({ claimed: true, start }),
  persistDraftStart: async (_id: string, start: import('./draftStartClaims').DraftStart) => start,
  readDraftStart: async (id: string) => useNewConversationDrafts.getState().starts[id],
}));

beforeEach(() => {
  localStorage.clear();
  window.dispatchEvent(new StorageEvent('storage', { key: null }));
  useNewConversationDrafts.setState({ drafts: [], starts: {} });
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
    if (key === 'ocman.composerDrafts.v1' && Object.keys(JSON.parse(value)).some((id) => id !== 'quota-copy')) throw new Error('quota');
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

it.each(['null', '{}', '[null, {}, {"draftId": 1, "directory": "/repo"}]', 'invalid'])('ignores malformed storage %s', async (raw) => {
  localStorage.setItem('ocman.newConversationDrafts.v1:invalid', raw);
  vi.resetModules();
  const restored = await import('./newConversationDrafts');
  expect(restored.useNewConversationDrafts.getState().drafts).toEqual([]);
});

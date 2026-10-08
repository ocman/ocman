import { create } from 'zustand';
import type { NewSessionParams } from './newSessionPath';
import { applyTexts, captureTextSeqs, settleText, finishLegacyTextImport, getDraftVersion, hydrateTexts, type TextRecord } from './composerDraft';
import type { DraftStart } from './draftStartClaims';
import { onDraftChange, publishDraftChange, transact } from './draftDb';
import { randomId } from './randomId';
import { remoteLog } from './remoteLog';
import { forgetDraftAttachments, transferDraftAttachments } from './pendingDraftPayloads';

/**
 * Prepared conversations: per-draft owner, target and selections, plus the
 * start receipt that links a draft to the session it created. Every lifecycle
 * step (claim, failure, completion, discard) is one IndexedDB transaction over
 * the texts, drafts and starts stores, so no tab can observe or interleave a
 * partial retirement or relocation. Zustand holds the synchronous snapshot.
 */
export interface ConversationDraft extends NewSessionParams {
  draftId: string;
  model?: string;
  agent?: string;
  reasoning?: string;
  target?: string;
  createdAt?: number;
}
/** A retired identity stays as a tombstone so a late autosave cannot resurrect it. */
type StoredDraft = ConversationDraft & { deleted?: boolean };
type Submitted = NonNullable<DraftStart['submitted']>;

export const routeKeyOf = (draft: Pick<NewSessionParams, 'remoteId' | 'directory' | 'platform' | 'title'>) =>
  `${draft.remoteId || 'local'}:${draft.directory}:${draft.platform}:${draft.title}`;
export const selectionsKey = (draft: Pick<ConversationDraft, 'model' | 'agent' | 'reasoning' | 'target'>) =>
  JSON.stringify([draft.model || '', draft.agent || '', draft.reasoning || '', draft.target || '']);

export const useNewConversationDrafts = create<{ drafts: ConversationDraft[]; starts: Record<string, DraftStart> }>(() => ({ drafts: [], starts: {} }));
export const getConversationDraft = (id: string) => useNewConversationDrafts.getState().drafts.find((draft) => draft.draftId === id);
const isConversationStart = (key: string) => !key.startsWith('first-delivery:');
const RELOCATION_ERROR = 'Could not update the draft. Free browser storage and retry.';

// Local metadata edits per id: a database read never overwrites a newer local edit.
const draftSeq = new Map<string, number>();
// Drafts whose latest local write failed: keep the live copy until a write succeeds.
const unsaved = new Set<string>();

function applyDrafts(entries: [string, StoredDraft | undefined][], seqs?: Map<string, number>) {
  const drafts = new Map(useNewConversationDrafts.getState().drafts.map((draft) => [draft.draftId, draft]));
  for (const [id, stored] of entries) {
    if (seqs && ((seqs.get(id) ?? 0) !== (draftSeq.get(id) || 0) || unsaved.has(id))) continue;
    if (stored && !stored.deleted) drafts.set(id, stored);
    else drafts.delete(id);
  }
  useNewConversationDrafts.setState({ drafts: [...drafts.values()]
    .sort((a, b) => (a.createdAt || 0) - (b.createdAt || 0) || a.draftId.localeCompare(b.draftId)) });
}

function applyStarts(entries: [string, DraftStart | undefined][]) {
  useNewConversationDrafts.setState((state) => {
    const starts = { ...state.starts };
    for (const [id, start] of entries) {
      if (start) starts[id] = start;
      else delete starts[id];
      // Live attachments are browser memory: follow the identity that now owns the draft.
      if (start?.replacementDraftId) transferDraftAttachments(id, start.replacementDraftId);
    }
    return { starts };
  });
}

/** Re-read the named drafts from the database into this tab. */
async function refresh(ids: string[]) {
  const seqs = new Map(ids.map((id) => [id, draftSeq.get(id) || 0]));
  const textSeqs = captureTextSeqs(ids);
  const rows = await transact(['texts', 'drafts', 'starts'], 'readonly', (tx) => Promise.all(ids.map((id) => Promise.all([
    tx.get<TextRecord>('texts', id), tx.get<StoredDraft>('drafts', id), tx.get<DraftStart>('starts', id)]))));
  // A terminal outcome this tab knows but could not store stays until it is repaired.
  const live = useNewConversationDrafts.getState().starts;
  applyStarts(ids.map((id, i) => [id, live[id]?.persistenceError ? live[id] : rows[i][2]]));
  const current: number[] = [];
  ids.forEach((id, i) => {
    if (!rows[i][1]?.deleted) { current.push(i); return; }
    // A committed discard/retirement is authoritative, including for unsaved live
    // state. Failed transactions never reach this path. Transfer ran above first.
    unsaved.delete(id);
    draftSeq.set(id, (draftSeq.get(id) || 0) + 1);
    settleText(id, rows[i][0]);
    applyDrafts([[id, rows[i][1]]]);
    forgetDraftAttachments(id);
  });
  applyTexts(current.map((i) => [ids[i], rows[i][0]]), textSeqs);
  applyDrafts(current.map((i) => [ids[i], rows[i][1]]), seqs);
}

/** Startup: one transaction reads all draft state and imports legacy localStorage text. */
export async function hydrateDrafts() {
  // Local edits made before hydration finished win over the stored values.
  const textSeqs = captureTextSeqs();
  const seqs = new Map(draftSeq);
  const [texts, drafts, starts] = await transact(['texts', 'drafts', 'starts'], 'readwrite', async (tx) =>
    Promise.all([hydrateTexts(tx), tx.getAll<StoredDraft>('drafts'), tx.getAll<DraftStart>('starts')]));
  finishLegacyTextImport();
  applyTexts([...texts], textSeqs);
  applyStarts(starts.filter(([key]) => isConversationStart(key)));
  applyDrafts(drafts, seqs);
}

export function rememberConversationDraft(params: ConversationDraft) {
  const existing = getConversationDraft(params.draftId);
  const merged = { ...existing, ...params, createdAt: existing?.createdAt || Date.now() };
  const seq = (draftSeq.get(params.draftId) || 0) + 1;
  draftSeq.set(params.draftId, seq);
  applyDrafts([[params.draftId, merged]]);
  void transact(['drafts'], 'readwrite', async (tx) => {
    const stored = await tx.get<StoredDraft>('drafts', params.draftId);
    // Never resurrect a draft another tab discarded or retired into a session.
    if (stored?.deleted) return stored;
    const next = { ...stored, ...params, createdAt: stored?.createdAt || merged.createdAt };
    tx.put('drafts', params.draftId, next);
    return next;
  }).then((committed) => {
    unsaved.delete(params.draftId);
    if (draftSeq.get(params.draftId) === seq) applyDrafts([[params.draftId, committed]]);
    publishDraftChange({ drafts: [params.draftId] });
  }, () => { unsaved.add(params.draftId); }); // Keep the live selection; the next edit retries the write.
}

/**
 * Discard a prepared draft. Live state (metadata, text, browser Files) is
 * dropped only after the tombstone commits; a failure rejects and keeps it.
 */
export async function forgetConversationDraft(draftId: string) {
  const revision = getDraftVersion(draftId);
  const cleared = await transact(['texts', 'drafts'], 'readwrite', async (tx) => {
    const text = await tx.get<TextRecord>('texts', draftId);
    const next = { text: '', revision: Math.max(text?.revision || 0, revision) + 1 };
    tx.put('drafts', draftId, { draftId, directory: '', deleted: true });
    tx.put('texts', draftId, next);
    return next;
  });
  unsaved.delete(draftId);
  draftSeq.set(draftId, (draftSeq.get(draftId) || 0) + 1);
  applyDrafts([[draftId, undefined]]);
  settleText(draftId, cleared);
  forgetDraftAttachments(draftId);
  publishDraftChange({ drafts: [draftId], texts: [draftId] });
}

export async function beginConversationStart(draftId: string, routeKey: string, text = ''): Promise<number | null> {
  const known = useNewConversationDrafts.getState().starts[draftId];
  const result = await transact(['texts', 'drafts', 'starts'], 'readwrite', async (tx) => {
    const [stored, textRecord, meta] = await Promise.all([tx.get<DraftStart>('starts', draftId),
      tx.get<TextRecord>('texts', draftId), tx.get<StoredDraft>('drafts', draftId)]);
    // Another tab discarded this identity: starting it would launch an orphaned session.
    if (meta?.deleted && !stored?.sessionId) throw new Error('This draft was discarded in another tab.');
    // A failure this tab knows about but could not store is repaired in the same transaction.
    const current = stored && !stored.error && !stored.sessionId && known?.error && known.attemptId === stored.attemptId
      ? { ...stored, error: known.error } : stored;
    if (current && (!current.error || current.sessionId)) return { claimed: false, start: current };
    const next = { version: textRecord?.revision || 0, text, routeKey, attemptId: randomId() };
    tx.put('starts', draftId, next);
    return { claimed: true, start: next };
  });
  applyStarts([[draftId, result.start]]);
  if (result.claimed) publishDraftChange({ starts: [draftId] });
  return result.claimed ? result.start.version : null;
}

/**
 * Record the created session and, atomically, retire the submitted draft or
 * move newer edits (text, owner, target or selections) to a fresh identity.
 */
export async function completeConversationStart(draftId: string, createdSession: NonNullable<DraftStart['createdSession']>, submitted: Submitted,
  /** The sent prompt, compared but never stored: storage still holding exactly it is not a newer edit. */
  sentText?: string) {
  const replacementId = randomId();
  const live = useNewConversationDrafts.getState().starts[draftId];
  draftSeq.set(draftId, (draftSeq.get(draftId) || 0) + 1);
  try {
    const { start, replacement, source, moved } = await transact(['texts', 'drafts', 'starts'], 'readwrite', async (tx) => {
      const [stored, meta, text] = await Promise.all([tx.get<DraftStart>('starts', draftId),
        tx.get<StoredDraft>('drafts', draftId), tx.get<TextRecord>('texts', draftId)]);
      const next: DraftStart = { ...(stored || live || { version: submitted.revision, text: '' }), sessionId: createdSession.sessionId,
        createdSession, text: '', submitted, relocationError: undefined, persistenceError: undefined, error: undefined };
      let replacement: ConversationDraft | undefined;
      let source: TextRecord | undefined;
      let moved: TextRecord | undefined;
      if (meta && !meta.deleted) {
        const owned = routeKeyOf(meta) === submitted.routeKey && (text?.revision || 0) === submitted.revision &&
          (!text?.text || text.text === sentText) && selectionsKey(meta) === submitted.selections;
        if (!owned) {
          replacement = { ...meta, draftId: replacementId };
          moved = { text: text?.text || '', revision: 0 };
          tx.put('drafts', replacementId, replacement);
          tx.put('texts', replacementId, moved);
          next.replacementDraftId = replacementId;
        }
        source = { text: '', revision: (text?.revision || 0) + 1 };
        tx.put('drafts', draftId, { draftId, directory: '', deleted: true });
        tx.put('texts', draftId, source);
      }
      tx.put('starts', draftId, next);
      return { start: next, replacement, source, moved };
    });
    if (source) settleText(draftId, source);
    if (replacement) applyTexts([[replacement.draftId, moved]]);
    applyDrafts([[draftId, undefined], ...(replacement ? [[replacement.draftId, replacement] as [string, ConversationDraft]] : [])]);
    applyStarts([[draftId, start]]);
    forgetDraftAttachments(draftId);
    publishDraftChange({ drafts: [draftId, ...(replacement ? [replacement.draftId] : [])], texts: [draftId], starts: [draftId] });
  } catch (error) {
    // A known created session must never become a retryable creation. Store the
    // receipt alone and leave the draft untouched for an explicit retry.
    remoteLog.error('Could not retire the started draft', error);
    const fallback: DraftStart = { ...(live || { version: submitted.revision, text: '' }), sessionId: createdSession.sessionId, createdSession,
      text: '', submitted, relocationError: RELOCATION_ERROR, error: undefined };
    try { await transact(['starts'], 'readwrite', (tx) => tx.put('starts', draftId, fallback)); publishDraftChange({ starts: [draftId] }); }
    catch (persistError) { fallback.persistenceError = persistError instanceof Error ? persistError.message : String(persistError); }
    applyStarts([[draftId, fallback]]);
  }
}

/** Retry an interrupted completion: the transaction is idempotent once the draft is retired. */
export async function retryDraftRelocation(draftId: string) {
  const receipt = useNewConversationDrafts.getState().starts[draftId];
  if (!receipt?.createdSession || !receipt.submitted) return;
  await completeConversationStart(draftId, receipt.createdSession, receipt.submitted);
  const after = useNewConversationDrafts.getState().starts[draftId];
  if (after?.relocationError) throw new Error(after.persistenceError || after.relocationError);
}

/** Record the failure and, unless a newer edit or discard happened, restore the prompt. */
export async function failConversationStart(draftId: string, error: string, restore?: { text: string; revision: number }) {
  const live = useNewConversationDrafts.getState().starts[draftId];
  const textSeqs = captureTextSeqs([draftId]);
  try {
    const { start, text } = await transact(['texts', 'drafts', 'starts'], 'readwrite', async (tx) => {
      const [stored, meta, text] = await Promise.all([tx.get<DraftStart>('starts', draftId),
        tx.get<StoredDraft>('drafts', draftId), tx.get<TextRecord>('texts', draftId)]);
      if (stored?.sessionId || (stored && live && stored.attemptId !== live.attemptId)) return { start: stored, text };
      const next = { ...(stored || live || { version: 0, text: '' }), error, text: '', persistenceError: undefined };
      tx.put('starts', draftId, next);
      let restored = text;
      if (restore && meta && !meta.deleted && (text?.revision || 0) === restore.revision && !text?.text) {
        restored = { text: restore.text, revision: restore.revision };
        tx.put('texts', draftId, restored);
      }
      return { start: next, text: restored };
    });
    applyTexts([[draftId, text]], textSeqs);
    applyStarts([[draftId, start]]);
    publishDraftChange({ starts: [draftId], texts: [draftId] });
  } catch (persistError) {
    remoteLog.error('Could not persist the terminal draft start receipt', persistError);
    applyStarts([[draftId, { ...(live || { version: 0, text: '' }), error,
      persistenceError: persistError instanceof Error ? persistError.message : String(persistError) }]]);
  }
}

/** A request that never claimed (peer pending) leaves no local lock behind. */
export function endConversationStart(draftId: string) {
  const start = useNewConversationDrafts.getState().starts[draftId];
  if (start?.sessionId || start?.error) return;
  applyStarts([[draftId, undefined]]);
}

/** Re-read authoritative state; a known terminal outcome that failed to store is written first. */
export async function reconcileConversationStart(draftId: string) {
  const live = useNewConversationDrafts.getState().starts[draftId];
  if (live?.persistenceError && live.sessionId && live.createdSession && live.submitted) {
    await completeConversationStart(draftId, live.createdSession, live.submitted);
  } else if (live?.persistenceError && live.error) {
    await failConversationStart(draftId, live.error);
  }
  await refresh([draftId]);
  const after = useNewConversationDrafts.getState().starts[draftId];
  if (after?.persistenceError) throw new Error(after.persistenceError);
}

if (typeof window !== 'undefined') onDraftChange((change) => {
  const ids = [...new Set([...(change.drafts || []), ...(change.starts || []).filter(isConversationStart)])];
  if (ids.length) void refresh(ids).catch(() => undefined);
});

import { useMemo, useSyncExternalStore } from 'react';
import { onDraftChange, publishDraftChange, transact, type DraftTx } from './draftDb';
import { remoteLog } from './remoteLog';

/**
 * Unsent composer text per session/draft id. Reads are synchronous from an
 * in-memory snapshot; every write is one IndexedDB transaction that re-checks
 * the stored revision, so a stale autosave can never land after a discard in
 * another tab. A revision only grows: explicit discard increments it, which
 * invalidates outstanding autosaves and failed-start recovery for older text.
 */
export interface TextRecord { text: string; revision: number }
const LEGACY_KEY = 'ocman.composerDrafts.v1';

const texts = new Map<string, TextRecord>();
// Local operation counter per id: a database read never overwrites a newer local edit.
const localSeq = new Map<string, number>();
// Text whose latest write failed: the live copy is authoritative until a write succeeds.
const writeErrors = new Map<string, string>();
const listeners = new Set<() => void>();
let snapshot: string | null = null;

export const getDraft = (id: string) => texts.get(id)?.text || '';
export const getDraftVersion = (id: string) => texts.get(id)?.revision || 0;
/** Why the latest local write for `id` was not stored, if it failed. */
export const getDraftWriteError = (id: string) => writeErrors.get(id);
const seqOf = (id: string) => localSeq.get(id) || 0;
const bump = (id: string) => { const seq = seqOf(id) + 1; localSeq.set(id, seq); return seq; };

const computeSnapshot = () => [...texts].filter(([, record]) => record.text).map(([id]) => id).sort().join('\n');

function emit() {
  snapshot = computeSnapshot();
  for (const listener of listeners) listener();
}

function setMemory(id: string, record: TextRecord | undefined) {
  if (record) texts.set(id, record);
  else texts.delete(id);
}

/** Apply committed database values unless a newer local edit superseded them. */
export function applyTexts(entries: [string, TextRecord | undefined][], seqs?: Map<string, number>) {
  for (const [id, record] of entries) {
    if (writeErrors.has(id) || (seqs && (seqs.get(id) ?? 0) !== seqOf(id))) continue;
    setMemory(id, record);
  }
  emit();
}

/** A transaction authoritatively replaced this text (discard, retirement): drop any unsaved live copy. */
export function settleText(id: string, record: TextRecord | undefined) {
  writeErrors.delete(id);
  bump(id);
  setMemory(id, record);
  emit();
}

/** Snapshot local edit counters (all ids when none are given) to guard a later database read. */
export const captureTextSeqs = (ids?: string[]) => ids ? new Map(ids.map((id) => [id, seqOf(id)])) : new Map(localSeq);

export function useDraftWriteError(id?: string) {
  return useSyncExternalStore(subscribeDraftTexts, () => id ? getDraftWriteError(id) : undefined, () => undefined);
}

async function refresh(ids: string[]) {
  const seqs = captureTextSeqs(ids);
  try {
    const records = await transact(['texts'], 'readonly', (tx) => Promise.all(ids.map((id) => tx.get<TextRecord>('texts', id))));
    applyTexts(ids.map((id, i) => [id, records[i]]), seqs);
  } catch { /* Keep the live snapshot when the database is unavailable. */ }
}

/** One fenced read-modify-write; `next` returns undefined to leave the stored record as is. */
function write(id: string, next: (stored: TextRecord | undefined) => TextRecord | undefined) {
  const seq = seqOf(id);
  void transact(['texts'], 'readwrite', async (tx) => {
    const stored = await tx.get<TextRecord>('texts', id);
    const value = next(stored);
    if (value) tx.put('texts', id, value);
    return value || stored;
  }).then((committed) => {
    if (seqOf(id) === seq) { writeErrors.delete(id); setMemory(id, committed); emit(); }
    publishDraftChange({ texts: [id] });
  }, (error: unknown) => {
    // Keep the typed text live (the next edit retries); never roll it back to the stored copy.
    if (seqOf(id) !== seq) return;
    writeErrors.set(id, error instanceof Error ? error.message : String(error));
    remoteLog.warn('Could not save the composer draft', error);
    emit();
  });
}

export function saveDraft(id: string, text: string, version = getDraftVersion(id)) {
  if (version !== getDraftVersion(id)) return;
  if (!text) { discardDraft(id); return; }
  bump(id);
  setMemory(id, { text, revision: version });
  emit();
  // A peer's discard raised the revision: this autosave is stale and must not land.
  write(id, (stored) => (stored?.revision || 0) <= version ? { text, revision: version } : undefined);
}

/** Remove sent text without invalidating newer edits: only the exact text/revision seen here is cleared. */
export function clearDraft(id: string) {
  const expected = texts.get(id);
  if (!expected?.text) return;
  bump(id);
  setMemory(id, { text: '', revision: expected.revision });
  emit();
  write(id, (stored) => stored && stored.text === expected.text && stored.revision === expected.revision
    ? { text: '', revision: stored.revision } : undefined);
}

/** Explicit discard: clears and invalidates outstanding autosaves and failed-send recovery. */
export function discardDraft(id: string) {
  const revision = getDraftVersion(id);
  bump(id);
  setMemory(id, { text: '', revision: revision + 1 });
  emit();
  write(id, (stored) => ({ text: '', revision: Math.max(stored?.revision || 0, revision) + 1 }));
}

/** Move text in one transaction; the source is discarded only together with the copy. */
export async function migrateDraft(from: string, to: string): Promise<boolean> {
  const seqs = captureTextSeqs([from, to]);
  try {
    const [source, target] = await transact(['texts'], 'readwrite', async (tx) => {
      const [src, dst] = await Promise.all([tx.get<TextRecord>('texts', from), tx.get<TextRecord>('texts', to)]);
      if (!src?.text) return [src, dst];
      const moved = dst?.text ? dst : { text: src.text, revision: dst?.revision || 0 };
      const cleared = { text: '', revision: src.revision + 1 };
      tx.put('texts', to, moved);
      tx.put('texts', from, cleared);
      return [cleared, moved];
    });
    applyTexts([[from, source], [to, target]], seqs);
    publishDraftChange({ texts: [from, to] });
    return true;
  } catch { return false; }
}

/** Startup: read every stored text and import the pre-IndexedDB localStorage map once. */
export async function hydrateTexts(tx: DraftTx) {
  const stored = new Map(await tx.getAll<TextRecord>('texts'));
  let legacy: Record<string, unknown> = {};
  try { legacy = JSON.parse(localStorage.getItem(LEGACY_KEY) || '{}') || {}; } catch { /* Malformed: nothing to import. */ }
  for (const [id, text] of Object.entries(legacy)) {
    if (typeof text !== 'string' || !text || stored.has(id)) continue;
    const record = { text, revision: 0 };
    tx.put('texts', id, record);
    stored.set(id, record);
  }
  return stored;
}

export function finishLegacyTextImport() {
  try { localStorage.removeItem(LEGACY_KEY); } catch { /* The import is idempotent. */ }
}

if (typeof window !== 'undefined') onDraftChange((change) => { if (change.texts?.length) void refresh(change.texts); });

/** Fires on every text change, including ones committed by other tabs. */
export function subscribeDraftTexts(listener: () => void) {
  listeners.add(listener);
  return () => { listeners.delete(listener); };
}

function getSnapshot() {
  if (snapshot === null) snapshot = computeSnapshot();
  return snapshot;
}

/** Session ids that currently hold an unsent composer draft. */
export function useDraftSessionIds(): Set<string> {
  const key = useSyncExternalStore(subscribeDraftTexts, getSnapshot, () => '');
  return useMemo(() => new Set(key ? key.split('\n') : []), [key]);
}

/** Tests only. */
export function resetDraftTextsForTests() {
  texts.clear();
  writeErrors.clear();
  localSeq.clear();
  emit();
}

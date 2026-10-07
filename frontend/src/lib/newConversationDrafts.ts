import { create } from 'zustand';
import type { NewSessionParams } from './newSessionPath';
import { discardDraft, getDraft, getDraftVersion, saveDraft } from './composerDraft';
import { claimDraftStart, persistDraftStart, readDraftStart, type DraftStart } from './draftStartClaims';
import { randomId } from './randomId';
import { remoteLog } from './remoteLog';

const STORAGE_PREFIX = 'ocman.newConversationDrafts.v1:';
const START_PREFIX = 'ocman.newConversationStarts.v1:';

export interface ConversationDraft extends NewSessionParams {
  draftId: string;
  model?: string;
  agent?: string;
  reasoning?: string;
  target?: string;
  createdAt?: number;
}

function load(): ConversationDraft[] | null {
  try {
    const drafts: ConversationDraft[] = [];
    for (let i = 0; i < localStorage.length; i++) {
      const key = localStorage.key(i);
      if (!key?.startsWith(STORAGE_PREFIX)) continue;
      try {
        const draft = JSON.parse(localStorage.getItem(key) || 'null');
        if (draft && typeof draft.draftId === 'string' && key === STORAGE_PREFIX + draft.draftId && typeof draft.directory === 'string' &&
          (draft.createdAt === undefined || typeof draft.createdAt === 'number') &&
          ['remoteId', 'platform', 'title', 'model', 'agent', 'reasoning', 'target'].every((field) =>
            draft[field] === undefined || typeof draft[field] === 'string')) drafts.push(draft);
      } catch { /* A malformed entry must not hide the other drafts. */ }
    }
    return drafts.sort((a, b) => (a.createdAt || 0) - (b.createdAt || 0) || a.draftId.localeCompare(b.draftId));
  } catch {
    return null;
  }
}

function loadStarts(): Record<string, DraftStart> {
  const starts: Record<string, DraftStart> = {};
  try {
    for (let i = 0; i < localStorage.length; i++) {
      const key = localStorage.key(i);
      if (!key?.startsWith(START_PREFIX)) continue;
      const start = JSON.parse(localStorage.getItem(key) || 'null') as DraftStart | null;
      if (start && typeof start.version === 'number' && typeof start.text === 'string') starts[key.slice(START_PREFIX.length)] = start;
    }
  } catch { /* The IndexedDB claim remains authoritative if its mirror is unavailable. */ }
  return starts;
}

export const useNewConversationDrafts = create<{
  drafts: ConversationDraft[];
  starts: Record<string, DraftStart>;
}>(() => ({ drafts: load() || [], starts: loadStarts() }));
const pendingWrites = new Map<string, ConversationDraft | null>();

function currentDrafts() {
  const drafts = new Map((load() || useNewConversationDrafts.getState().drafts).map((draft) => [draft.draftId, draft]));
  for (const [id, draft] of pendingWrites) {
    if (draft) drafts.set(id, draft);
    else drafts.delete(id);
  }
  return [...drafts.values()].sort((a, b) => (a.createdAt || 0) - (b.createdAt || 0) || a.draftId.localeCompare(b.draftId));
}

export const getConversationDraft = (draftId: string) => currentDrafts().find((draft) => draft.draftId === draftId);

function publishStart(draftId: string, start: DraftStart) {
  try { localStorage.setItem(START_PREFIX + draftId, JSON.stringify(start)); } catch { /* Retain the live receipt. */ }
  useNewConversationDrafts.setState((state) => ({ starts: { ...state.starts, [draftId]: start } }));
}

/** Mirrors are hints: a dropped terminal write must not become an indefinite lock. */
export async function reconcileConversationStart(draftId: string) {
  const before = useNewConversationDrafts.getState().starts[draftId];
  let start = await readDraftStart(draftId);
  if (useNewConversationDrafts.getState().starts[draftId] !== before) return;
  if (before && (before.sessionId || before.error) && (!start || !start.sessionId && !start.error) &&
    (!start || start.attemptId === before.attemptId)) {
    const repaired = { ...before, persistenceError: undefined };
    start = await persistDraftStart(draftId, repaired);
  }
  if (start?.error && !getDraft(draftId) && getConversationDraft(draftId)) saveDraft(draftId, start.text);
  if (JSON.stringify(start) === JSON.stringify(before)) return;
  useNewConversationDrafts.setState((state) => {
    const starts = { ...state.starts };
    if (start) starts[draftId] = start;
    else delete starts[draftId];
    return { starts };
  });
}

export async function beginConversationStart(draftId: string, text: string, routeKey?: string): Promise<number | null> {
  let { starts } = useNewConversationDrafts.getState();
  if (starts[draftId]?.persistenceError) {
    await reconcileConversationStart(draftId);
    starts = useNewConversationDrafts.getState().starts;
  }
  if (starts[draftId] && !starts[draftId].error) return null;
  const version = getDraftVersion(draftId);
  const result = await claimDraftStart(draftId, { version, text, routeKey, attemptId: randomId() });
  publishStart(draftId, result.start);
  return result.claimed ? version : null;
}

export async function completeConversationStart(draftId: string, createdSession: NonNullable<DraftStart['createdSession']>, retire = true) {
  const { starts } = useNewConversationDrafts.getState();
  let replacementDraftId: string | undefined;
  const saved = getConversationDraft(draftId);
  if (!retire && saved) {
    replacementDraftId = randomId();
    rememberConversationDraft({ ...saved, draftId: replacementDraftId });
    const text = getDraft(draftId);
    if (text) saveDraft(replacementDraftId, text);
  }
  const start = { ...starts[draftId], sessionId: createdSession.sessionId, createdSession, replacementDraftId, text: '' };
  // A known created session must never become a retryable creation if saving its receipt fails.
  await saveTerminalStart(draftId, start);
  forgetConversationDraft(draftId);
}

async function saveTerminalStart(draftId: string, start: DraftStart) {
  publishStart(draftId, start);
  try {
    const persisted = await persistDraftStart(draftId, start);
    if (persisted) publishStart(draftId, persisted);
  } catch (error) {
    remoteLog.error('Could not persist the terminal draft start receipt', error);
    publishStart(draftId, { ...start, persistenceError: error instanceof Error ? error.message : String(error) });
  }
}

export async function failConversationStart(draftId: string, error: string) {
  const { starts } = useNewConversationDrafts.getState();
  const start = { ...starts[draftId], error };
  await saveTerminalStart(draftId, start);
}

export function endConversationStart(draftId: string) {
  const { starts } = useNewConversationDrafts.getState();
  if (starts[draftId]?.sessionId || starts[draftId]?.error) return;
  const next = { ...starts };
  delete next[draftId];
  useNewConversationDrafts.setState({ starts: next });
}

function save(draftId: string, draft?: ConversationDraft) {
  pendingWrites.set(draftId, draft || null);
  for (const [id, pending] of pendingWrites) {
    try {
      if (pending) localStorage.setItem(STORAGE_PREFIX + id, JSON.stringify(pending));
      else localStorage.removeItem(STORAGE_PREFIX + id);
      pendingWrites.delete(id);
    } catch { /* Keep live metadata and retry writes on the next mutation. */ }
  }
  useNewConversationDrafts.setState({ drafts: currentDrafts() });
}

export function rememberConversationDraft(params: ConversationDraft) {
  const drafts = currentDrafts();
  const existing = drafts.find((draft) => draft.draftId === params.draftId);
  save(params.draftId, { ...existing, ...params, createdAt: existing?.createdAt || Date.now() });
}

export function forgetConversationDraft(draftId: string) {
  save(draftId);
  discardDraft(draftId);
}

if (typeof window !== 'undefined') window.addEventListener('storage', (event) => {
  if (event.key?.startsWith(START_PREFIX)) {
    void reconcileConversationStart(event.key.slice(START_PREFIX.length)).catch(() => undefined);
    return;
  }
  if (event.key !== null && !event.key.startsWith(STORAGE_PREFIX)) return;
  if (event.key === null) pendingWrites.clear();
  else if (event.newValue === null) pendingWrites.delete(event.key.slice(STORAGE_PREFIX.length));
  const drafts = load();
  if (!drafts) return;
  // Only an explicit per-draft deletion (or clear) invalidates text; an unrelated write cannot discard it.
  if (event.key && event.newValue === null && !drafts.some((draft) => STORAGE_PREFIX + draft.draftId === event.key)) discardDraft(event.key.slice(STORAGE_PREFIX.length));
  if (event.key === null) for (const old of useNewConversationDrafts.getState().drafts) {
    if (!drafts.some((draft) => draft.draftId === old.draftId)) discardDraft(old.draftId);
  }
  useNewConversationDrafts.setState({ drafts: currentDrafts() });
});

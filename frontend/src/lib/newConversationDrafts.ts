import { create } from 'zustand';
import type { NewSessionParams } from './newSessionPath';
import { discardDraft, getDraftVersion } from './composerDraft';

const STORAGE_KEY = 'ocman.newConversationDrafts.v1';

export interface ConversationDraft extends NewSessionParams {
  draftId: string;
  model?: string;
  agent?: string;
  reasoning?: string;
  target?: string;
}

function load(): ConversationDraft[] | null {
  try {
    const value: unknown = JSON.parse(localStorage.getItem(STORAGE_KEY) || '[]');
    return Array.isArray(value) ? value.filter((draft): draft is ConversationDraft =>
      !!draft && typeof draft.draftId === 'string' && typeof draft.directory === 'string' &&
      ['remoteId', 'platform', 'title', 'model', 'agent', 'reasoning', 'target'].every((key) =>
        draft[key] === undefined || typeof draft[key] === 'string')) : [];
  } catch {
    return null;
  }
}

export const useNewConversationDrafts = create<{
  drafts: ConversationDraft[];
  starts: Record<string, { version: number; text: string; sessionId?: string; error?: string }>;
}>(() => ({ drafts: load() || [], starts: {} }));
let storageUnavailable = false;

function currentDrafts() {
  return storageUnavailable ? useNewConversationDrafts.getState().drafts : load() || useNewConversationDrafts.getState().drafts;
}

export const getConversationDraft = (draftId: string) => currentDrafts().find((draft) => draft.draftId === draftId);

export function beginConversationStart(draftId: string, text: string): number | null {
  const { starts } = useNewConversationDrafts.getState();
  if (starts[draftId] && !starts[draftId].error) return null;
  const version = getDraftVersion(draftId);
  useNewConversationDrafts.setState({ starts: { ...starts, [draftId]: { version, text } } });
  return version;
}

export function completeConversationStart(draftId: string, sessionId: string) {
  const { starts } = useNewConversationDrafts.getState();
  useNewConversationDrafts.setState({ starts: { ...starts, [draftId]: { ...starts[draftId], sessionId } } });
  forgetConversationDraft(draftId);
}

export function failConversationStart(draftId: string, error: string) {
  const { starts } = useNewConversationDrafts.getState();
  useNewConversationDrafts.setState({ starts: { ...starts, [draftId]: { ...starts[draftId], error } } });
}

export function endConversationStart(draftId: string) {
  const { starts } = useNewConversationDrafts.getState();
  if (starts[draftId]?.sessionId || starts[draftId]?.error) return;
  const next = { ...starts };
  delete next[draftId];
  useNewConversationDrafts.setState({ starts: next });
}

function save(drafts: ConversationDraft[]) {
  useNewConversationDrafts.setState({ drafts });
  try {
    localStorage.setItem(STORAGE_KEY, JSON.stringify(drafts));
    storageUnavailable = false;
  } catch {
    storageUnavailable = true;
    // Keep live drafts available even when browser storage is full.
  }
}

export function rememberConversationDraft(params: ConversationDraft) {
  const drafts = currentDrafts();
  const existing = drafts.find((draft) => draft.draftId === params.draftId);
  save(existing ? drafts.map((draft) => draft === existing ? { ...draft, ...params } : draft) : [...drafts, params]);
}

export function forgetConversationDraft(draftId: string) {
  save(currentDrafts().filter((draft) => draft.draftId !== draftId));
  discardDraft(draftId);
}

if (typeof window !== 'undefined') window.addEventListener('storage', (event) => {
  if (event.key !== STORAGE_KEY && event.key !== null || storageUnavailable) return;
  const drafts = load();
  if (!drafts) return;
  for (const old of useNewConversationDrafts.getState().drafts) {
    if (!drafts.some((draft) => draft.draftId === old.draftId)) discardDraft(old.draftId);
  }
  useNewConversationDrafts.setState({ drafts });
});

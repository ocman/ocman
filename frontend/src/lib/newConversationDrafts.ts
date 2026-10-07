import { create } from 'zustand';
import type { NewSessionParams } from './newSessionPath';
import { clearDraft } from './composerDraft';

const STORAGE_KEY = 'ocman.newConversationDrafts.v1';

export interface ConversationDraft extends NewSessionParams {
  draftId: string;
  model?: string;
  agent?: string;
  reasoning?: string;
  target?: string;
}

function load(): ConversationDraft[] {
  try {
    const value: unknown = JSON.parse(localStorage.getItem(STORAGE_KEY) || '[]');
    return Array.isArray(value) ? value.filter((draft): draft is ConversationDraft =>
      !!draft && typeof draft.draftId === 'string' && typeof draft.directory === 'string' &&
      ['remoteId', 'platform', 'title', 'model', 'agent', 'reasoning', 'target'].every((key) =>
        draft[key] === undefined || typeof draft[key] === 'string')) : [];
  } catch {
    return [];
  }
}

export const useNewConversationDrafts = create<{ drafts: ConversationDraft[] }>(() => ({ drafts: load() }));

function save(drafts: ConversationDraft[]) {
  useNewConversationDrafts.setState({ drafts });
  try { localStorage.setItem(STORAGE_KEY, JSON.stringify(drafts)); } catch {
    // Keep live drafts available even when browser storage is full.
  }
}

export function rememberConversationDraft(params: ConversationDraft) {
  const drafts = useNewConversationDrafts.getState().drafts;
  const existing = drafts.find((draft) => draft.draftId === params.draftId);
  save(existing ? drafts.map((draft) => draft === existing ? { ...draft, ...params } : draft) : [...drafts, params]);
}

export function forgetConversationDraft(draftId: string) {
  save(useNewConversationDrafts.getState().drafts.filter((draft) => draft.draftId !== draftId));
  clearDraft(draftId);
}

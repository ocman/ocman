import { create } from 'zustand';
import { fetchJSON, postJSON } from './api.requests';
import type { StartSessionResponse } from './api.types';
import type { DurableStartRequest } from '../pages/session-detail/newConversationSubmission';
import { forgetConversationDraft, getConversationDraft, rememberConversationDraft, useNewConversationDrafts, type ConversationDraft } from './newConversationDrafts';
import { getDraft, saveDraft, subscribeDraftTexts } from './composerDraft';
import { publishDraftChange, transact } from './draftDb';
import { remoteLog } from './remoteLog';
import type { StartSteps } from '../pages/session-detail/LaunchSetup';

export interface BackendConversationDraft {
  id: string;
  metadata: ConversationDraft & { text?: string };
  phase: 'draft' | 'accepted' | 'starting' | 'ready' | 'failed' | 'interrupted' | 'discarded' | 'delivering';
  request?: DurableStartRequest;
  result?: StartSessionResponse;
  steps: StartSteps;
  error?: string;
  createdAt: number;
  submittedAt?: number;
}

export const useBackendConversationDrafts = create<{ drafts: Record<string, BackendConversationDraft>; saveErrors: Record<string, string> }>(() => ({ drafts: {}, saveErrors: {} }));
export const isBackendDraft = (id?: string) => !!id && /^ocm_[0-9a-f-]{36}$/.test(id);
let reading: Promise<void> | undefined;
let syncing = false;

export function refreshBackendDrafts(): Promise<void> {
  return reading ??= fetchJSON<BackendConversationDraft[]>('/api/session-drafts').then((drafts) => {
    syncing = true;
    try {
      const previous = useBackendConversationDrafts.getState().drafts;
      useBackendConversationDrafts.setState({ drafts: Object.fromEntries(drafts.map((draft) => [draft.id,
        draft.phase === 'ready' && previous[draft.id]?.phase === 'ready' && previous[draft.id]?.request ? previous[draft.id] : draft])) });
      for (const draft of drafts) {
        if (draft.phase === 'discarded') { if (getConversationDraft(draft.id)) void forgetConversationDraft(draft.id); continue; }
        if (draft.phase !== 'ready' && !getConversationDraft(draft.id)) {
          rememberConversationDraft({ ...draft.metadata, draftId: draft.id, createdAt: draft.createdAt });
          if (draft.phase === 'draft' && draft.metadata.text && !getDraft(draft.id)) saveDraft(draft.id, draft.metadata.text);
        }
      }
    } finally { syncing = false; }
  }).finally(() => { reading = undefined; });
}

export async function persistBackendDraft(id: string) {
  const draft = getConversationDraft(id);
  if (!draft || !isBackendDraft(id)) return;
  try {
    await postJSON<void>(`/api/session-drafts/${encodeURIComponent(id)}`, { ...draft, text: getDraft(id) }, { method: 'PUT', parseJSON: false });
    useBackendConversationDrafts.setState((state) => { const saveErrors = { ...state.saveErrors }; delete saveErrors[id]; return { saveErrors }; });
  } catch (error) {
    useBackendConversationDrafts.setState((state) => ({ saveErrors: { ...state.saveErrors, [id]: error instanceof Error ? error.message : String(error) } }));
    throw error;
  }
}

export function applyBackendStartProgress(id: string, step: string, value: string) {
  const record = useBackendConversationDrafts.getState().drafts[id];
  if (!record) return;
  useBackendConversationDrafts.setState((state) => ({ drafts: { ...state.drafts,
    [id]: { ...record, steps: { ...record.steps, [step]: value } } } }));
}

export async function fetchBackendDraft(id: string) {
  if (!isBackendDraft(id)) return;
  const draft = await fetchJSON<BackendConversationDraft>(`/api/session-drafts/${encodeURIComponent(id)}`);
  useBackendConversationDrafts.setState((state) => ({ drafts: { ...state.drafts, [id]: draft } }));
}

export async function discardBackendDraft(id: string, afterCommit?: () => void) {
  if (isBackendDraft(id)) await postJSON<void>(`/api/session-drafts/${encodeURIComponent(id)}`, undefined, { method: 'DELETE', parseJSON: false });
  await forgetConversationDraft(id, afterCommit);
}

export async function acceptBackendDraft(id: string, request: DurableStartRequest) {
  await persistBackendDraft(id);
  const draft = await postJSON<BackendConversationDraft>(`/api/session-drafts/${encodeURIComponent(id)}/start`, request);
  useBackendConversationDrafts.setState((state) => ({ drafts: { ...state.drafts, [id]: draft } }));
}

export async function queueBackendDraft(id: string, request: NonNullable<DurableStartRequest['send']>) {
  // An uncertain queue acknowledgement must not enter Composer's automatic retry loop.
  try { await postJSON<void>(`/api/session-drafts/${encodeURIComponent(id)}/messages`, request, { parseJSON: false }); }
  catch (error) { throw new Error(error instanceof Error ? error.message : String(error)); }
}

/** Retire only pre-launch metadata. The same identity keeps its next composer text. */
export async function retireBackendDraft(id: string) {
  await transact(['drafts'], 'readwrite', (tx) => tx.put('drafts', id, { draftId: id, directory: '', deleted: true, launched: true }));
  useNewConversationDrafts.setState((state) => ({ drafts: state.drafts.filter((draft) => draft.draftId !== id) }));
  publishDraftChange({ drafts: [id] });
}

// IndexedDB remains the offline composer cache. Server drafts own the lifecycle.
function syncDrafts() {
  if (syncing) return;
  for (const draft of useNewConversationDrafts.getState().drafts) {
    const id = draft.draftId;
    const phase = useBackendConversationDrafts.getState().drafts[id]?.phase;
    if (isBackendDraft(id) && (!phase || phase === 'draft')) void persistBackendDraft(id).catch((error) => remoteLog.error('Could not save backend draft', error));
  }
}
export function subscribeBackendDraftSync() {
  const metadata = useNewConversationDrafts.subscribe((state, previous) => { if (state.drafts !== previous.drafts) syncDrafts(); });
  const texts = subscribeDraftTexts(syncDrafts);
  return () => { metadata(); texts(); };
}

import { create } from 'zustand';
import { APIError, fetchJSON, postJSON } from './api.requests';
import type { StartSessionResponse } from './api.types';
import type { DurableStartRequest } from '../pages/session-detail/newConversationSubmission';
import { forgetConversationDraft, getConversationDraft, rememberConversationDraft, useNewConversationDrafts, type ConversationDraft } from './newConversationDrafts';
import { discardDraft, getDraft, saveDraft, subscribeDraftTexts } from './composerDraft';
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
  revision?: string;
}

export const useBackendConversationDrafts = create<{
  drafts: Record<string, BackendConversationDraft>; saveErrors: Record<string, string>;
  conflicts: Record<string, boolean>; recovered: Record<string, number>;
  recoveryTargets: Record<string, BackendConversationDraft['metadata']>;
}>(() => ({ drafts: {}, saveErrors: {}, conflicts: {}, recovered: {}, recoveryTargets: {} }));
export const isBackendDraft = (id?: string) => !!id && /^ocm_[0-9a-f-]{36}$/.test(id);
let reading: Promise<void> | undefined;
let syncing = false;
let syncDeferred = false;

export function refreshBackendDrafts(): Promise<void> {
  return reading ??= fetchJSON<BackendConversationDraft[]>('/api/session-drafts').then((drafts) => {
    syncing = true;
    try {
      const previous = useBackendConversationDrafts.getState().drafts;
      useBackendConversationDrafts.setState({ drafts: Object.fromEntries(drafts.map((draft) => [draft.id,
        draft.phase === 'ready' && previous[draft.id]?.phase === 'ready' && previous[draft.id]?.request ? previous[draft.id]
          : draft.phase === 'draft' && previous[draft.id] ? { ...draft, revision: previous[draft.id].revision } : draft])) });
      for (const draft of drafts) {
        if (draft.phase === 'discarded') { if (getConversationDraft(draft.id)) void forgetConversationDraft(draft.id); continue; }
        if (draft.phase !== 'ready' && !getConversationDraft(draft.id)) {
          rememberConversationDraft({ ...draft.metadata, draftId: draft.id, createdAt: draft.createdAt });
          if (draft.phase === 'draft' && draft.metadata.text && !getDraft(draft.id)) saveDraft(draft.id, draft.metadata.text);
        }
      }
    } finally { syncing = false; }
  }).finally(() => { reading = undefined; if (syncDeferred) { syncDeferred = false; syncDrafts(); } });
}

const saves = new Map<string, Promise<void>>();
export function persistBackendDraft(id: string): Promise<void> {
  const draft = getConversationDraft(id);
  if (!draft || !isBackendDraft(id)) return Promise.resolve();
  const snapshot = { ...draft, text: getDraft(id) };
  const previous = saves.get(id) ?? Promise.resolve();
  const save = previous.catch(() => {}).then(() => writeBackendDraft(id, snapshot));
  saves.set(id, save);
  void save.finally(() => { if (saves.get(id) === save) saves.delete(id); }).catch(() => {});
  return save;
}

async function writeBackendDraft(id: string, snapshot: ConversationDraft & { text: string }) {
  if (useBackendConversationDrafts.getState().recoveryTargets[id]) throw new Error('Wait for the recovered draft route before saving.');
  if (useBackendConversationDrafts.getState().conflicts[id]) throw new Error('Resolve the draft conflict before saving.');
  try {
    const revision = useBackendConversationDrafts.getState().drafts[id]?.revision ?? '';
    const saved = await postJSON<BackendConversationDraft>(`/api/session-drafts/${encodeURIComponent(id)}`, snapshot, { method: 'PUT', headers: { 'If-Match': revision } });
    if (saved) useBackendConversationDrafts.setState((state) => ({ drafts: { ...state.drafts, [id]: saved } }));
    useBackendConversationDrafts.setState((state) => { const saveErrors = { ...state.saveErrors }; delete saveErrors[id]; return { saveErrors }; });
  } catch (error) {
    useBackendConversationDrafts.setState((state) => ({ saveErrors: { ...state.saveErrors, [id]: error instanceof Error ? error.message : String(error) },
      conflicts: { ...state.conflicts, [id]: error instanceof APIError && error.status === 409 } }));
    throw error;
  }
}

/** Inspect without adopting the peer revision or mutating the local editor. */
export async function loadBackendDraftConflict(id: string) {
  await saves.get(id)?.catch(() => {});
  return fetchJSON<BackendConversationDraft>(`/api/session-drafts/${encodeURIComponent(id)}`);
}

export async function resolveBackendDraftConflict(id: string, server: BackendConversationDraft, choice: 'local' | 'server') {
  await saves.get(id)?.catch(() => {});
  if (server.id !== id || server.phase !== 'draft') throw new Error('This conversation is no longer an editable draft. Reopen it to recover its current state.');
  // Only an explicit choice may advance this editor's base revision.
  syncing = true;
  try {
    useBackendConversationDrafts.setState((state) => {
      const saveErrors = { ...state.saveErrors }; delete saveErrors[id];
      return { drafts: { ...state.drafts, [id]: server }, saveErrors, conflicts: { ...state.conflicts, [id]: false },
        recoveryTargets: choice === 'server' ? { ...state.recoveryTargets, [id]: { ...server.metadata, draftId: id } } : state.recoveryTargets };
    });
    if (choice === 'server') {
      rememberConversationDraft({ title: undefined, platform: undefined, remoteId: undefined, model: undefined, agent: undefined,
        reasoning: undefined, target: undefined, permissionMode: undefined, clone: undefined, ...server.metadata, draftId: id, createdAt: server.createdAt });
      discardDraft(id);
      saveDraft(id, server.metadata.text ?? '');
      useBackendConversationDrafts.setState((state) => ({ recovered: { ...state.recovered, [id]: (state.recovered[id] ?? 0) + 1 } }));
    }
  } finally { syncing = false; }
  if (choice === 'local') await persistBackendDraft(id);
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
  if (reading) { syncDeferred = true; return; }
  for (const draft of useNewConversationDrafts.getState().drafts) {
    const id = draft.draftId;
    const phase = useBackendConversationDrafts.getState().drafts[id]?.phase;
    if (isBackendDraft(id) && (!phase || phase === 'draft') && !useBackendConversationDrafts.getState().saveErrors[id] && !useBackendConversationDrafts.getState().recoveryTargets[id]) void persistBackendDraft(id).catch((error) => remoteLog.error('Could not save backend draft', error));
  }
}
export function subscribeBackendDraftSync() {
  const metadata = useNewConversationDrafts.subscribe((state, previous) => { if (state.drafts !== previous.drafts) syncDrafts(); });
  const texts = subscribeDraftTexts(syncDrafts);
  return () => { metadata(); texts(); };
}

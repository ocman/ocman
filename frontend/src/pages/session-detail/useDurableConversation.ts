import { useEffect, useRef } from 'react';
import { acceptBackendDraft, fetchBackendDraft, isBackendDraft, persistBackendDraft, queueBackendDraft, refreshBackendDrafts, retireBackendDraft, useBackendConversationDrafts } from '../../lib/backendConversationDrafts';
import { useNewConversationDrafts } from '../../lib/newConversationDrafts';
import { useApiStore } from '../../lib/apiStore';
import type { NewConversationProps } from './NewConversation';
import { remoteLog } from '../../lib/remoteLog';
import type { DurableStartRequest } from './newConversationSubmission';
import { useMessageQueue } from '../../lib/useMessageQueue';
import { uploadDraftFiles } from './uploadDraftFiles';
import { APIError, postJSON } from '../../lib/api.requests';
import { clearDraft } from '../../lib/composerDraft';

export function useDurableConversation(params: NewConversationProps['params'], navigate: NewConversationProps['navigate']) {
  const id = params.draftId ?? '';
  const enabled = isBackendDraft(id);
  const record = useBackendConversationDrafts((state) => state.drafts[id]);
  const phase = record?.phase;
  const saveError = useBackendConversationDrafts((state) => state.saveErrors[id]);
  const metadata = useNewConversationDrafts((state) => state.drafts.find((draft) => draft.draftId === id));
  const complete = useRef('');
  const reconciled = useRef('');
  const pendingInitial = useRef<{ request: DurableStartRequest; original: string } | undefined>(undefined);
  const mounted = useRef(false);
  useEffect(() => { mounted.current = true; return () => { mounted.current = false; }; }, []);
  const queue = useMessageQueue(enabled && record && record.phase !== 'draft' ? id : undefined, record?.request?.platform);
  useEffect(() => {
    if (!enabled || !metadata || phase && phase !== 'draft') return;
    void persistBackendDraft(id).catch((error) => remoteLog.error('Could not save backend draft', error));
  }, [enabled, id, metadata, phase]);
  useEffect(() => {
    if (!enabled) return;
    let active = true;
    let timer: ReturnType<typeof setTimeout>;
    const refresh = () => {
      void refreshBackendDrafts().catch((error) => remoteLog.error('Could not refresh setup', error)).finally(() => {
        if (active && phase && ['accepted', 'starting', 'delivering'].includes(phase)) timer = setTimeout(refresh, 1000);
      });
    };
    refresh();
    return () => { active = false; clearTimeout(timer); };
  }, [enabled, id, phase]);
  useEffect(() => {
    if (enabled && record?.phase === 'ready' && !record.result) void fetchBackendDraft(id).catch((error) => remoteLog.error('Could not read setup result', error));
  }, [enabled, record?.phase, record?.result, id]);
  useEffect(() => {
    if (!enabled || !record || record.phase === 'draft' || record.phase === 'discarded' || reconciled.current === id) return;
    // Admission owns the first payload even while setup/delivery is unfinished.
    // Also reconcile restored text after a lost response or reopening the draft.
    const request = record.request ?? pendingInitial.current?.request;
    const text = request?.prompt ?? request?.send?.message;
    if (text !== undefined) {
      clearDraft(id, text);
      reconciled.current = id;
    }
  }, [enabled, record, id]);
  useEffect(() => {
    if (!enabled || record?.phase !== 'ready' || !record.result || complete.current === id) return;
    pendingInitial.current = undefined;
    complete.current = id;
    const result = record.result;
    useApiStore.getState().seedNewSession(id, result.directory, result.platform, params.title, result.remoteId);
    void retireBackendDraft(id).then(() => { if (mounted.current) navigate(`/session/${id}`, { replace: true }); }).catch((error) => {
      complete.current = '';
      remoteLog.error('Could not retire draft metadata', error);
    });
  }, [enabled, record, id, navigate, params.title]);

  async function admitInitial(request: DurableStartRequest) {
    try {
      await acceptBackendDraft(id, request);
      pendingInitial.current = undefined;
    } catch (error) {
      if (error instanceof APIError && error.status >= 400 && error.status < 500) pendingInitial.current = undefined;
      throw error;
    }
  }

  return {
    enabled, record, queue, saveError,
    retrySave: () => persistBackendDraft(id),
    submitted: !!record && record.phase !== 'draft',
    async send(request: DurableStartRequest, files?: File[]) {
      const original = JSON.stringify(request);
      if (pendingInitial.current) {
        const pending = pendingInitial.current;
        await admitInitial(pending.request);
        if (pending.original === original) return;
      }
      const observed = useBackendConversationDrafts.getState().drafts[id];
      const initialSubmission = !observed || observed.phase === 'draft';
      await persistBackendDraft(id);
      await fetchBackendDraft(id);
      const current = useBackendConversationDrafts.getState().drafts[id];
      if (files?.length && request.send) request = { ...request, send: { ...request.send, message: await uploadDraftFiles(id, request.send.message, files) } };
      if (current && current.phase !== 'draft' && !initialSubmission) {
        if (request.command || request.shell) throw new Error('Commands can run after setup completes. Messages can be queued now.');
        if (!request.send) throw new Error('Only messages can be queued during setup.');
        await queueBackendDraft(id, request.send);
        queue.refresh();
        return;
      }
      // The response means persisted, not setup complete. Composer can clear now.
      pendingInitial.current = { request, original };
      await admitInitial(request);
    },
    async retry() {
      await postJSON<void>(`/api/session-drafts/${encodeURIComponent(id)}/retry-delivery`, undefined, { parseJSON: false });
      await refreshBackendDrafts();
    },
  };
}

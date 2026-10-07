import { useEffect, useState, type ReactNode } from 'react';
import { NEW_SESSION_ID, newSessionPath } from '../../lib/newSessionPath';
import { reconcileConversationStart, useNewConversationDrafts } from '../../lib/newConversationDrafts';
import { useApiStore } from '../../lib/apiStore';
import { clearDraft, getDraft, saveDraft } from '../../lib/composerDraft';
import { randomId } from '../../lib/randomId';
import { InlineAlert } from '../../components/InlineAlert';
import type { NewConversationProps } from './NewConversation';

/** Retire completed drafts and stop editing identities discarded by another tab. */
export function PreparedDraftLifecycle({ params, navigate, navigateToSession, children }:
  Pick<NewConversationProps, 'params' | 'navigate' | 'navigateToSession'> & { children: ReactNode }) {
  const { directory, remoteId, platform, title, draftId: routeDraftId } = params;
  const [legacyId] = useState(randomId);
  const draftId = params.draftId || legacyId;
  const exists = useNewConversationDrafts((state) => state.drafts.some((draft) => draft.draftId === draftId));
  const routeKey = `${params.remoteId || 'local'}:${params.directory}:${params.platform}:${params.title}`;
  const createdSession = useNewConversationDrafts((state) => state.starts[draftId]?.createdSession);
  const sessionId = useNewConversationDrafts((state) => {
    const start = state.starts[draftId];
    return start && (!start.routeKey || start.routeKey === routeKey) ? start.sessionId : undefined;
  });
  const [observed, setObserved] = useState('');
  const [receiptError, setReceiptError] = useState('');
  const receipt = useNewConversationDrafts((state) => state.starts[draftId]);
  const receiptState = JSON.stringify([receipt?.version, receipt?.error, receipt?.sessionId]);
  useEffect(() => {
    if (!routeDraftId) {
      const text = getDraft(NEW_SESSION_ID);
      if (text) { saveDraft(legacyId, text); clearDraft(NEW_SESSION_ID); }
      navigate(newSessionPath({ directory, remoteId, platform, title, draftId: legacyId }));
      return;
    }
    let active = true;
    void reconcileConversationStart(draftId).catch((error: unknown) => {
      if (active) setReceiptError(error instanceof Error ? error.message : String(error));
    });
    return () => { active = false; };
  }, [directory, remoteId, platform, title, routeDraftId, legacyId, draftId, receiptState, navigate]);
  useEffect(() => {
    let active = true;
    if (sessionId) {
      const store = useApiStore.getState();
      if (createdSession && store.getCachedSession(sessionId)?.session.platform !== createdSession.platform) {
        store.seedNewSession(sessionId, createdSession.directory, createdSession.platform, params.title, createdSession.remoteId);
      }
      navigateToSession(sessionId);
    }
    // eslint-disable-next-line react-hooks/set-state-in-effect -- remember which route has registered its client-only draft.
    else if (exists) setObserved(draftId);
    else if (observed === draftId) {
      // Completion and deletion arrive on different storage keys. Read the
      // authoritative receipt before interpreting retirement as user discard.
      void reconcileConversationStart(draftId).then(() => {
        if (!active) return;
        const start = useNewConversationDrafts.getState().starts[draftId];
        if (start?.sessionId && (!start.routeKey || start.routeKey === routeKey)) return;
        navigate(newSessionPath({ ...params, draftId: undefined }));
      }).catch((error: unknown) => {
        if (active) setReceiptError(error instanceof Error ? error.message : String(error));
      });
    }
    return () => { active = false; };
  }, [draftId, exists, sessionId, createdSession, observed, params, routeKey, navigate, navigateToSession]);
  if (!params.draftId || sessionId || observed === draftId && !exists) return null;
  return <>{receiptError && receipt && !receipt.error && !receipt.sessionId && <InlineAlert>{receiptError}</InlineAlert>}{children}</>;
}

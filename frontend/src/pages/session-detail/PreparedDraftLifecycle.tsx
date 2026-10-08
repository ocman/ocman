import { useEffect, useRef, useState, type ReactNode } from 'react';
import { NEW_SESSION_ID, newSessionPath } from '../../lib/newSessionPath';
import { reconcileConversationStart, retryDraftRelocation, useNewConversationDrafts } from '../../lib/newConversationDrafts';
import { useApiStore } from '../../lib/apiStore';
import { migrateDraft } from '../../lib/composerDraft';
import { randomId } from '../../lib/randomId';
import { InlineAlert } from '../../components/InlineAlert';
import type { NewConversationProps } from './NewConversation';

/** Retire completed drafts and stop editing identities discarded by another tab. */
export function PreparedDraftLifecycle({ params, navigate, children }:
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
  const [receiptRetry, setReceiptRetry] = useState(0);
  const navigated = useRef('');
  const receipt = useNewConversationDrafts((state) => state.starts[draftId]);
  const replacement = useNewConversationDrafts((state) => state.drafts.find((draft) => draft.draftId === state.starts[draftId]?.replacementDraftId));
  const receiptState = JSON.stringify([receipt?.version, receipt?.error, receipt?.sessionId]);
  useEffect(() => {
    let active = true;
    if (!routeDraftId) {
      void migrateDraft(NEW_SESSION_ID, legacyId).then((moved) => {
        if (!active) return;
        if (!moved) { setReceiptError('Could not migrate the saved draft. Free browser storage and retry.'); return; }
        navigate(newSessionPath({ directory, remoteId, platform, title, draftId: legacyId }), { replace: true });
      });
      return () => { active = false; };
    }
    void reconcileConversationStart(draftId).catch((error: unknown) => {
      if (active) setReceiptError(error instanceof Error ? error.message : String(error));
    });
    return () => { active = false; };
  }, [directory, remoteId, platform, title, routeDraftId, legacyId, draftId, receiptState, receiptRetry, navigate]);
  useEffect(() => {
    let active = true;
    if (receipt?.relocationError) return;
    if (replacement) {
      const target = `${draftId}:${replacement.draftId}`;
      if (navigated.current !== target) { navigated.current = target; navigate(newSessionPath(replacement), { replace: true }); }
    }
    else if (sessionId) {
      const target = `${draftId}:${sessionId}`;
      if (navigated.current === target) return;
      navigated.current = target;
      const store = useApiStore.getState();
      if (createdSession && store.getCachedSession(sessionId)?.session.platform !== createdSession.platform) {
        store.seedNewSession(sessionId, createdSession.directory, createdSession.platform, params.title, createdSession.remoteId);
      }
      navigate(`/session/${sessionId}`, { replace: true });
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
        navigate(newSessionPath({ ...params, draftId: undefined }), { replace: true });
      }).catch((error: unknown) => {
        if (active) setReceiptError(error instanceof Error ? error.message : String(error));
      });
    }
    return () => { active = false; };
  }, [draftId, exists, sessionId, createdSession, replacement, observed, params, routeKey, receipt?.relocationError, receiptRetry, navigate]);
  const retry = () => {
    setReceiptError('');
    if (receipt?.relocationError) void retryDraftRelocation(draftId).catch((error: unknown) => setReceiptError(error instanceof Error ? error.message : String(error)));
    else setReceiptRetry((value) => value + 1);
  };
  if (receipt?.relocationError) return <InlineAlert onRetry={retry}>{receiptError || receipt.relocationError}</InlineAlert>;
  if (!params.draftId) return receiptError ? <InlineAlert onRetry={retry}>{receiptError}</InlineAlert> : null;
  if (sessionId || replacement) return null;
  if (observed === draftId && !exists) return receiptError ? <InlineAlert onRetry={retry}>{receiptError}</InlineAlert> : null;
  return <>{(receiptError || receipt?.persistenceError) && <InlineAlert onRetry={retry}>{receiptError || receipt?.persistenceError}</InlineAlert>}{children}</>;
}

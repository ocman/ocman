import { useEffect, useState, type ReactNode } from 'react';
import { NEW_SESSION_ID, newSessionPath } from '../../lib/newSessionPath';
import { useNewConversationDrafts } from '../../lib/newConversationDrafts';
import type { NewConversationProps } from './NewConversation';

/** Retire completed drafts and stop editing identities discarded by another tab. */
export function PreparedDraftLifecycle({ params, navigate, navigateToSession, children }:
  Pick<NewConversationProps, 'params' | 'navigate' | 'navigateToSession'> & { children: ReactNode }) {
  const draftId = params.draftId || NEW_SESSION_ID;
  const exists = useNewConversationDrafts((state) => state.drafts.some((draft) => draft.draftId === draftId));
  const sessionId = useNewConversationDrafts((state) => state.starts[draftId]?.sessionId);
  const [observed, setObserved] = useState('');
  useEffect(() => {
    if (sessionId) navigateToSession(sessionId);
    // eslint-disable-next-line react-hooks/set-state-in-effect -- remember which route has registered its client-only draft.
    else if (exists) setObserved(draftId);
    else if (observed === draftId) navigate(newSessionPath({ ...params, draftId: undefined }));
  }, [draftId, exists, sessionId, observed, params, navigate, navigateToSession]);
  return sessionId || observed === draftId && !exists ? null : children;
}

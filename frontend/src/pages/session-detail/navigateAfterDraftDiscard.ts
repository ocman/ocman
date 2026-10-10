import { flushSync } from 'react-dom';
import { useApiStore } from '../../lib/apiStore';
import { useUiStore } from '../../lib/uiStore';
import { useNewConversationDrafts } from '../../lib/newConversationDrafts';
import { newSessionPath } from '../../lib/newSessionPath';

export function navigateAfterDraftDiscard(draftId: string, navigate: (path: string, options: { replace: boolean }) => void) {
  const next = useNewConversationDrafts.getState().drafts.find((entry) => entry.draftId !== draftId);
  const sessions = useApiStore.getState().recentSessions.filter((session) => !session.archived);
  const lastOpened = sessions.find((session) => session.id === useUiStore.getState().lastOpenedSessionId);
  const target = lastOpened ?? sessions.reduce<(typeof sessions)[number] | undefined>(
    (latest, session) => !latest || session.timeUpdated > latest.timeUpdated ? session : latest, undefined);
  // Stay on the session route: bouncing through / unmounts the workspace.
  flushSync(() => navigate(next ? newSessionPath(next) : target ? `/session/${target.id}` : '/session/new', { replace: true }));
}

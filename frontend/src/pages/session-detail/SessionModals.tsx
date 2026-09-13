import { useCallback } from 'react';
import type { Dispatch, SetStateAction } from 'react';
import { api } from '../../lib/api';
import type { Message, Part, Project, Session } from '../../lib/api';
import { useApiStore } from '../../lib/apiStore';
import { remoteLog } from '../../lib/remoteLog';
import { RenameModal } from './RenameModal';
import { ForkPicker } from './ForkPicker';
import { MessageJumpPicker } from './MessageJumpPicker';
import { MovePathDialog, MovePicker } from './MovePicker';
import type { UsePendingSendResult } from './usePendingSend';

export interface MessageJumpHistory {
  sessionId: string;
  messages: Message[];
  parts: Part[];
}

export interface SessionModalsProps {
  session: Session;
  messages: Message[];
  parts: Part[];
  allProjects: Project[] | undefined;
  recentSessions: Session[];
  /** Full history fetched for the jump picker; falls back to `messages`. */
  messageJumpHistory: MessageJumpHistory | null;
  pending: UsePendingSendResult;

  showRenameModal: boolean;
  setShowRenameModal: Dispatch<SetStateAction<boolean>>;
  showForkPicker: boolean;
  setShowForkPicker: Dispatch<SetStateAction<boolean>>;
  showMessageJumpPicker: boolean;
  setShowMessageJumpPicker: Dispatch<SetStateAction<boolean>>;
  showMovePicker: boolean;
  setShowMovePicker: Dispatch<SetStateAction<boolean>>;
  showMovePathDialog: boolean;
  setShowMovePathDialog: Dispatch<SetStateAction<boolean>>;

  patchSession: (patch: Partial<Pick<Session, 'title' | 'directory'>>) => void;
  navigateToSession: (id: string) => void;
  hydrateHistory: (messages: Message[], parts: Part[]) => void;
  onRenamed: () => void;
  onScrollToMessage: (messageId: string) => void;
}

/** Rename / fork / jump / move dialogs for the open session. */
export function SessionModals({
  session,
  messages,
  parts,
  allProjects,
  recentSessions,
  messageJumpHistory,
  pending,
  showRenameModal,
  setShowRenameModal,
  showForkPicker,
  setShowForkPicker,
  showMessageJumpPicker,
  setShowMessageJumpPicker,
  showMovePicker,
  setShowMovePicker,
  showMovePathDialog,
  setShowMovePathDialog,
  patchSession,
  navigateToSession,
  hydrateHistory,
  onRenamed,
  onScrollToMessage,
}: SessionModalsProps) {
  const patchRecentSession = useApiStore((state) => state.patchRecentSession);

  const handleMoveDestination = useCallback((directory: string) => {
    pending.begin(`/move ${directory}`);
    api.moveSession(session.id, directory)
      .then(() => {
        pending.clear();
        patchSession({ directory });
        patchRecentSession(session.id, { directory });
      })
      .catch((error) => {
        remoteLog.error('Failed to move session', error);
        pending.fail(error instanceof Error ? error.message : 'Unknown error');
      });
  }, [patchRecentSession, patchSession, pending, session.id]);

  const sameHost = (remoteId: string | undefined) => (remoteId || 'local') === (session.remoteId || 'local');
  const jumpHistory = messageJumpHistory?.sessionId === session.id ? messageJumpHistory : null;

  return (
    <>
      {showRenameModal && (
        <RenameModal
          sessionId={session.id}
          initialTitle={session.title || ''}
          onClose={() => setShowRenameModal(false)}
          onRenamed={(newTitle) => {
            patchSession({ title: newTitle });
            patchRecentSession(session.id, { title: newTitle });
            onRenamed();
          }}
        />
      )}
      {showForkPicker && (
        <ForkPicker
          open
          messages={messages}
          parts={parts}
          onClose={() => setShowForkPicker(false)}
          onSelect={(messageID) => {
            setShowForkPicker(false);
            pending.begin('/fork');
            api.forkSession(session.id, messageID)
              .then(({ id: forkedID }) => {
                pending.clear();
                navigateToSession(forkedID);
              })
              .catch((error) => {
                remoteLog.error('Failed to fork session', error);
                pending.fail(error instanceof Error ? error.message : 'Unknown error');
              });
          }}
        />
      )}
      {showMessageJumpPicker && (
        <MessageJumpPicker
          open
          messages={jumpHistory ? jumpHistory.messages : messages}
          parts={jumpHistory ? jumpHistory.parts : parts}
          onClose={() => setShowMessageJumpPicker(false)}
          onSelect={(messageId) => {
            if (jumpHistory && !messages.some((message) => message.id === messageId)) {
              hydrateHistory(jumpHistory.messages, jumpHistory.parts);
            }
            onScrollToMessage(messageId);
          }}
        />
      )}
      {showMovePicker && (
        <MovePicker
          open
          currentDirectory={session.directory}
          directories={[
            ...(allProjects ?? []).filter((project) => sameHost(project.remoteId)).map((project) => project.directory),
            ...recentSessions.filter((recent) => sameHost(recent.remoteId)).map((recent) => recent.directory),
          ]}
          onClose={() => setShowMovePicker(false)}
          onCustom={() => setShowMovePathDialog(true)}
          onSelect={(directory) => {
            setShowMovePicker(false);
            handleMoveDestination(directory);
          }}
        />
      )}
      {showMovePathDialog && (
        <MovePathDialog
          onClose={() => setShowMovePathDialog(false)}
          onSelect={(directory) => {
            setShowMovePathDialog(false);
            handleMoveDestination(directory);
          }}
        />
      )}
    </>
  );
}

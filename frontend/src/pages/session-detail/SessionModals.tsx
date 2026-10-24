import { api } from '../../lib/api';
import type { Message, Part, Project, Session } from '../../lib/api';
import { remoteLog } from '../../lib/remoteLog';
import { RenameModal } from './RenameModal';
import { UserMessagePicker } from './UserMessagePicker';
import { MovePathDialog, MovePicker } from './MovePicker';
import type { UsePendingSendResult } from './usePendingSend';
import type { SessionModal } from './useSessionModal';
import { patchSessionLists } from '../../lib/sessionsCache';

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
  /** The platform can fork from a chosen message; otherwise only the full session. */
  forkAtMessage?: boolean;

  openModal: SessionModal | null;
  onClose: () => void;
  onOpen: (modal: SessionModal) => void;

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
  forkAtMessage = true,
  openModal,
  onClose,
  onOpen,
  patchSession,
  navigateToSession,
  hydrateHistory,
  onRenamed,
  onScrollToMessage,
}: SessionModalsProps) {

  const handleMoveDestination = (directory: string) => {
    pending.begin(`/move ${directory}`);
    api.moveSession(session.id, directory)
      .then(() => {
        pending.clear();
        patchSession({ directory });
        patchSessionLists(session.id, { directory }, session.platform);
      })
      .catch((error) => {
        remoteLog.error('Failed to move session', error);
        pending.fail(error instanceof Error ? error.message : 'Unknown error');
      });
  };

  const sameHost = (remoteId: string | undefined) => (remoteId || 'local') === (session.remoteId || 'local');
  const jumpHistory = messageJumpHistory?.sessionId === session.id ? messageJumpHistory : null;

  return (
    <>
      {openModal === 'rename' && (
        <RenameModal
          sessionId={session.id}
          initialTitle={session.title || ''}
          onClose={onClose}
          onRenamed={(newTitle) => {
            patchSession({ title: newTitle });
            patchSessionLists(session.id, { title: newTitle }, session.platform);
            onRenamed();
          }}
        />
      )}
      {openModal === 'fork' && (
        <UserMessagePicker
          open
          mode="fork"
          messages={messages}
          parts={parts}
          fullSessionOnly={!forkAtMessage}
          onClose={onClose}
          onSelect={(messageID) => {
            onClose();
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
      {openModal === 'jump' && (
        <UserMessagePicker
          open
          mode="jump"
          messages={jumpHistory ? jumpHistory.messages : messages}
          parts={jumpHistory ? jumpHistory.parts : parts}
          onClose={onClose}
          onSelect={(messageId) => {
            if (jumpHistory && !messages.some((message) => message.id === messageId)) {
              hydrateHistory(jumpHistory.messages, jumpHistory.parts);
            }
            onScrollToMessage(messageId);
          }}
        />
      )}
      {openModal === 'move' && (
        <MovePicker
          open
          currentDirectory={session.directory}
          directories={[
            ...(allProjects ?? []).filter((project) => sameHost(project.remoteId)).map((project) => project.directory),
            ...recentSessions.filter((recent) => sameHost(recent.remoteId)).map((recent) => recent.directory),
          ]}
          onClose={onClose}
          onCustom={() => onOpen('movePath')}
          onSelect={(directory) => {
            onClose();
            handleMoveDestination(directory);
          }}
        />
      )}
      {openModal === 'movePath' && (
        <MovePathDialog
          onClose={onClose}
          onSelect={(directory) => {
            onClose();
            handleMoveDestination(directory);
          }}
        />
      )}
    </>
  );
}

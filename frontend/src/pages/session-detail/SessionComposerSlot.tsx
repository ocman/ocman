import type { ComponentProps } from 'react';
import { Composer } from '../../components/assistant/Composer';
import { PermissionPrompt } from '../../components/session/PermissionPrompt';
import { QuestionPrompt } from '../../components/session/QuestionPrompt';
import { FactoryPlanApproval } from '../../components/FactoryPlanApproval';
import { ErrorBoundary } from '../../components/ErrorBoundary';

export interface SessionComposerSlotProps {
  sessionId: string;
  platformId: string;
  factoryEpicID: string;
  /** Unread pill; hidden when null / 0. */
  firstUnreadMessageId: string | null;
  unreadMessageCount: number;
  onJumpToUnread: (messageId: string) => void;
  /** Exactly one of these renders, in this priority order. */
  permission: ComponentProps<typeof PermissionPrompt> | null;
  question: ComponentProps<typeof QuestionPrompt> | null;
  composer: ComponentProps<typeof Composer> | null;
}

/**
 * What sits below the thread: the Factory approval card, the unread
 * pill, and then whichever of permission prompt / question prompt /
 * composer the session currently needs.
 */
export function SessionComposerSlot({
  sessionId,
  platformId,
  factoryEpicID,
  firstUnreadMessageId,
  unreadMessageCount,
  onJumpToUnread,
  permission,
  question,
  composer,
}: SessionComposerSlotProps) {
  return (
    <ErrorBoundary name="session:composer" inline resetKey={sessionId}>
      <FactoryPlanApproval epicID={factoryEpicID} platformID={platformId} sessionID={sessionId} />
      {firstUnreadMessageId && unreadMessageCount > 0 && (
        <button
          type="button"
          className="oc-jump-unread"
          data-testid="jump-to-first-unread"
          onClick={() => onJumpToUnread(firstUnreadMessageId)}
          title="Scroll to the first message you haven't seen yet"
        >
          <i className="bi bi-arrow-up" aria-hidden="true" />
          {' '}
          {unreadMessageCount} new message{unreadMessageCount === 1 ? '' : 's'}
        </button>
      )}
      {permission ? (
        <PermissionPrompt {...permission} />
      ) : question ? (
        <QuestionPrompt {...question} />
      ) : composer ? (
        <Composer {...composer} />
      ) : null}
    </ErrorBoundary>
  );
}

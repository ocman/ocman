import type { ComponentProps } from 'react';
import { Composer } from '../../components/assistant/Composer';
import { PermissionPrompt } from '../../components/session/PermissionPrompt';
import { QuestionPrompt } from '../../components/session/QuestionPrompt';
import { FactoryPlanApproval } from '../../components/FactoryPlanApproval';
import { FactorySessionRecovery } from '../../components/FactorySessionRecovery';
import { ErrorBoundary } from '../../components/ErrorBoundary';
import { FirstSubmissionNotice } from './FirstSubmissionNotice';
import { LaunchProgressCard } from '../../components/LaunchProgressCard';
import { getFirstSubmission, useFirstSubmission } from './firstSubmission';

export interface SessionComposerSlotProps {
  sessionId: string;
  platformId: string;
  /** Session directory + owner; scope the launch progress card. */
  directory?: string;
  remoteId?: string;
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
  directory,
  remoteId,
  factoryEpicID,
  firstUnreadMessageId,
  unreadMessageCount,
  onJumpToUnread,
  permission,
  question,
  composer,
}: SessionComposerSlotProps) {
  const livePending = useFirstSubmission((state) => !!state.entries[sessionId]?.pending);
  const firstPending = getFirstSubmission(sessionId)?.pending || livePending;
  return (
    <ErrorBoundary name="session:composer" inline resetKey={sessionId}>
      <FactoryPlanApproval epicID={factoryEpicID} platformID={platformId} sessionID={sessionId} />
      <FactorySessionRecovery key={`${platformId}/${sessionId}`} platformID={platformId} sessionID={sessionId} />
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
      {/* Above the prompt branches: a launch can start while a prompt is shown. */}
      <LaunchProgressCard directory={directory} remoteId={remoteId} />
      {permission ? (
        <PermissionPrompt {...permission} />
      ) : question ? (
        <QuestionPrompt {...question} />
      ) : composer ? (
        <>
          <FirstSubmissionNotice sessionId={sessionId} />
          <Composer {...composer}
            platform={platformId}
            disabled={composer.disabled || firstPending}
            disabledHint={firstPending ? 'Waiting for the first submission…' : composer.disabledHint}
            onLaunchRequest={firstPending ? undefined : composer.onLaunchRequest} />
        </>
      ) : null}
    </ErrorBoundary>
  );
}

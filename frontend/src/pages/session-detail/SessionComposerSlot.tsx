import type { ComponentProps } from 'react';
import { Composer } from '../../components/assistant/Composer';
import { PermissionPrompt } from '../../components/session/PermissionPrompt';
import { QuestionPrompt } from '../../components/session/QuestionPrompt';
import { FactoryPlanApproval } from '../../components/FactoryPlanApproval';
import { FactorySessionRecovery } from '../../components/FactorySessionRecovery';
import { ErrorBoundary } from '../../components/ErrorBoundary';
import { FirstSubmissionNotice } from './FirstSubmissionNotice';
import { LaunchProgressCard } from '../../components/LaunchProgressCard';
import { useFirstSubmission } from './firstSubmission';

export interface SessionComposerSlotProps {
  sessionId: string;
  platformId: string;
  /** Session directory + owner; scope the launch progress card. */
  directory?: string;
  remoteId?: string;
  factoryEpicID: string;
  firstUnreadMessageId: string | null;
  unreadMessageCount: number;
  onJumpToUnread: (messageId: string) => void;
  pendingPrompt?: boolean;
  composer: ComponentProps<typeof Composer> | null;
}

export function SessionPromptSlot({ permission, question }: {
  permission: ComponentProps<typeof PermissionPrompt> | null;
  question: ComponentProps<typeof QuestionPrompt> | null;
}) {
  return permission ? <PermissionPrompt {...permission} />
    : question ? <QuestionPrompt {...question} /> : null;
}

/**
 * What sits below the thread: Factory cards and composer.
 * Pending prompts render inside the conversation viewport instead.
 */
export function SessionComposerSlot({
  sessionId,
  platformId,
  directory,
  remoteId,
  factoryEpicID,
  pendingPrompt,
  composer,
}: SessionComposerSlotProps) {
  const firstPending = useFirstSubmission((state) => !!state.entries[sessionId]?.pending);
  return (
    <ErrorBoundary name="session:composer" inline resetKey={sessionId}>
      <FactoryPlanApproval epicID={factoryEpicID} platformID={platformId} sessionID={sessionId} />
      <FactorySessionRecovery key={`${platformId}/${sessionId}`} platformID={platformId} sessionID={sessionId} />
      {/* Above the prompt branches: a launch can start while a prompt is shown. */}
      <LaunchProgressCard directory={directory} remoteId={remoteId} />
      {!pendingPrompt && composer ? (
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

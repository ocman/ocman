import type { ComponentProps } from 'react';
import { Composer } from '../../components/assistant/Composer';
import { PermissionPrompt } from '../../components/session/PermissionPrompt';
import { QuestionPrompt } from '../../components/session/QuestionPrompt';
import { ErrorBoundary } from '../../components/ErrorBoundary';
import { FirstSubmissionNotice } from './FirstSubmissionNotice';
import { LaunchProgressCard } from '../../components/LaunchProgressCard';
import { useSessionFirstSubmission } from './firstSubmission';

export interface SessionComposerSlotProps {
  sessionId: string;
  platformId: string;
  /** Session directory + owner; scope the launch progress card. */
  directory?: string;
  remoteId?: string;
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
 * What sits below the thread: launch progress and composer.
 * Factory cards and pending prompts render inside the conversation viewport.
 */
export function SessionComposerSlot({
  sessionId,
  platformId,
  directory,
  remoteId,
  pendingPrompt,
  composer,
}: SessionComposerSlotProps) {
  const firstPending = useSessionFirstSubmission(sessionId)?.pending;
  return (
    <ErrorBoundary name="session:composer" inline resetKey={sessionId}>
      {/* Keep the composer mounted so incoming prompts cannot destroy open dialogs. */}
      <LaunchProgressCard directory={directory} remoteId={remoteId} />
      {composer ? (
        <>
          <FirstSubmissionNotice sessionId={sessionId} />
          <Composer {...composer}
            platform={platformId}
            disabled={composer.disabled || pendingPrompt || firstPending}
            disabledHint={firstPending ? 'Waiting for the first submission…' : composer.disabledHint}
            onLaunchRequest={firstPending ? undefined : composer.onLaunchRequest} />
        </>
      ) : null}
    </ErrorBoundary>
  );
}

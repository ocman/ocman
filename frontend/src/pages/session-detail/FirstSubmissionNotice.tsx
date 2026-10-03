import { InlineAlert } from '../../components/InlineAlert';
import { startFirstSubmission, useFirstSubmission } from './firstSubmission';

export function FirstSubmissionNotice({ sessionId }: { sessionId: string }) {
  const submission = useFirstSubmission((state) => state.entries[sessionId]);
  if (!submission) return null;
  if (submission.pending) return <div role="status">Sending first submission…</div>;
  return <InlineAlert onRetry={() => startFirstSubmission(sessionId, submission.text, submission.execute)}>
    {submission.error} First submission: <code>{submission.text}</code>
  </InlineAlert>;
}

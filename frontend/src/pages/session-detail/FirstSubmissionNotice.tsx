import { InlineAlert } from '../../components/InlineAlert';
import { discardFirstSubmission, reconcileFirstSubmission, startFirstSubmission, useFirstSubmission } from './firstSubmission';
import { Button } from '../../components/Control';
import { useState } from 'react';

export function FirstSubmissionNotice({ sessionId }: { sessionId: string }) {
  const submission = useFirstSubmission((state) => state.entries[sessionId]);
  const [recoveryError, setRecoveryError] = useState('');
  const recover = (action: () => Promise<void>) => {
    setRecoveryError('');
    void action().catch((error: unknown) => setRecoveryError(error instanceof Error ? error.message : String(error)));
  };
  if (!submission) return null;
  if (submission.pending && !submission.error) return <div role="status">Sending first submission…</div>;
  return <InlineAlert onRetry={() => recover(submission.execute && !submission.pending ? () => startFirstSubmission(sessionId, submission.text, submission.execute!) : () => reconcileFirstSubmission(sessionId))}>
    {recoveryError || submission.error} First submission: <code>{submission.text}</code>
    {!submission.execute && ' Retry in the originating tab, which retains the attachment or command payload.'}
    {!submission.execute && submission.canRelease && <Button onClick={() => { if (window.confirm('The first delivery may already have run. Release this lock without resending it?')) recover(() => discardFirstSubmission(sessionId)); }}>Release first-delivery lock</Button>}
  </InlineAlert>;
}

import { useRef, useState } from 'react';
import type { BackendConversationDraft } from '../../lib/backendConversationDrafts';
import { InlineAlert } from '../../components/InlineAlert';
import { CopyButton } from '../../components/CopyButton';

/** Keep long checkout logs from pushing the saved first message out of view. */
export function DraftSetupError({ record, retry }: { record: BackendConversationDraft; retry: () => Promise<void> }) {
  const [retrying, setRetrying] = useState(false);
  const [retryError, setRetryError] = useState('');
  const locked = useRef(false);
  const canRetry = record.setupRetryable || record.result?.sessionId === record.id;
  const prompt = record.request?.prompt || record.request?.send?.message || '';
  const runRetry = async () => {
    if (locked.current) return;
    locked.current = true;
    setRetrying(true);
    setRetryError('');
    try { await retry(); }
    catch (error) { setRetryError(error instanceof Error ? error.message : String(error)); }
    finally { locked.current = false; setRetrying(false); }
  };
  return <>
    <InlineAlert onRetry={canRetry ? () => { void runRetry(); } : undefined} retrying={retrying}>
      {retryError || record.error?.split('\n', 1)[0] || 'Session setup failed.'}
      {' Your submitted message is saved above.'}
      {!canRetry && ' Session creation may have started, so setup cannot be safely repeated.'}
      {prompt && <CopyButton text={prompt} label="Copy submitted message" size="small" variant="ghost" />}
    </InlineAlert>
    <details><summary>Error details</summary><div style={{ whiteSpace: 'pre-wrap', maxHeight: '12rem', overflow: 'auto' }}>{record.error}</div></details>
  </>;
}

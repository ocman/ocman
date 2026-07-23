import { useCallback, useEffect, useState } from 'react';
import { api, type DaguStatus as DaguStatusResult } from '../lib/api';
import './DaguStatus.css';

export function DaguStatus({ remoteId = 'local' }: { remoteId?: string }) {
  const [result, setResult] = useState<DaguStatusResult>();
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);

  const check = useCallback(async (signal?: AbortSignal) => {
    setBusy(true);
    setError('');
    try {
      setResult(await api.dagu.status(remoteId, signal));
    } catch (reason) {
      if (!(reason instanceof DOMException && reason.name === 'AbortError'))
        setError(reason instanceof Error ? reason.message : 'Could not check Dagu');
    } finally {
      if (!signal?.aborted) setBusy(false);
    }
  }, [remoteId]);

  useEffect(() => {
    const controller = new AbortController();
    void check(controller.signal);
    return () => controller.abort();
  }, [check]);

  let message = 'Checking Dagu...';
  if (result?.status === 'unavailable') message = 'Dagu is not installed';
  if (result?.status === 'compatible') message = `Dagu ${result.version} is compatible`;
  if (result?.status === 'unsupported') message = result.version
    ? `Dagu ${result.version} is unsupported`
    : 'The installed Dagu version is unsupported';

  return (
    <section className="dagu-status" aria-label="Dagu compatibility">
      <div>
        <strong>{message}</strong>
        {result?.status !== 'compatible' && result?.installCommand && <>
          <p>Dagu 2.x is optional and installed separately. Prompt scheduling remains available.</p>
          <code>{result.installCommand}</code>
        </>}
        {error && <p role="alert">{error}</p>}
      </div>
      <button className="oc-time-range-btn" disabled={busy} onClick={() => void check()}>Recheck Dagu</button>
    </section>
  );
}

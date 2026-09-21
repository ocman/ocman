import { useCallback, useEffect, useState } from 'react';
import { plugins, type PluginBacklog, type PluginRegistration } from '../lib/plugins';
import { SettingRow } from './SettingRow';

function age(since: number) {
  if (!since) return 'none waiting';
  const seconds = Math.max(0, Math.round((Date.now() - since) / 1000));
  if (seconds < 60) return `${seconds}s`;
  if (seconds < 3600) return `${Math.round(seconds / 60)}m`;
  return `${Math.round(seconds / 3600)}h`;
}

/**
 * PluginDeliveryBacklog is the actionable view of what a conversation plugin
 * still owes its provider: undelivered replies, whether new work is paused by
 * the backlog limits, and the dead letters that need an explicit retry or
 * discard. Nothing here retries on its own — that is the host's job; this is
 * where a human decides about the ones it gave up on.
 */
export function PluginDeliveryBacklog({ plugin, owner }: { plugin: PluginRegistration; owner: string }) {
  const id = plugin.description.id;
  const [backlog, setBacklog] = useState<PluginBacklog | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');

  const load = useCallback(async (signal?: AbortSignal) => {
    try {
      const result = await plugins.backlog(owner, id, signal);
      if (!signal?.aborted) { setBacklog(result); setError(''); }
    } catch {
      if (!signal?.aborted) setError('Could not load reply delivery status for this owner.');
    }
  }, [owner, id]);

  useEffect(() => {
    const controller = new AbortController();
    void load(controller.signal);
    return () => controller.abort();
  }, [load]);

  async function decide(action: 'conversations/retry' | 'conversations/discard', deliveryId: number) {
    setBusy(true);
    setError('');
    try {
      setBacklog(await plugins.mutate(owner, id, action, { deliveryId }) as PluginBacklog);
    } catch {
      // Reload first: a refused decision must leave the real backlog on
      // screen, and the reload's own success clears the error banner.
      await load();
      setError(`Could not ${action.endsWith('retry') ? 'retry' : 'discard'} that reply. Reload the status and try again.`);
    } finally { setBusy(false); }
  }

  const deadLetters = backlog?.deadLetters ?? [];
  return <fieldset disabled={busy} style={{ border: 0, padding: 0 }}>
    <legend>Reply delivery</legend>
    <SettingRow label="Undelivered replies"
      desc="Completed replies are stored before they are sent, so a disconnect or a restart retries them instead of losing them.">
      {backlog
        ? <>{backlog.pending} waiting · {backlog.retrying} retrying · {backlog.dead} needing a decision</>
        : <span role="status">Loading…</span>}
      <button type="button" className="vscode-btn" onClick={() => { void load(); }}>Reload status</button>
    </SettingRow>
    {backlog && <SettingRow label="Backlog limits" desc="At either limit, new conversation work pauses instead of replies being dropped.">
      {backlog.pending + backlog.dead} of {backlog.maxRows} replies · {Math.round(backlog.bytes / 1024)} of {Math.round(backlog.maxBytes / 1024)} KiB · oldest {age(backlog.oldestUnsent)}
    </SettingRow>}
    {backlog?.paused && <p role="alert">New conversation messages are paused: the reply backlog is at its limit. Retry or discard the replies below, or wait for delivery to catch up.</p>}
    {error && <p role="alert">{error}</p>}
    {deadLetters.length > 0 && <SettingRow label="Replies awaiting a decision" desc="These exhausted their retries. Later replies in the same conversation wait until each one is retried or discarded." block>
      <ul>
        {deadLetters.map((letter) => <li key={letter.id}>
          <code>{letter.threadId}</code> in <code>{letter.accountId}</code> · {letter.attempts} attempts · {letter.lastError}
          <button type="button" className="vscode-btn" onClick={() => { void decide('conversations/retry', letter.id); }}>
            Retry delivery
          </button>
          <button type="button" className="vscode-btn" onClick={() => { void decide('conversations/discard', letter.id); }}>
            Discard reply
          </button>
        </li>)}
      </ul>
    </SettingRow>}
  </fieldset>;
}

import { useCallback, useEffect, useState } from 'react';
import { maintenance, type MaintenanceStatus } from '../lib/maintenance';
import { SettingRow } from './SettingRow';
import './MaintenanceSettings.css';

const POLL_MS = 1000;

function gb(bytes: number) {
  return `${(bytes / 1e9).toFixed(1)} GB`;
}

const stepIcon: Record<string, string> = {
  pending: 'bi-circle',
  running: 'bi-arrow-repeat',
  done: 'bi-check-circle-fill',
  failed: 'bi-x-circle-fill',
  skipped: 'bi-dash-circle',
};

export function MaintenanceSettings() {
  const [status, setStatus] = useState<MaintenanceStatus | null>(null);
  const [error, setError] = useState('');
  const refresh = useCallback((signal?: AbortSignal) => maintenance.status(signal).then((next) => {
    setStatus(next);
    setError('');
  }).catch((err: unknown) => {
    if (!signal?.aborted) setError(err instanceof Error ? err.message : String(err));
  }), []);

  useEffect(() => {
    const controller = new AbortController();
    void refresh(controller.signal);
    return () => controller.abort();
  }, [refresh]);

  const running = status?.job.running ?? false;
  useEffect(() => {
    if (!running) return;
    const id = window.setInterval(() => { void refresh(); }, POLL_MS);
    return () => window.clearInterval(id);
  }, [running, refresh]);

  async function act(question: string, action: () => Promise<MaintenanceStatus>) {
    if (!window.confirm(question)) return;
    try {
      setStatus(await action());
      setError('');
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    }
  }

  if (!status) {
    return error ? <p role="alert">{error}</p> : <p role="status">Loading…</p>;
  }
  if (!status.available) {
    return <p role="status">OpenCode database maintenance needs the OpenCode platform enabled.</p>;
  }
  const hasDump = status.dumpBytes > 0;
  const { job } = status;
  return <>
    <SettingRow label="OpenCode database" desc={<><code>{status.dbPath}</code> · {gb(status.dbBytes)}</>}>
      <button type="button" className="vscode-btn" disabled={running} onClick={() => { void refresh(); }}>Refresh</button>
    </SettingRow>
    <SettingRow
      label="Remove old diffs"
      desc={<>OpenCode keeps a full patch of every changed file on each user message, and repeats it in its event log.
        This removes those patches from sessions not updated for {status.cutoffDays} days, then compacts the database.
        Only OpenCode&rsquo;s web per-turn changes view reads them. ocman stops its managed opencode instances
        (in-flight turns are interrupted) and relaunches them afterwards. It refuses to start while any other process,
        such as an opencode you started yourself, has the database open. A full backup exists while the job runs;
        the removed patches are kept in a dump so they can be restored.</>}
    >
      <button
        type="button"
        className="vscode-btn"
        disabled={running}
        onClick={() => { void act(`Stop opencode and remove diffs older than ${status.cutoffDays} days?`, maintenance.cleanup); }}
      >Clean up</button>
    </SettingRow>
    <SettingRow
      label="Removed diffs"
      desc={hasDump ? <><code>{status.dumpPath}</code> · {gb(status.dumpBytes)}</> : 'No dump yet.'}
    >
      <button
        type="button"
        className="vscode-btn"
        disabled={running || !hasDump}
        onClick={() => { void act('Stop opencode and put the removed diffs back?', maintenance.restore); }}
      >Restore</button>
      <button
        type="button"
        className="vscode-btn"
        disabled={running || !hasDump}
        onClick={() => { void act('Delete the dump? The removed diffs can no longer be restored.', maintenance.deleteDump); }}
      >Delete dump</button>
    </SettingRow>
    {error && <p role="alert">{error}</p>}
    {job.steps.length > 0 && (
      <ol className="maintenance-steps" aria-label={`${job.job ?? ''} progress`}>
        {job.steps.map((step) => (
          <li key={step.name} data-state={step.state}>
            <i className={`bi ${stepIcon[step.state]}`} role="img" aria-label={step.state} /> {step.name}
            {step.detail && <small> — {step.detail}</small>}
          </li>
        ))}
      </ol>
    )}
    {!running && job.error && <p role="alert">{job.error}</p>}
    {!running && job.finishedAt && !job.error && <p role="status">Finished.</p>}
  </>;
}

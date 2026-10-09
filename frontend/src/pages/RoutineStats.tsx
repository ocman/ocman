import { useEffect, useRef, useState } from 'react';
import { DataTable } from '../components/DataTable';
import { InlineAlert } from '../components/InlineAlert';
import { LoadingState } from '../components/LoadingState';
import { api, type RoutineStatsData } from '../lib/api';
import { useDocumentVisible } from '../lib/usePanelVisible';

const money = (amount: number) => `$${amount.toFixed(4)}`;

export function RoutineStats({ routineId, refreshKey }: { routineId: string; refreshKey: number }) {
  const visible = useDocumentVisible();
  const [stats, setStats] = useState<RoutineStatsData>();
  const [error, setError] = useState('');
  const request = useRef<AbortController | null>(null);
  const refreshedAt = useRef<number | null>(null);
  useEffect(() => () => { request.current?.abort(); request.current = null; }, [routineId, visible]);
  useEffect(() => {
    // Billing scans lifetime usage, so refresh at most every 30s while open.
    if (!visible || request.current || (refreshedAt.current !== null && Date.now() - refreshedAt.current < 30_000)) return;
    const controller = new AbortController();
    request.current = controller;
    api.routines.stats(routineId, controller.signal).then((data) => {
      if (!controller.signal.aborted) { refreshedAt.current = Date.now(); setStats(data); setError(''); }
    }).catch((err: Error) => { if (!controller.signal.aborted) setError(err.message); })
      .finally(() => { if (request.current === controller) request.current = null; });
  }, [routineId, refreshKey, visible]);

  return <section aria-label="Routine stats">
    {error && <InlineAlert>{error}</InlineAlert>}
    {!stats && !error && <LoadingState>Loading stats...</LoadingState>}
    {stats && <>
      <DataTable framed><tbody>
        <tr><th>Total runs</th><td>{stats.totalRuns}</td></tr>
        {['success', 'failure', 'interrupted', 'running'].map((state) => <tr key={state}><th>{({ success: 'Successful', failure: 'Failed', interrupted: 'Interrupted', running: 'Running' })[state]}</th><td>{stats.states[state] ?? 0}</td></tr>)}
        <tr><th>Average duration</th><td>{stats.averageDurationMs === null ? 'Unavailable' : `${(stats.averageDurationMs / 1000).toFixed(1)} s`}</td></tr>
        <tr><th>Total session cost</th><td>{stats.costSessions ? money(stats.totalCost) : 'Unavailable'}</td></tr>
        <tr><th>Average session cost</th><td>{stats.costSessions ? money(stats.totalCost / stats.costSessions) : 'Unavailable'}</td></tr>
        <tr><th>Estimated total session cost</th><td>{stats.costSessions ? money(stats.totalEstCost) : 'Unavailable'}</td></tr>
      </tbody></DataTable>
      <p>Duration uses finished runs with recorded start and end times. Costs cover {stats.costSessions} linked sessions and their subagents. Reused sessions count once and include their full lifetime usage.</p>
      {stats.missingSessions > 0 && <p role="status">Costs unavailable for {stats.missingSessions} linked sessions.</p>}
    </>}
  </section>;
}

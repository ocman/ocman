import { useEffect, useRef, useState } from 'react';
import { useDocumentVisible } from '../lib/usePanelVisible';
import { Link } from 'react-router-dom';
import { Button } from '../components/Control';
import { DataTable } from '../components/DataTable';
import { EmptyState } from '../components/EmptyState';
import { Drawer } from '../components/Drawer';
import { RoutineStateBadge } from '../components/RoutineStateBadge';
import { api, type Routine, type RoutineRun } from '../lib/api';
import { formatDateTimeShort } from '../lib/format';
import styles from './RoutineHistoryDrawer.module.css';

const HISTORY_PAGE_SIZE = 50;

const olderThan = (run: RoutineRun, last: RoutineRun) => run.createdAt < last.createdAt || (run.createdAt === last.createdAt && run.id < last.id);

type Shown = { runs: RoutineRun[]; exhausted: boolean };

/**
 * Replace the newest page. Older pages already loaded are kept only when the
 * new page overlaps them; otherwise (more than a page of new runs) they would
 * leave an unreachable gap, so the view resets to the newest page.
 */
function mergeNewestPage(newest: RoutineRun[], shown: Shown): Shown {
  if (newest.length < HISTORY_PAGE_SIZE) return { runs: newest, exhausted: true };
  const ids = new Set(newest.map((run) => run.id));
  if (!shown.runs.some((run) => ids.has(run.id))) return { runs: newest, exhausted: false };
  const last = newest[newest.length - 1];
  return { runs: [...newest, ...shown.runs.filter((run) => !ids.has(run.id) && olderThan(run, last))], exhausted: shown.exhausted };
}

/**
 * Run state and session linkage change until a run settles. Retained rows past
 * the newest page are otherwise never refetched, so re-read the pages covering
 * any still-running ones (one bounded request per 50 rows), oldest-first cursor.
 */
async function refetchRunning(routineId: string, runs: RoutineRun[], from: number, signal: AbortSignal): Promise<Map<string, RoutineRun>> {
  const updates = new Map<string, RoutineRun>();
  let i = runs.findIndex((run, index) => index >= from && run.state === 'running');
  while (i > 0 && !signal.aborted && !document.hidden) {
    const page = await api.routines.history(routineId, { limit: HISTORY_PAGE_SIZE, before: runs[i - 1] }, signal);
    for (const run of page) updates.set(run.id, run);
    if (page.length < HISTORY_PAGE_SIZE) break;
    const covered = i + page.length;
    i = runs.findIndex((run, index) => index >= covered && run.state === 'running');
  }
  return updates;
}

type Props = {
  routine: Routine;
  /** Bumped by the page after each list refresh; refetches the newest page. */
  refreshKey: number;
  onClose: () => void;
};

// Mount with key={routine.id} so switching routines starts a fresh history.
export function RoutineHistoryDrawer({ routine, refreshKey, onClose }: Props) {
  const visible = useDocumentVisible();
  const [shown, setShown] = useState<Shown>({ runs: [], exhausted: false });
  const { runs, exhausted } = shown;
  const shownRef = useRef(shown);
  useEffect(() => { shownRef.current = shown; }, [shown]);
  const [loaded, setLoaded] = useState(false);
  const [loadingOlder, setLoadingOlder] = useState(false);
  const [error, setError] = useState('');
  const mounted = useRef(true);
  const refreshRequest = useRef<AbortController | null>(null);
  useEffect(() => { mounted.current = true; return () => { mounted.current = false; refreshRequest.current?.abort(); }; }, []);
  useEffect(() => {
    if (!visible) { refreshRequest.current?.abort(); refreshRequest.current = null; }
  }, [visible]);

  // One newest-page fetch at a time: a refresh that lands mid-flight is skipped
  // (the next one catches up), so a slow response is never discarded.
  useEffect(() => {
    if (!visible || document.hidden || refreshRequest.current) return;
    const controller = new AbortController();
    refreshRequest.current = controller;
    const refresh = async () => {
      const newest = await api.routines.history(routine.id, { limit: HISTORY_PAGE_SIZE }, controller.signal);
      if (!mounted.current || controller.signal.aborted || document.hidden) return;
      const merged = mergeNewestPage(newest, shownRef.current);
      setShown((current) => mergeNewestPage(newest, current));
      setLoaded(true);
      setError('');
      const updates = await refetchRunning(routine.id, merged.runs, newest.length, controller.signal);
      if (mounted.current && !controller.signal.aborted && updates.size > 0) setShown((current) => ({ ...current, runs: current.runs.map((run) => updates.get(run.id) ?? run) }));
    };
    refresh().catch((err: unknown) => { if (mounted.current && !controller.signal.aborted) setError(err instanceof Error ? err.message : 'Could not load history.'); })
      .finally(() => { if (refreshRequest.current === controller) refreshRequest.current = null; });
  }, [routine.id, refreshKey, visible]);

  const loadOlder = async () => {
    const before = runs[runs.length - 1];
    if (!before) return;
    setLoadingOlder(true);
    try {
      const page = await api.routines.history(routine.id, { limit: HISTORY_PAGE_SIZE, before });
      if (!mounted.current) return;
      // Drop the page if a refresh reset the view meanwhile; it would not be contiguous.
      setShown((shown) => shown.runs[shown.runs.length - 1]?.id !== before.id ? shown : { runs: [...shown.runs, ...page], exhausted: page.length < HISTORY_PAGE_SIZE });
    } catch (err) {
      if (mounted.current) setError(err instanceof Error ? err.message : 'Could not load older runs.');
    } finally {
      if (mounted.current) setLoadingOlder(false);
    }
  };

  return (
    <Drawer title={`${routine.name} history`} onClose={onClose} closeLabel="Close routine history" backdropTestId="routine-drawer-backdrop">
      <div className={styles.content}>
        <p className={styles.next}>Next run: {routine.nextDueAt ? formatDateTimeShort(routine.nextDueAt) : '-'}</p>
        {error && <p role="alert" className={styles.error}>{error}</p>}
        <section className={styles.history} aria-labelledby="routine-history-heading">
          <h3 id="routine-history-heading">History</h3>
          {!loaded && !error ? <div className="oc-list-loading" role="status"><div className="oc-spinner" />Loading history...</div> : runs.length === 0 ? <EmptyState>No runs yet.</EmptyState> : (
            <DataTable framed><thead><tr><th>Started</th><th>Trigger</th><th>Status</th><th>Session</th></tr></thead><tbody>{runs.map((run) => <tr key={run.id}><td>{formatDateTimeShort(run.startedAt || run.createdAt)}</td><td>{run.trigger}</td><td><RoutineStateBadge state={run.state} />{run.error && <small className={styles.error}>{run.error}</small>}</td><td>{run.sessionId ? <Link to={`/session/${encodeURIComponent(run.sessionId)}?platform=${encodeURIComponent(run.platform ?? '')}`}>Open</Link> : '-'}</td></tr>)}</tbody></DataTable>
          )}
          {loaded && !exhausted && runs.length > 0 && <Button type="button" disabled={loadingOlder} onClick={() => void loadOlder()}>{loadingOlder ? 'Loading...' : 'Load older runs'}</Button>}
        </section>
      </div>
    </Drawer>
  );
}

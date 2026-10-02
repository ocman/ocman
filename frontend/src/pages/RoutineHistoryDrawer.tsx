import { useEffect, useRef, useState } from 'react';
import { Link } from 'react-router-dom';
import { Button } from '../components/Control';
import { DataTable } from '../components/DataTable';
import { EmptyState } from '../components/EmptyState';
import { Modal } from '../components/Modal';
import { ModalHeader } from '../components/ModalHeader';
import { api, type Routine, type RoutineRun } from '../lib/api';
import { formatDateTimeShort } from '../lib/format';

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

type Props = {
  routine: Routine;
  /** Bumped by the page after each list refresh; refetches the newest page. */
  refreshKey: number;
  onClose: () => void;
};

// Mount with key={routine.id} so switching routines starts a fresh history.
export function RoutineHistoryDrawer({ routine, refreshKey, onClose }: Props) {
  const [{ runs, exhausted }, setShown] = useState<Shown>({ runs: [], exhausted: false });
  const [loaded, setLoaded] = useState(false);
  const [loadingOlder, setLoadingOlder] = useState(false);
  const [error, setError] = useState('');
  const mounted = useRef(true);
  const refreshing = useRef(false);
  useEffect(() => { mounted.current = true; return () => { mounted.current = false; }; }, []);

  // One newest-page fetch at a time: a refresh that lands mid-flight is skipped
  // (the next one catches up), so a slow response is never discarded.
  useEffect(() => {
    if (refreshing.current) return;
    refreshing.current = true;
    api.routines.history(routine.id, { limit: HISTORY_PAGE_SIZE }).then((newest) => {
      if (!mounted.current) return;
      setShown((shown) => mergeNewestPage(newest, shown));
      setLoaded(true);
      setError('');
    }, (err: unknown) => { if (mounted.current) setError(err instanceof Error ? err.message : 'Could not load history.'); })
      .finally(() => { refreshing.current = false; });
  }, [routine.id, refreshKey]);

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
    <Modal label={`${routine.name} history`} onClose={onClose} backdropClassName="routine-drawer-backdrop" dialogClassName="routine-drawer" backdropTestId="routine-drawer-backdrop">
      <div className="routine-form">
        <ModalHeader title={routine.name} onClose={onClose} closeLabel="Close routine history" />
        <p className="routine-detail-next">Next run: {routine.nextDueAt ? formatDateTimeShort(routine.nextDueAt) : '-'}</p>
        {error && <p role="alert" className="routine-error">{error}</p>}
        <section className="routine-detail-history" aria-labelledby="routine-history-heading">
          <h3 id="routine-history-heading">History</h3>
          {!loaded && !error ? <div className="oc-list-loading" role="status"><div className="oc-spinner" />Loading history...</div> : runs.length === 0 ? <EmptyState>No runs yet.</EmptyState> : (
            <DataTable framed><thead><tr><th>Started</th><th>Trigger</th><th>Status</th><th>Session</th></tr></thead><tbody>{runs.map((run) => <tr key={run.id}><td>{formatDateTimeShort(run.startedAt || run.createdAt)}</td><td>{run.trigger}</td><td><span className={`routine-state ${run.state}`}>{run.state}</span>{run.error && <small className="routine-error">{run.error}</small>}</td><td>{run.sessionId ? <Link to={`/session/${encodeURIComponent(run.sessionId)}?platform=${encodeURIComponent(run.platform ?? '')}`}>Open</Link> : '-'}</td></tr>)}</tbody></DataTable>
          )}
          {loaded && !exhausted && runs.length > 0 && <Button type="button" disabled={loadingOlder} onClick={() => void loadOlder()}>{loadingOlder ? 'Loading...' : 'Load older runs'}</Button>}
        </section>
      </div>
    </Modal>
  );
}

import { useEffect, useState } from 'react';
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

/** Replace the newest page, keeping any older pages the user already loaded. */
function mergeNewestPage(newest: RoutineRun[], shown: RoutineRun[]): RoutineRun[] {
  const last = newest[newest.length - 1];
  if (newest.length < HISTORY_PAGE_SIZE || !last) return newest;
  const ids = new Set(newest.map((run) => run.id));
  return [...newest, ...shown.filter((run) => !ids.has(run.id) && olderThan(run, last))];
}

type Props = {
  routine: Routine;
  /** Bumped by the page after each list refresh; refetches the newest page. */
  refreshKey: number;
  onClose: () => void;
};

// Mount with key={routine.id} so switching routines starts a fresh history.
export function RoutineHistoryDrawer({ routine, refreshKey, onClose }: Props) {
  const [runs, setRuns] = useState<RoutineRun[]>([]);
  const [loaded, setLoaded] = useState(false);
  const [exhausted, setExhausted] = useState(false);
  const [loadingOlder, setLoadingOlder] = useState(false);
  const [error, setError] = useState('');

  useEffect(() => {
    let active = true;
    api.routines.history(routine.id, { limit: HISTORY_PAGE_SIZE }).then((newest) => {
      if (!active) return;
      setRuns((shown) => mergeNewestPage(newest, shown));
      if (newest.length < HISTORY_PAGE_SIZE) setExhausted(true);
      setLoaded(true);
      setError('');
    }, (err: unknown) => { if (active) setError(err instanceof Error ? err.message : 'Could not load history.'); });
    return () => { active = false; };
  }, [routine.id, refreshKey]);

  const loadOlder = async () => {
    const before = runs[runs.length - 1];
    if (!before) return;
    setLoadingOlder(true);
    try {
      const page = await api.routines.history(routine.id, { limit: HISTORY_PAGE_SIZE, before });
      setRuns((shown) => [...shown, ...page.filter((run) => olderThan(run, shown[shown.length - 1] ?? before))]);
      if (page.length < HISTORY_PAGE_SIZE) setExhausted(true);
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Could not load older runs.');
    } finally {
      setLoadingOlder(false);
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

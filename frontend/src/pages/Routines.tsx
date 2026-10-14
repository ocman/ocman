import { useState } from 'react';
import { useSearchParams } from 'react-router-dom';
import { Button, ButtonGroup } from '../components/Control';
import { EmptyState } from '../components/EmptyState';
import { ProjectLabel } from '../components/ProjectLabel';
import { DataTable } from '../components/DataTable';
import { RoutineStateBadge } from '../components/RoutineStateBadge';
import { Tabs, TabsContent, TabsList, TabsTrigger } from '../components/Tabs';
import { api, type Routine } from '../lib/api';
import type { WebhookInbox } from '../lib/api.types';
import { useRoutines, useWebhookInboxes } from '../lib/routineQueries';
import { RoutineHistoryDrawer } from './RoutineHistoryDrawer';
import { RoutineEditorDrawer } from './RoutineEditorDrawer';
import { WebhookInboxDrawer } from './WebhookInboxDrawer';
import { triggerLabel } from '../lib/webhookFilters';
import { formatDateTimeShort } from '../lib/format';
import { usePageTitle } from '../lib/headerContext';
import styles from './Routines.module.css';

const EMPTY_ROUTINES: Routine[] = [];
const EMPTY_INBOXES: WebhookInbox[] = [];

export function Routines() {
  usePageTitle('Routines');
  const [searchParams, setSearchParams] = useSearchParams();
  const tab = searchParams.get('tab') === 'inboxes' ? 'inboxes' : 'routines';
  // Both lists poll while the page is visible (react-query pauses hidden tabs).
  const routinesQ = useRoutines();
  const inboxesQ = useWebhookInboxes();
  const routines = routinesQ.data ?? EMPTY_ROUTINES;
  const inboxes = inboxesQ.data ?? EMPTY_INBOXES;
  // Bumps on every successful read so the open history drawer refreshes too.
  const refreshKey = routinesQ.dataUpdatedAt;
  const historyId = searchParams.get('routine') ?? undefined;
  const view = searchParams.get('view');
  const detailTab = view === 'settings' || view === 'stats' ? view : 'history';
  const selectRoutine = (id?: string, view = 'history') => setSearchParams((previous) => {
    const next = new URLSearchParams(previous);
    next.delete('routine'); next.delete('view');
    if (id) { next.set('routine', id); if (view !== 'history') next.set('view', view); }
    return next;
  });
  const [showForm, setShowForm] = useState(false);
  const loading = routinesQ.isPending || inboxesQ.isPending;
  const [busy, setBusy] = useState(false);
  const [actionError, setError] = useState('');
  const loadError = routinesQ.error ?? inboxesQ.error;
  const error = actionError || (loadError ? (loadError instanceof Error ? loadError.message : 'Could not load routines.') : '');
  const [inboxDrawer, setInboxDrawer] = useState<string>();

  const load = async () => {
    await Promise.all([routinesQ.refetch({ throwOnError: true }), inboxesQ.refetch({ throwOnError: true })]);
  };

  const openCreate = () => {
    selectRoutine(); setShowForm(true); setError('');
  };
  const openSettings = (routine: Routine) => {
    selectRoutine(routine.id, 'settings'); setInboxDrawer(undefined); setShowForm(false); setError('');
  };
  const refreshAfterSave = () => load().catch((err: Error) => setError(err.message));
  const saved = () => {
    // Close before refreshing: a failed refresh must not invite a duplicate Create.
    setShowForm(false);
    selectRoutine();
    void refreshAfterSave();
  };
  const act = async (action: () => Promise<unknown>) => {
    setBusy(true); setError('');
    try { await action(); await load(); }
    catch (err) { setError(err instanceof Error ? err.message : 'Routine action failed.'); }
    finally { setBusy(false); }
  };
  const historyRoutine = routines.find((routine) => routine.id === historyId);

  return <main className={styles.page}>
    <Tabs value={tab} onValueChange={(value) => setSearchParams(value === 'inboxes' ? { tab: value } : {}, { replace: true })} className={styles.tabs}>
      <TabsList aria-label="Routine views"><TabsTrigger value="routines">Routines</TabsTrigger><TabsTrigger value="inboxes">Webhook inboxes</TabsTrigger></TabsList>
      {error && !showForm && <p role="alert" className={styles.error}>{error}</p>}
      {showForm && <RoutineEditorDrawer inboxes={inboxes} onClose={() => setShowForm(false)} onSaved={saved} onRefresh={refreshAfterSave} />}
      {historyRoutine && <RoutineHistoryDrawer key={historyRoutine.id} routine={historyRoutine} inboxes={inboxes} activeTab={detailTab} onTabChange={(view) => selectRoutine(historyRoutine.id, view)} refreshKey={refreshKey} onRefresh={refreshAfterSave} onSaved={saved} onClose={() => selectRoutine()} />}
      {historyId && !historyRoutine && !loading && !error && <p role="alert">This routine is unavailable.</p>}
      {inboxDrawer !== undefined && <WebhookInboxDrawer key={inboxDrawer} inbox={inboxes.find((inbox) => inbox.id === inboxDrawer) ?? null} routines={routines} onClose={() => setInboxDrawer(undefined)} onChange={() => void load().catch((err: Error) => setError(err.message))} onEditRoutine={openSettings} />}

      <TabsContent value="routines" className={styles.panel}>
        <header className={styles.header}><p>Save a prompt, run it now, or schedule it for later.</p><Button type="button" variant="accent" onClick={openCreate}><i className="bi bi-plus-lg" aria-hidden="true" />New routine</Button></header>
        {loading ? <div className="oc-list-loading" role="status"><div className="oc-spinner" />Loading routines...</div> : routines.length === 0 ? <EmptyState>No routines yet.</EmptyState> : (
          <section className={styles.list} aria-label="Saved routines"><DataTable framed><thead><tr><th>Name</th><th>Project</th><th>Session</th><th>Trigger</th><th>Last run</th><th>Status</th><th>Actions</th></tr></thead><tbody>{routines.map((routine) => {
            const latest = routine.latestRun;
            const status = routine.expiredAt && routine.expiredAt > (latest?.createdAt ?? 0) ? 'expired' : latest?.state ?? (routine.enabled ? 'ready' : 'disabled');
            const openHistory = () => selectRoutine(routine.id);
            return <tr key={routine.id} tabIndex={0} aria-label={`View ${routine.name} history`} onClick={openHistory} onKeyDown={(event) => { if (event.target === event.currentTarget && (event.key === 'Enter' || event.key === ' ')) { event.preventDefault(); openHistory(); } }}>
              <td><strong>{routine.name}</strong><small>{routine.prompt}</small></td><td><ProjectLabel path={routine.directory} /></td><td>{routine.sessionMode === 'new' ? 'New each run' : routine.sessionMode === 'reuse' ? 'Reuse' : 'Existing'}</td><td>{triggerLabel(routine, inboxes)}</td><td>{latest ? formatDateTimeShort(latest.startedAt || latest.createdAt) : '-'}</td><td><RoutineStateBadge state={status} /></td><td><ButtonGroup label={`Actions for ${routine.name}`} joined>
                <Button aria-label="Run" title="Run" size="small" disabled={busy} type="button" variant="accent" onClick={(event) => { event.stopPropagation(); void act(() => api.routines.run(routine.id)); }}><i className="bi bi-play-fill" aria-hidden="true" /></Button>
                <Button aria-label="Settings" title="Settings" size="small" disabled={busy} type="button" onClick={(event) => { event.stopPropagation(); openSettings(routine); }}><i className="bi bi-gear" aria-hidden="true" /></Button>
                <Button aria-label="Delete" title="Delete" size="small" disabled={busy} type="button" variant="danger" onClick={(event) => { event.stopPropagation(); if (window.confirm(`Delete "${routine.name}"?`)) void act(() => api.routines.remove(routine.id)); }}><i className="bi bi-trash" aria-hidden="true" /></Button>
              </ButtonGroup></td>
            </tr>;
          })}</tbody></DataTable></section>
        )}
      </TabsContent>
      <TabsContent value="inboxes" className={styles.panel}>
        <section aria-label="Webhook inboxes" className={styles.panel}>
          <header className={styles.header}><p>Inboxes capture encrypted webhook deliveries. Routines subscribe to an inbox and filter which deliveries run them.</p><Button type="button" variant="accent" onClick={() => setInboxDrawer('')}><i className="bi bi-plus-lg" aria-hidden="true" />New inbox</Button></header>
          {loading ? <div className="oc-list-loading" role="status"><div className="oc-spinner" />Loading inboxes...</div> : inboxes.length === 0 ? <EmptyState>No webhook inboxes yet.</EmptyState> : (
            <section className={styles.list} aria-label="Saved inboxes"><DataTable framed><thead><tr><th>Name</th><th>Linked routines</th><th>Deliveries</th></tr></thead><tbody>{inboxes.map((inbox) => (
              <tr key={inbox.id} tabIndex={0} aria-label={`Manage ${inbox.name || inbox.id} inbox`} onClick={() => setInboxDrawer(inbox.id)} onKeyDown={(event) => { if (event.target === event.currentTarget && (event.key === 'Enter' || event.key === ' ')) { event.preventDefault(); setInboxDrawer(inbox.id); } }}>
                <td><strong>{inbox.name || inbox.id}</strong><small>Key v{inbox.keyVersion}</small></td>
                <td>{inbox.subscriptions.map((sub) => routines.find((r) => r.id === sub.routineId)?.name).filter(Boolean).join(', ') || '-'}</td>
                <td>{Object.entries(inbox.counts).map(([state, count]) => `${state}: ${count}`).join(' · ') || '-'}</td>
              </tr>
            ))}</tbody></DataTable></section>
          )}
        </section>
      </TabsContent>
    </Tabs>
  </main>;
}

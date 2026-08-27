import { useFactoryStatus } from '../lib/queries';
import './MissionControl.css';

export function MissionControl() {
  const status = useFactoryStatus();

  if (status.isLoading && !status.data) {
    return <div className="oc-list-loading" role="status"><span className="oc-spinner" />Loading Factory status…</div>;
  }
  if (status.isError && !status.data) {
    const message = status.error instanceof Error ? status.error.message : 'Factory status is unavailable.';
    return (
      <div className="oc-error-banner" role="alert">
        {message}
        <button type="button" onClick={() => void status.refetch()}>Retry</button>
      </div>
    );
  }
  if (!status.data) return null;

  const factory = status.data;
  const healthy = factory.health === 'healthy';
  return (
    <main className="factory-page">
      {status.isError && (
        <div className="oc-error-banner" role="alert">
          Factory status may be stale: {status.error instanceof Error ? status.error.message : 'refresh failed'}
          <button type="button" onClick={() => void status.refetch()}>Retry</button>
        </div>
      )}
      <div className="factory-heading">
        <div>
          <div className="factory-kicker">Software Factory</div>
          <h2>Mission Control</h2>
        </div>
        <div className={`factory-health factory-health-${factory.health}`} role={healthy ? 'status' : 'alert'}>
          <strong>{healthy ? `Healthy · ${factory.idle ? 'idle' : 'active'}` : factory.health}</strong>
          <span>{factory.message}</span>
        </div>
      </div>

      <section className="factory-panel" aria-labelledby="factory-dispatch-heading">
        <h3 id="factory-dispatch-heading">Factory dispatcher</h3>
        <p>
          {factory.dispatchOwner
            ? 'This process owns dispatch.'
            : 'Another local process owns dispatch; this process is read-only.'}
        </p>
        {factory.beads.version && (
          <small>Beads {factory.beads.version} · JSON contract {factory.beads.contractVersion}</small>
        )}
      </section>

      {healthy && factory.workEpicCount === 0 && (
        <section className="factory-panel oc-empty" aria-label="Work Epics">
          <strong>No Work Epics yet</strong>
          <p>Factory work will appear here when a Work Epic is created.</p>
        </section>
      )}
    </main>
  );
}

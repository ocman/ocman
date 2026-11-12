import { useQuery } from '@tanstack/react-query';
import { plugins } from '../lib/plugins';
import { Button, ButtonGroup } from './Control';
import { CopyButton } from './CopyButton';
import styles from './PluginSettingsCard.module.css';

export function PluginLogs({ owner, pluginId }: { owner: string; pluginId: string }) {
  const query = useQuery({
    queryKey: ['plugin-stderr', owner, pluginId],
    queryFn: async () => (await plugins.stderr(owner, pluginId)).stderr.slice(-128 * 1024),
    retry: false,
  });
  const logs = query.data;
  return <section className={styles.logSection} aria-label="Plugin logs">
    <div className={styles.logHeader}>
      <div><h3>Recent logs</h3><p>Standard error · Last 128 KiB · {owner}</p></div>
      <ButtonGroup label="Plugin log actions">
        <Button type="button" size="small" disabled={query.isFetching} aria-busy={query.isFetching} onClick={() => void query.refetch()}>
          <i className="bi bi-arrow-clockwise" aria-hidden="true" /> Refresh logs
        </Button>
        <CopyButton text={logs ?? ''} label="Copy logs" size="small" disabled={!logs} />
      </ButtonGroup>
    </div>
    {query.isError && <p role="alert">Could not load recent stderr for this owner. Refresh logs to retry.</p>}
    <pre aria-label="Recent stderr" aria-busy={query.isFetching} className={styles.logs}>
      {logs ?? (query.isPending ? 'Loading recent logs…' : 'Logs are unavailable.')}{logs === '' && 'No recent stderr.'}
    </pre>
  </section>;
}

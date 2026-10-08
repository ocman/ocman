import { useCallback, useEffect, useState } from 'react';
import { ArtifactList } from '../components/ArtifactList';
import { Button, SearchField, SelectField } from '../components/Control';
import { EmptyState } from '../components/EmptyState';
import { LoadingState } from '../components/LoadingState';
import { InlineAlert } from '../components/InlineAlert';
import { api, type Project } from '../lib/api';
import { artifactsApi, formatBytes, type Artifact, type ArtifactStats } from '../lib/artifactsApi';
import { shortPath } from '../lib/format';
import { usePageTitle } from '../lib/headerContext';
import { onArtifactCreated } from '../lib/useGlobalEvents';
import styles from './Artifacts.module.css';

const SEARCH_DEBOUNCE_MS = 300;

export function Artifacts() {
  usePageTitle('Artifacts');
  const [artifacts, setArtifacts] = useState<Artifact[]>([]);
  const [cursor, setCursor] = useState('');
  const [stats, setStats] = useState<ArtifactStats>();
  const [projects, setProjects] = useState<Project[]>([]);
  const [directory, setDirectory] = useState('');
  const [search, setSearch] = useState('');
  const [q, setQ] = useState('');
  const [tick, setTick] = useState(0);
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');

  useEffect(() => {
    const t = window.setTimeout(() => setQ(search.trim()), SEARCH_DEBOUNCE_MS);
    return () => window.clearTimeout(t);
  }, [search]);

  useEffect(() => {
    api.projects().then(setProjects, () => setProjects([]));
    return onArtifactCreated(() => setTick((n) => n + 1));
  }, []);

  useEffect(() => {
    const ctrl = new AbortController();
    setLoading(true);
    artifactsApi.stats(ctrl.signal).then(setStats, () => undefined);
    artifactsApi.list({ directory, q }, ctrl.signal).then(
      (page) => { setArtifacts(page.artifacts ?? []); setCursor(page.nextCursor); setError(''); },
      (err) => { if (!ctrl.signal.aborted) setError(err instanceof Error ? err.message : 'Could not load artifacts.'); },
    ).finally(() => { if (!ctrl.signal.aborted) setLoading(false); });
    return () => ctrl.abort();
  }, [directory, q, tick]);

  const loadMore = useCallback(async () => {
    setBusy(true);
    try {
      const page = await artifactsApi.list({ directory, q, cursor });
      setArtifacts((prev) => [...prev, ...(page.artifacts ?? [])]);
      setCursor(page.nextCursor);
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Could not load artifacts.');
    } finally {
      setBusy(false);
    }
  }, [directory, q, cursor]);

  const dirs = [...new Set(projects.filter((p) => !p.archived).map((p) => p.directory))].sort();

  return (
    <main className={styles.page}>
      <header className={styles.header}>
        <p className={styles.stats} data-testid="artifact-stats">{stats ? `${stats.count} artifacts · ${formatBytes(stats.totalBytes)} stored` : 'Files and links saved by sessions.'}</p>
        <div className={styles.filters}>
          <SearchField className={styles.search} aria-label="Search artifacts" placeholder="Search artifacts" value={search} onChange={(e) => setSearch(e.target.value)} />
          <SelectField className={styles.project} aria-label="Project" value={directory} onChange={(e) => setDirectory(e.target.value)}>
            <option value="">All projects</option>
            {dirs.map((d) => <option key={d} value={d}>{shortPath(d)}</option>)}
          </SelectField>
        </div>
      </header>
      {error && <InlineAlert onRetry={() => setTick((n) => n + 1)} retrying={loading || busy}>{error}</InlineAlert>}
      {loading && artifacts.length === 0 ? <LoadingState>Loading artifacts...</LoadingState>
        : artifacts.length > 0 ? <ArtifactList artifacts={artifacts} />
        : !error && <EmptyState>No artifacts yet.</EmptyState>}
      {cursor && <Button type="button" disabled={busy || loading} aria-busy={busy} onClick={() => void loadMore()}>Load more</Button>}
    </main>
  );
}

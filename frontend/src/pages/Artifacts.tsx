import { useCallback, useEffect, useState } from 'react';
import { ArtifactList } from '../components/ArtifactList';
import { Button } from '../components/Control';
import { EmptyState } from '../components/EmptyState';
import { api, type Project } from '../lib/api';
import { artifactsApi, formatBytes, type Artifact, type ArtifactStats } from '../lib/artifactsApi';
import { usePageTitle } from '../lib/headerContext';
import { onArtifactCreated } from '../lib/useGlobalEvents';

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
    <main className="artifact-page">
      <header className="artifact-header">
        <p data-testid="artifact-stats">{stats ? `${stats.count} artifacts · ${formatBytes(stats.totalBytes)} stored` : 'Files and links saved by sessions.'}</p>
        <div className="artifact-filters">
          <select aria-label="Project" value={directory} onChange={(e) => setDirectory(e.target.value)}>
            <option value="">All projects</option>
            {dirs.map((d) => <option key={d} value={d}>{d}</option>)}
          </select>
          <input type="search" aria-label="Search artifacts" placeholder="Search artifacts" value={search} onChange={(e) => setSearch(e.target.value)} />
        </div>
      </header>
      {error && <p role="alert" className="artifact-missing">{error}</p>}
      {loading ? <div className="oc-list-loading" role="status"><div className="oc-spinner" />Loading artifacts...</div>
        : artifacts.length === 0 ? <EmptyState>No artifacts yet.</EmptyState>
        : <ArtifactList artifacts={artifacts} />}
      {cursor && <Button type="button" disabled={busy} onClick={() => void loadMore()}>Load more</Button>}
    </main>
  );
}

import { useEffect, useState } from 'react';
import { useSearchParams } from 'react-router-dom';
import { useInfiniteQuery, useQuery, useQueryClient } from '@tanstack/react-query';
import { ArtifactList } from '../components/ArtifactList';
import { Button, SearchField, SelectField } from '../components/Control';
import { PageState } from '../components/PageState';
import { SkeletonTable } from '../components/Skeleton';
import { InlineAlert } from '../components/InlineAlert';
import { api, type Project } from '../lib/api';
import { artifactsApi, formatBytes } from '../lib/artifactsApi';
import { shortPath } from '../lib/format';
import { usePageTitle } from '../lib/headerContext';
import { onArtifactCreated } from '../lib/useGlobalEvents';
import styles from './Artifacts.module.css';

const SEARCH_DEBOUNCE_MS = 300;

export function Artifacts() {
  usePageTitle('Artifacts');
  const [params, setParams] = useSearchParams();
  const directory = params.get('directory') ?? '';
  const search = params.get('q') ?? '';
  const [q, setQ] = useState(search.trim());
  const [projects, setProjects] = useState<Project[]>([]);
  const queryClient = useQueryClient();
  const stats = useQuery({ queryKey: ['artifact-stats'], queryFn: ({ signal }) => artifactsApi.stats(signal) });
  const list = useInfiniteQuery({
    queryKey: ['artifacts', directory, q],
    initialPageParam: '',
    queryFn: ({ pageParam, signal }) => artifactsApi.list({ directory, q, ...(pageParam ? { cursor: pageParam } : {}) }, signal),
    getNextPageParam: (page) => page.nextCursor || undefined,
  });
  const artifacts = list.data?.pages.flatMap((page) => page.artifacts ?? []) ?? [];
  const unavailable = list.isError && !list.data;

  useEffect(() => {
    const timer = window.setTimeout(() => setQ(search.trim()), SEARCH_DEBOUNCE_MS);
    return () => window.clearTimeout(timer);
  }, [search]);

  useEffect(() => {
    api.projects().then(setProjects, () => setProjects([]));
    return onArtifactCreated(() => {
      void queryClient.invalidateQueries({ queryKey: ['artifacts'] });
      void queryClient.invalidateQueries({ queryKey: ['artifact-stats'] });
    });
  }, [queryClient]);

  const filter = (key: string, value: string, replace = false) => setParams((previous) => {
    const next = new URLSearchParams(previous);
    if (value) next.set(key, value); else next.delete(key);
    return next;
  }, { replace });
  const clearFilters = () => setParams((previous) => {
    const next = new URLSearchParams(previous);
    next.delete('q'); next.delete('directory');
    return next;
  });
  const retry = () => { void (list.isFetchNextPageError ? list.fetchNextPage() : list.refetch()); };
  const dirs = [...new Set(projects.filter((p) => !p.archived).map((p) => p.directory))].sort();

  return <main className={styles.page}>
    <header className={styles.header}>
      <p className={styles.stats} data-testid="artifact-stats">{stats.data ? `${stats.data.count} artifacts · ${formatBytes(stats.data.totalBytes)} stored` : 'Files and links saved by sessions.'}</p>
      <div className={styles.filters}>
        <SearchField className={styles.search} aria-label="Search artifacts" placeholder="Search artifacts" value={search} onChange={(event) => filter('q', event.target.value, true)} />
        <SelectField className={styles.project} aria-label="Project" value={directory} onChange={(event) => filter('directory', event.target.value)}>
          <option value="">All projects</option>
          {directory && !dirs.includes(directory) && <option value={directory}>{shortPath(directory)}</option>}
          {dirs.map((dir) => <option key={dir} value={dir}>{shortPath(dir)}</option>)}
        </SelectField>
      </div>
    </header>
    {unavailable ? <PageState error icon="bi-exclamation-circle" title="Artifacts are unavailable" description={list.error.message}><Button type="button" onClick={retry} disabled={list.isFetching} aria-busy={list.isFetching}>Retry</Button></PageState> : <>
      {list.error && <InlineAlert onRetry={retry} retrying={list.isFetching}>{list.error.message}</InlineAlert>}
      {list.isPending ? <SkeletonTable columns={['Title', 'Project', 'Session', 'Items', 'Size', 'Created']} rows={3} label="Loading artifacts" />
        : artifacts.length > 0 ? <ArtifactList artifacts={artifacts} listSearch={params.toString()} />
        : directory || q ? <PageState icon="bi-search" title="No matching artifacts" description="Try a different search or clear your filters to see all artifacts."><Button type="button" onClick={clearFilters}>Clear filters</Button></PageState>
        : <PageState icon="bi-archive" title="No artifacts saved yet" description="Files and links saved by your sessions will appear here. Ask your agent to save a report or screenshot as an artifact." />}
      {list.hasNextPage && <Button type="button" disabled={list.isFetching} aria-busy={list.isFetchingNextPage} onClick={() => void list.fetchNextPage()}>Load more</Button>}
    </>}
  </main>;
}

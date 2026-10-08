import { useEffect, useCallback, useRef, useMemo } from 'react';
import { useParams, useSearchParams } from 'react-router-dom';
import { SessionTable } from '../components/SessionTable';
import { TimeRangeControl } from '../components/TimeRangeControl';
import { cleanTitle, fuzzyMatch } from '../lib/format';
import { openVSCode } from '../lib/shortcuts';
import { useShortcut } from '../lib/shortcutRegistry';
import { useProjects, useSessions } from '../lib/queries';
import { projectIdentityIndex } from '../lib/projectIdentity';
import { Button, ButtonGroup, SearchField } from '../components/Control';
import styles from './ProjectDetail.module.css';
import { ProjectShell } from './ProjectShell';

const DEFAULT_TIME_RANGE = 168; // 7d

export function ProjectDetail() {
  const { dir } = useParams();
  const directory = dir ? decodeURIComponent(dir) : undefined;

  // Filter state (mirrors the dashboard Sessions tab) — persisted in the
  // URL so refresh / back-forward keep the user's view. Default to 7d
  // (`t` absent ⇒ 168) and to "include archived" (`a` absent ⇒ false),
  // since users navigating into a specific project usually want to see
  // everything that's happened there, archived sessions included.
  const [searchParams, setSearchParams] = useSearchParams();
  const timeRange = parseInt(searchParams.get('t') || String(DEFAULT_TIME_RANGE), 10);
  const excludeArchived = searchParams.get('a') === '1';
  // Carry an explicit owner on to the Worktrees view (absent = this machine).
  const ownerId = searchParams.get('remoteId');

  const setTimeRange = useCallback((v: number) => {
    setSearchParams((p) => { p.set('t', String(v)); return p; }, { replace: true });
  }, [setSearchParams]);

  const setExcludeArchived = useCallback((v: boolean) => {
    setSearchParams((p) => {
      if (v) p.set('a', '1');
      else p.delete('a');
      return p;
    }, { replace: true });
  }, [setSearchParams]);

  // TanStack Query handles dedup, cancellation, stale-while-revalidate,
  // and visibility pausing automatically (Wave 3 / P4+P5 fix).
  // sinceHours produces a stable query key; the actual timestamp is
  // computed inside the queryFn at fetch time.
  const sinceHours = timeRange > 0 ? timeRange : undefined;
  const projectsQ = useProjects();
  const identity = useMemo(() => projectIdentityIndex(projectsQ.data ?? []), [projectsQ.data]);
  const sessionsQ = useSessions(
    { sinceHours, limit: 0 },
    { refetchInterval: 5000, enabled: !!directory },
  );
  const projectKey = identity(directory ?? '', ownerId ?? undefined).key;
  const sessions = (sessionsQ.data ?? []).filter(s => identity(s.directory || '', s.remoteId).key === projectKey);
  const sessionsLoaded = !sessionsQ.isLoading && !projectsQ.isLoading;
  const search = searchParams.get('q') || '';
  const q = search.trim();
  const filteredSessions = q
    ? sessions.filter((s) => fuzzyMatch(q, `${cleanTitle(s.title)} ${s.directory}`))
    : sessions;

  const handleOpenVSCode = useCallback(() => {
    if (!directory) return;
    openVSCode(directory);
  }, [directory]);

  const handleOpenVSCodeRef = useRef(handleOpenVSCode);
  useEffect(() => { handleOpenVSCodeRef.current = handleOpenVSCode; }, [handleOpenVSCode]);
  const directoryRef = useRef(directory);
  useEffect(() => { directoryRef.current = directory; }, [directory]);

  const openVscodeShortcut = useMemo(() => ({
    id: 'project.open-vscode',
    scope: 'project' as const,
    keys: { code: 'KeyV', alt: true },
    description: 'Open current project in VS Code',
    enabled: () => !!directoryRef.current,
    handler: () => handleOpenVSCodeRef.current(),
  }), []);

  useShortcut(openVscodeShortcut);

  return (
    <ProjectShell view="sessions">
      <div className={styles.searchBar}>
        <SearchField className={styles.search}
          placeholder="Search sessions…"
          aria-label="Search sessions"
          value={search}
          onChange={(e) => setSearchParams((params) => {
            const next = new URLSearchParams(params);
            if (e.target.value) next.set('q', e.target.value); else next.delete('q');
            return next;
          }, { replace: true })}
        />
      </div>
      <ButtonGroup label="Project session filters" className={styles.range}>
        <TimeRangeControl value={timeRange} onChange={setTimeRange} />
        <Button type="button" size="small" variant={excludeArchived ? 'accent' : 'default'} aria-pressed={excludeArchived}
          onClick={() => setExcludeArchived(!excludeArchived)}
        >Exclude archived</Button>
      </ButtonGroup>
      <SessionTable sessions={filteredSessions} showProject={false} loading={!sessionsLoaded} includeArchived={!excludeArchived} />
    </ProjectShell>
  );
}

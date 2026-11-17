import { useEffect, useRef } from 'react';
import { useParams, useSearchParams } from 'react-router-dom';
import { SessionTable } from '../components/SessionTable';
import { SessionRangeFilters } from '../components/SessionRangeFilters';
import { useSessionListUrlFilters } from '../lib/useSessionListUrlFilters';
import { cleanTitle, fuzzyMatch } from '../lib/format';
import { openVSCode } from '../lib/shortcuts';
import { useShortcut } from '../lib/shortcutRegistry';
import { useProjects, useSessions } from '../lib/queries';
import { useProjectIdentityIndex } from '../lib/useProjectIdentityIndex';
import { useSessionAccounting } from '../lib/useSessionAccounting';
import { SearchField } from '../components/Control';
import styles from './ProjectDetail.module.css';
import { ProjectShell } from './ProjectShell';

export function ProjectDetail() {
  const { dir } = useParams();
  const directory = dir ? decodeURIComponent(dir) : undefined;

  // Filter state is shared with the Sessions page and persisted in the URL so
  // refresh / back-forward keep the user's view.
  const [searchParams, setSearchParams] = useSearchParams();
  const { timeRange, sinceHours, setTimeRange, showArchived, setShowArchived } = useSessionListUrlFilters();
  // Carry an explicit owner on to the Worktrees view (absent = this machine).
  const ownerId = searchParams.get('remoteId');

  const projectsQ = useProjects();
  const sessionsQ = useSessions(
    { sinceHours, limit: 0 },
    { poll: true, enabled: !!directory },
  );
  const identity = useProjectIdentityIndex(projectsQ.data ?? [], [...(sessionsQ.data ?? []), { directory: directory ?? '', remoteId: ownerId ?? undefined }]);
  const projectKey = identity(directory ?? '', ownerId ?? undefined).key;
  const { sessions, pendingKeys: activityPendingKeys } = useSessionAccounting(
    (sessionsQ.data ?? []).filter(s => identity(s.directory || '', s.remoteId).key === projectKey),
    { sinceHours }, !!directory);
  const sessionsLoaded = !sessionsQ.isLoading && !projectsQ.isLoading;
  const search = searchParams.get('q') || '';
  const q = search.trim();
  const filteredSessions = q
    ? sessions.filter((s) => fuzzyMatch(q, `${cleanTitle(s.title)} ${s.directory}`))
    : sessions;

  const directoryRef = useRef(directory);
  useEffect(() => { directoryRef.current = directory; }, [directory]);

  const openVscodeShortcut = {
    id: 'project.open-vscode',
    scope: 'project' as const,
    description: 'Open current project in VS Code',
    enabled: () => !!directoryRef.current,
    handler: () => {
      if (directoryRef.current) openVSCode(directoryRef.current);
    },
  };

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
      <SessionRangeFilters label="Project session filters" className={styles.range} timeRange={timeRange} onTimeRangeChange={setTimeRange}
        showArchived={showArchived} onShowArchivedChange={setShowArchived} />
      <SessionTable sessions={filteredSessions} showProject={false} loading={!sessionsLoaded} includeArchived={showArchived} activityPendingKeys={activityPendingKeys} />
    </ProjectShell>
  );
}

import { useState } from 'react';
import { cleanTitle, fuzzyMatch } from '../../lib/format';
import { usePageTitle } from '../../lib/headerContext';
import { SessionTable } from '../../components/SessionTable';
import { TimeRangeControl } from '../../components/TimeRangeControl';
import { matchesScope } from '../../lib/projectTree';
import { useUiStore } from '../../lib/uiStore';
import { useDashboard as useDashboardCtx } from './context';
import { DashboardToolbar } from './DashboardToolbar';

// ---------------------------------------------------------------------------
// Sessions tab
// ---------------------------------------------------------------------------

export function SessionsTab() {
  usePageTitle('Sessions');
  const { sessions, projects, sessionsLoading, sessionsError, loadSessions, timeRange, setTimeRange, showArchived, setShowArchived, dirScope, setDirScope } = useDashboardCtx();
  const openProjectSessionPalette = useUiStore((s) => s.openProjectSessionPalette);
  const [search, setSearch] = useState('');

  const q = search.trim();
  const filteredSessions = sessions
    .filter((s) => matchesScope(s.directory, dirScope))
    .filter((s) => !q || fuzzyMatch(q, `${cleanTitle(s.title)} ${s.directory}`));

  return (
    <>
      {sessionsError && (
        <div className="oc-error-banner">
          {sessionsError}
          <button onClick={() => loadSessions()}>Retry</button>
        </div>
      )}
      <DashboardToolbar
        projects={projects}
        dirScope={dirScope}
        setDirScope={setDirScope}
        search={search}
        setSearch={setSearch}
        searchLabel="Search sessions"
        actionIcon="bi-plus-lg"
        actionLabel="New session"
        actionTitle="Create a new OpenCode session in a known project"
        onAction={openProjectSessionPalette}
      />
      <div className="oc-time-range">
        <TimeRangeControl value={timeRange} onChange={setTimeRange} />
        <button
          className={`oc-time-range-btn${showArchived ? ' active' : ''}`}
          onClick={() => setShowArchived(!showArchived)}
        >Include archived</button>
      </div>
      <SessionTable
        sessions={filteredSessions}
        showProject
        loading={sessionsLoading && sessions.length === 0}
        includeArchived={showArchived}
      />
    </>
  );
}

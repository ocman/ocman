import { useState } from 'react';
import { cleanTitle, fuzzyMatch } from '../../lib/format';
import { usePageTitle } from '../../lib/headerContext';
import { SessionTable } from '../../components/SessionTable';
import { TimeRangeControl } from '../../components/TimeRangeControl';
import { InlineAlert } from '../../components/InlineAlert';
import { matchesScope } from '../../lib/projectTree';
import { useUiStore } from '../../lib/uiStore';
import { useDashboard as useDashboardCtx } from './context';
import { DashboardToolbar } from './DashboardToolbar';
import { Button, ButtonGroup } from '../../components/Control';
import { SegmentedControl } from '../../components/SegmentedControl';
import { useIsPhone } from '../../lib/useIsPhone';
import styles from './SessionsTab.module.css';

// ---------------------------------------------------------------------------
// Sessions tab
// ---------------------------------------------------------------------------

export function SessionsTab() {
  usePageTitle('Sessions');
  const { sessions, projects, sessionsLoading, sessionsError, loadSessions, timeRange, setTimeRange, showArchived, setShowArchived, dirScope, setDirScope } = useDashboardCtx();
  const openProjectSessionPalette = useUiStore((s) => s.openProjectSessionPalette);
  const [search, setSearch] = useState('');
  const phone = useIsPhone();
  const [phoneFilter, setPhoneFilter] = useState('all');

  const q = search.trim();
  const filteredSessions = sessions
    .filter((s) => matchesScope(s.directory, dirScope))
    .filter((s) => !phone || phoneFilter === 'all' || (phoneFilter === 'running' ? s.status === 'busy' : s.pendingPermission || s.pendingQuestion || s.status === 'waiting'))
    .filter((s) => !q || fuzzyMatch(q, `${cleanTitle(s.title)} ${s.directory}`));

  return (
    <>
      {sessionsError && (
        <InlineAlert onRetry={() => loadSessions()} retrying={sessionsLoading}>
          {sessionsError}
        </InlineAlert>
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
      <ButtonGroup label="Session filters" className={styles.filters}>
        <TimeRangeControl value={timeRange} onChange={setTimeRange} />
        <Button type="button" size="small" variant={showArchived ? 'accent' : 'default'} aria-pressed={showArchived}
          onClick={() => setShowArchived(!showArchived)}
        >Include archived</Button>
      </ButtonGroup>
      <div className="phone-session-filters"><SegmentedControl label="Session status" value={phoneFilter} onChange={setPhoneFilter}
        options={[{ value: 'all', label: 'All' }, { value: 'running', label: 'Running' }, { value: 'waiting', label: 'Waiting' }]} /></div>
      <SessionTable
        sessions={filteredSessions}
        showProject
        loading={sessionsLoading && sessions.length === 0}
        includeArchived={showArchived}
      />
    </>
  );
}

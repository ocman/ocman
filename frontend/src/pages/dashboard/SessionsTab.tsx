import { useState } from 'react';
import { useSearchParams } from 'react-router-dom';
import { cleanTitle, fuzzyMatch } from '../../lib/format';
import { usePageTitle } from '../../lib/headerContext';
import { SessionTable } from '../../components/SessionTable';
import { TimeRangeControl } from '../../components/TimeRangeControl';
import { InlineAlert } from '../../components/InlineAlert';
import { PageState } from '../../components/PageState';
import { Button } from '../../components/Control';
import { matchesScope } from '../../lib/projectTree';
import { useUiStore } from '../../lib/uiStore';
import { useDashboard as useDashboardCtx } from './context';
import { DashboardToolbar } from './DashboardToolbar';
import { SessionRangeFilters } from '../../components/SessionRangeFilters';
import { useIsPhone } from '../../lib/useIsPhone';
import styles from './SessionsTab.module.css';
import { PhoneSessionsToolbar } from './PhoneSessionsToolbar';
import { SessionFilterControls } from '../../components/SessionFilterControls';
import { useSessionListFilters } from '../../lib/useSessionListFilters';
import { useSessionFilterPreference } from '../../lib/useSessionFilterPreference';
import { SESSION_FILTER_DEFAULTS, SESSION_FILTER_FIELDS, hiddenSessionOrigins, matchesSessionListFilters } from '../../lib/sessionListFilters';
import { hasPendingPrompt } from '../../lib/sessionVisibility';
import { useProjectIdentityIndex } from '../../lib/useProjectIdentityIndex';
import { useSessionAccounting } from '../../lib/useSessionAccounting';

// ---------------------------------------------------------------------------
// Sessions tab
// ---------------------------------------------------------------------------

export function SessionsTab() {
  usePageTitle('Sessions');
  const { sessions: compactSessions, projects, sessionsLoading, sessionsError, sessionsFetching, sessionsRefreshError,
    hasSessionsOutsideRange, loadSessions, timeRange, setTimeRange, showArchived, setShowArchived, dirScope, setDirScope } = useDashboardCtx();
  const [, setSearchParams] = useSearchParams();
  const openProjectSessionPalette = useUiStore((s) => s.openProjectSessionPalette);
  const [search, setSearch] = useState('');
  const { sessions, pendingKeys: activityPendingKeys, ...activityQ } = useSessionAccounting(compactSessions, { sinceHours: timeRange > 0 ? timeRange : undefined });
  const phone = useIsPhone();
  const { alwaysShowPrompts, setAlwaysShowPrompts, showChildren, setShowChildren, showFactory, setShowFactory,
    showRoutines, setShowRoutines, showPlugins, setShowPlugins, showSnoozed, setShowSnoozed } = useSessionListFilters();
  const [archivedPreference, setArchivedPreference] = useSessionFilterPreference('archived', SESSION_FILTER_DEFAULTS.showArchived);
  const filters = { alwaysShowPrompts, showArchived: showArchived || archivedPreference, showSnoozed, showChildren, showFactory, showRoutines, showPlugins };
  const phoneFiltersActive = SESSION_FILTER_FIELDS.some(([key]) => filters[key] !== SESSION_FILTER_DEFAULTS[key]);
  const preferenceSetters = {
    alwaysShowPrompts: setAlwaysShowPrompts, showArchived: setArchivedPreference,
    showSnoozed: setShowSnoozed, showChildren: setShowChildren, showFactory: setShowFactory,
    showRoutines: setShowRoutines, showPlugins: setShowPlugins,
  };
  const hidden = hiddenSessionOrigins(sessions, filters);

  const filterControls = <>
    <SessionRangeFilters label="Session filters" className={styles.filters} timeRange={timeRange} onTimeRangeChange={setTimeRange}
      showArchived={showArchived} onShowArchivedChange={setShowArchived} />
  </>;

  const q = search.trim();
  const identity = useProjectIdentityIndex(projects, sessions, !!dirScope);
  const filteredSessions = sessions
    .filter((s) => matchesScope(identity(s.directory, s.remoteId).directory, dirScope) || phone && alwaysShowPrompts && hasPendingPrompt(s))
    .filter((s) => {
      const searchMatches = !q || fuzzyMatch(q, `${cleanTitle(s.title)} ${s.directory}`);
      return phone ? matchesSessionListFilters(s, filters, hidden, searchMatches) : searchMatches;
    });

  const clearFilters = () => {
    setSearch('');
    if (phone) {
      for (const [key] of SESSION_FILTER_FIELDS) {
        if (filters[key] !== SESSION_FILTER_DEFAULTS[key]) preferenceSetters[key](SESSION_FILTER_DEFAULTS[key]);
      }
    }
    setSearchParams(params => {
      params.delete('dir');
      params.delete('a');
      params.set('t', '0');
      return params;
    }, { replace: true });
  };

  const emptyState = q || dirScope || hasSessionsOutsideRange || (phone ? phoneFiltersActive : showArchived)
    ? <PageState icon="bi-search" title="No matching sessions" description="Try changing your search or clearing your filters.">
        <Button type="button" onClick={clearFilters}>Clear filters</Button>
      </PageState>
    : sessions.length === 0
      ? <PageState icon="bi-chat-dots" title="No sessions yet" description="Choose a project to start your first session.">
          <Button type="button" variant="accent" onClick={() => openProjectSessionPalette()}>New session</Button>
        </PageState>
      : <PageState icon="bi-chat-dots" title="No active sessions" description="Use the filters to include sessions hidden from this list." />;

  return (
    <>
      {sessionsError && sessionsRefreshError && (
        <InlineAlert tone="warning" onRetry={loadSessions} retrying={sessionsFetching}>
          {sessionsError}
        </InlineAlert>
      )}
      {activityQ.error && (
        <InlineAlert onRetry={activityQ.retry} retrying={activityQ.retrying}>
          Could not load session activity: {activityQ.error}
        </InlineAlert>
      )}
      {phone ? <PhoneSessionsToolbar projects={projects} dirScope={dirScope} setDirScope={setDirScope}
        search={search} setSearch={setSearch} onNewSession={() => openProjectSessionPalette()}
        filtersActive={phoneFiltersActive}>
        <TimeRangeControl value={timeRange} onChange={setTimeRange} />
        <SessionFilterControls value={filters} onChange={(key, checked) => {
          preferenceSetters[key](checked);
          if (key === 'showArchived') setShowArchived(checked);
        }} />
      </PhoneSessionsToolbar> : <><DashboardToolbar
        projects={projects}
        dirScope={dirScope}
        setDirScope={setDirScope}
        search={search}
        setSearch={setSearch}
        searchLabel="Search sessions"
        actionIcon="bi-plus-lg"
        actionLabel="New session"
        actionTitle="Create a new OpenCode session in a known project"
        onAction={() => openProjectSessionPalette()}
      />
        {filterControls}</>}
      {sessionsError && !sessionsRefreshError ? <PageState error title="Could not load sessions" description={sessionsError}
        onRetry={loadSessions} retrying={sessionsFetching} /> : <SessionTable
        sessions={filteredSessions}
        showProject
        loading={sessionsLoading && sessions.length === 0}
        includeArchived={phone ? filters.showArchived : showArchived}
        prefiltered={phone}
        activityPendingKeys={activityPendingKeys}
        emptyState={emptyState}
      />}
    </>
  );
}

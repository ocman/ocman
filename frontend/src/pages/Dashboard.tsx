import { Navigate, Outlet, useSearchParams, useLocation } from 'react-router-dom';
import type { Project, Session } from '../lib/api';
import { ErrorBoundary } from '../components/ErrorBoundary';
import { useSessionListUrlFilters } from '../lib/useSessionListUrlFilters';
import { useSessions as useTQSessions, useProjects as useTQProjects } from '../lib/queries';
import { DashboardContext, type DashboardCtx } from './dashboard/context';

export function LegacyAnalyticsRedirect({ section }: { section: 'overview' | 'performance' }) {
  const { search } = useLocation();
  return <Navigate to={`/analytics/${section}${search}`} replace />;
}

// Stable empty arrays so `data ?? []` doesn't create a new reference on
// every render while the query is still loading.
const EMPTY_SESSIONS: Session[] = [];
const EMPTY_PROJECTS: Project[] = [];

// ---------------------------------------------------------------------------
// Layout — renders the tab bar + <Outlet> for the active tab route.
// ---------------------------------------------------------------------------

export function DashboardLayout() {
  const location = useLocation();
  const [searchParams, setSearchParams] = useSearchParams();
  const isOnDashboard = location.pathname === '/sessions' || location.pathname === '/projects' || location.pathname.startsWith('/analytics') || location.pathname === '/routines' || location.pathname === '/settings';

  const { timeRange, sinceHours, setTimeRange, showArchived, setShowArchived } = useSessionListUrlFilters();
  const dirScope = searchParams.get('dir') ?? '';

  // setDirScope writes the chosen scope to the URL so it survives refresh
  // and is preserved when the user switches between Analytics and Projects
  // tabs. Empty value clears the param entirely so the URL stays clean
  // ('?t=24' rather than '?t=24&dir=').
  const setDirScope = (v: string) => {
    setSearchParams((p) => {
      if (v) p.set('dir', v);
      else p.delete('dir');
      return p;
    }, { replace: true });
  };

  const sessionsQ = useTQSessions(
    { sinceHours },
    { poll: isOnDashboard },
  );
  const projectsQ = useTQProjects();

  // Module-level constants avoid creating a new [] on every render when
  // the query hasn't resolved yet (undefined ?? [] would be a fresh ref).
  const sessions = sessionsQ.data ?? EMPTY_SESSIONS;
  const projects = projectsQ.data ?? EMPTY_PROJECTS;

  const sessionsError = sessionsQ.error instanceof Error ? sessionsQ.error.message : null;
  const { isLoading: sessionsLoading, refetch: refetchSessions } = sessionsQ;
  const { isLoading: projectsLoading, isFetching: projectsFetching, refetch: refetchProjectsQ } = projectsQ;
  // Surfaced so tabs can tell a failed query from a successfully empty
  // one; discarding it made every failure look like first-run.
  const projectsError = projectsQ.isError
    ? (projectsQ.error instanceof Error ? projectsQ.error.message : 'Could not load projects.')
    : null;

  const loadSessions = () => { void refetchSessions(); };
  const refetchProjects = () => { void refetchProjectsQ(); };

  // eslint-disable-next-line react/jsx-no-constructed-context-values -- React Compiler memoizes ctx.
  const ctx: DashboardCtx = {
    sessions,
    projects,
    sessionsLoading,
    sessionsError,
    projectsLoading,
    projectsFetching,
    projectsError,
    loadSessions,
    refetchProjects,
    timeRange,
    setTimeRange,
    showArchived,
    setShowArchived,
    dirScope,
    setDirScope,
  };

  return (
    <DashboardContext.Provider value={ctx}>
      {/* Per-tab boundary so a crash inside Stats / Usage / etc.
          (chart.js render error, malformed metrics payload) stays local.
          resetKey on pathname auto-clears when the user switches tabs. */}
      <ErrorBoundary name={`dashboard:${location.pathname}`} resetKey={location.pathname}>
        <Outlet />
      </ErrorBoundary>
    </DashboardContext.Provider>
  );
}

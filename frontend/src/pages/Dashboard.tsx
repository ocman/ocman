import { useCallback, useMemo } from 'react';
import './Dashboard.css';
import { NavLink, Outlet, useSearchParams, useLocation } from 'react-router-dom';
import { Chart as ChartJS, CategoryScale, LinearScale, BarElement, ArcElement, Tooltip, Legend, PointElement, LineElement } from 'chart.js';
import type { Project, Session } from '../lib/api';
import { ErrorBoundary } from '../components/ErrorBoundary';
import { useUiStore } from '../lib/uiStore';
import { useAgentLoops, useWorkflows } from '../lib/useCapabilities';
import { useSessions as useTQSessions, useProjects as useTQProjects } from '../lib/queries';
import { DashboardContext, type DashboardCtx } from './dashboard/context';

// Re-export tab components so App.tsx can import from a single place.
export { StatsTab } from './dashboard/StatsTab';
export { UsageTab } from './dashboard/UsageTab';
export { SessionsTab } from './dashboard/SessionsTab';
export { ProjectsTab } from './dashboard/ProjectsTab';
export { SettingsTab } from './dashboard/SettingsTab';

ChartJS.register(CategoryScale, LinearScale, BarElement, ArcElement, PointElement, LineElement, Tooltip, Legend);

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
  const agentLoopsAllowed = useAgentLoops();
  const workflowsAllowed = useWorkflows();

  const isOnDashboard = location.pathname === '/sessions' || location.pathname === '/projects' || location.pathname === '/stats' || location.pathname === '/usage' || location.pathname === '/loops' || location.pathname === '/workflows' || location.pathname === '/settings';

  const dashboardTimeRangeDefault = useUiStore((s) => s.dashboardTimeRangeDefault);
  const tParam = searchParams.get('t');
  const timeRange = tParam !== null ? parseInt(tParam, 10) : dashboardTimeRangeDefault;
  const showArchived = searchParams.get('a') === '1';
  const dirScope = searchParams.get('dir') ?? '';

  const setTimeRange = useCallback((v: number) => {
    setSearchParams((p) => { p.set('t', String(v)); return p; }, { replace: true });
  }, [setSearchParams]);

  const setShowArchived = useCallback((v: boolean) => {
    setSearchParams((p) => {
      if (v) p.set('a', '1');
      else p.delete('a');
      return p;
    }, { replace: true });
  }, [setSearchParams]);

  // setDirScope writes the chosen scope to the URL so it survives refresh
  // and is preserved when the user switches between the Stats/Usage/Projects
  // tabs. Empty value clears the param entirely so the URL stays clean
  // ('?t=24' rather than '?t=24&dir=').
  const setDirScope = useCallback((v: string) => {
    setSearchParams((p) => {
      if (v) p.set('dir', v);
      else p.delete('dir');
      return p;
    }, { replace: true });
  }, [setSearchParams]);

  // TanStack Query handles dedup, cancellation, stale-while-revalidate,
  // and visibility pausing automatically (Wave 3 / P4+P5 fix).
  // sinceHours produces a stable query key; the actual timestamp is
  // computed inside the queryFn at fetch time.
  const sinceHours = timeRange > 0 ? timeRange : 30 * 24;
  const sessionsQ = useTQSessions(
    { sinceHours },
    { refetchInterval: isOnDashboard ? 5000 : undefined },
  );
  const projectsQ = useTQProjects();

  // Module-level constants avoid creating a new [] on every render when
  // the query hasn't resolved yet (undefined ?? [] would be a fresh ref).
  const sessions = sessionsQ.data ?? EMPTY_SESSIONS;
  const projects = projectsQ.data ?? EMPTY_PROJECTS;

  const sessionsError = sessionsQ.error instanceof Error ? sessionsQ.error.message : null;
  const { isLoading: sessionsLoading, refetch: refetchSessions } = sessionsQ;
  const { isLoading: projectsLoading } = projectsQ;

  const loadSessions = useCallback(() => { void refetchSessions(); }, [refetchSessions]);

  const ctx: DashboardCtx = useMemo(() => ({
    sessions,
    projects,
    sessionsLoading,
    sessionsError,
    projectsLoading,
    loadSessions,
    timeRange,
    setTimeRange,
    showArchived,
    setShowArchived,
    dirScope,
    setDirScope,
  }), [
    sessions,
    projects,
    sessionsLoading,
    sessionsError,
    projectsLoading,
    loadSessions,
    timeRange,
    setTimeRange,
    showArchived,
    setShowArchived,
    dirScope,
    setDirScope,
  ]);

  return (
    <DashboardContext.Provider value={ctx}>
      <div style={{ flex: 1, overflowY: 'auto', minHeight: 0 }}>
        <div className="nav-tabs">
          <NavLink to="/sessions" className={({ isActive }) => `nav-tab${isActive ? ' active' : ''}`}>Sessions</NavLink>
          <NavLink to="/projects" className={({ isActive }) => `nav-tab${isActive ? ' active' : ''}`}>Projects</NavLink>
          {agentLoopsAllowed && (
            <NavLink to="/loops" className={({ isActive }) => `nav-tab${isActive ? ' active' : ''}`}>Loops</NavLink>
          )}
          {workflowsAllowed && <NavLink to="/workflows" className={({ isActive }) => `nav-tab${isActive ? ' active' : ''}`}>Workflows</NavLink>}
          <NavLink to="/stats" className={({ isActive }) => `nav-tab${isActive ? ' active' : ''}`}>Stats</NavLink>
          <NavLink to="/usage" className={({ isActive }) => `nav-tab${isActive ? ' active' : ''}`}>Usage</NavLink>
          <NavLink to="/settings" className={({ isActive }) => `nav-tab${isActive ? ' active' : ''}`}>Settings</NavLink>
        </div>
        {/* Per-tab boundary so a crash inside Stats / Usage / etc.
            (chart.js render error, malformed metrics payload) doesn't
            blank the tab bar above. resetKey on pathname auto-clears
            when the user switches to a different tab. */}
        <ErrorBoundary name={`dashboard:${location.pathname}`} resetKey={location.pathname}>
          <Outlet />
        </ErrorBoundary>
      </div>
    </DashboardContext.Provider>
  );
}

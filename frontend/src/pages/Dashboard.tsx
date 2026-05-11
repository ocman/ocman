import { useState, useCallback, useMemo } from 'react';
import './Dashboard.css';
import { useNavigate, NavLink, Outlet, useSearchParams, useLocation } from 'react-router-dom';
import { Chart as ChartJS, CategoryScale, LinearScale, BarElement, ArcElement, Tooltip, Legend, PointElement, LineElement } from 'chart.js';
import type { Project, Session } from '../lib/api';
import {
  formatNumber,
  relativeTime,
  shortPath,
} from '../lib/format';
import { usePageTitle } from '../lib/headerContext';
import { SessionTable } from '../components/SessionTable';
import { ErrorBoundary } from '../components/ErrorBoundary';
import { ProjectScopePicker } from '../components/ProjectScopePicker';
import { matchesScope } from '../lib/projectTree';

import { useUiStore } from '../lib/uiStore';
import { useAuthStore } from '../lib/authStore';
import { usePwaInstall } from '../lib/usePwaInstall';
import {
  notificationsSupported,
  requestNotificationPermission,
} from '../lib/useNotificationNotify';
import { useSessions as useTQSessions, useProjects as useTQProjects } from '../lib/queries';
import { DashboardContext, useDashboard as useDashboardCtx, type DashboardCtx } from './dashboard/context';

// Re-export tab components so App.tsx can import from a single place.
export { StatsTab } from './dashboard/StatsTab';
export { UsageTab } from './dashboard/UsageTab';

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

  const isOnDashboard = location.pathname === '/' || location.pathname === '/projects' || location.pathname === '/stats' || location.pathname === '/usage' || location.pathname === '/settings';

  const timeRange = parseInt(searchParams.get('t') || '12', 10);
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
          <NavLink to="/" end className={({ isActive }) => `nav-tab${isActive ? ' active' : ''}`}>Sessions</NavLink>
          <NavLink to="/projects" className={({ isActive }) => `nav-tab${isActive ? ' active' : ''}`}>Projects</NavLink>
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

// ---------------------------------------------------------------------------
// Sessions tab
// ---------------------------------------------------------------------------

export function SessionsTab() {
  usePageTitle('Sessions');
  const { sessions, sessionsLoading, sessionsError, loadSessions, timeRange, setTimeRange, showArchived, setShowArchived } = useDashboardCtx();

  return (
    <>
      {sessionsError && (
        <div className="oc-error-banner">
          {sessionsError}
          <button onClick={() => loadSessions()}>Retry</button>
        </div>
      )}
      <div className="oc-time-range">
        {[{label: '12h', value: 12}, {label: '24h', value: 24}, {label: '7d', value: 168}, {label: '30d', value: 720}, {label: 'All', value: 0}].map((opt) => (
          <button
            key={opt.value}
            className={`oc-time-range-btn${timeRange === opt.value ? ' active' : ''}`}
            onClick={() => setTimeRange(opt.value)}
          >{opt.label}</button>
        ))}
        <button
          className={`oc-time-range-btn${showArchived ? ' active' : ''}`}
          onClick={() => setShowArchived(!showArchived)}
        >Exclude archived</button>
      </div>
      <SessionTable sessions={sessions} showProject loading={sessionsLoading && sessions.length === 0} includeArchived={!showArchived} />
    </>
  );
}

// ---------------------------------------------------------------------------
// Projects tab
// ---------------------------------------------------------------------------

export function ProjectsTab() {
  usePageTitle('Projects');
  const { projects, projectsLoading, dirScope, setDirScope } = useDashboardCtx();
  const navigate = useNavigate();

  // The picker is sourced from the full project list (so the user can
  // navigate up/down the tree); the table itself is filtered to the
  // active scope. matchesScope mirrors the SQL predicate used by the
  // backend (see spec/stats-project-filter/architecture.md, AD-7).
  const visibleProjects = projects
    .filter((p) => p.sessionCount > 0)
    .filter((p) => matchesScope(p.directory, dirScope));

  return projectsLoading && projects.length === 0 ? (
    <div className="oc-list-loading">
      <div className="oc-spinner" />
      Loading projects...
    </div>
  ) : (
    <div className="metrics-page" style={{ padding: 0 }}>
      <div className="metrics-filters">
        <ProjectScopePicker projects={projects} value={dirScope} onChange={setDirScope} />
      </div>
    <table>
      <thead>
        <tr>
          <th>Project</th>
          <th>Sessions</th>
          <th>Messages</th>
          <th>Tokens (in/out)</th>
          <th>Last Active</th>
        </tr>
      </thead>
      <tbody>
        {visibleProjects.length === 0 ? (
          <tr>
            <td colSpan={5} style={{ textAlign: 'center', color: 'var(--text-dim)', padding: 24 }}>
              No projects found
            </td>
          </tr>
        ) : visibleProjects.map((p) => (
          <tr key={p.directory} onClick={() => navigate(`/project/${encodeURIComponent(p.directory)}`)}>
            <td>
              <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
                <span style={{ color: 'var(--accent)', fontWeight: 500 }}>{shortPath(p.directory)}</span>
                <a
                  href={`vscode://file${p.directory}`}
                  className="vscode-btn"
                  title="Open in VS Code"
                  onClick={(e) => e.stopPropagation()}
                >VS Code</a>
              </div>
              <div className="mono">{p.directory}</div>
            </td>
            <td>{p.sessionCount}</td>
            <td>{p.messageCount}</td>
            <td className="mono">{formatNumber(p.totalTokensIn)} / {formatNumber(p.totalTokensOut)}</td>
            <td>{relativeTime(p.lastUsed)}</td>
          </tr>
        ))}
      </tbody>
    </table>
    </div>
  );
}

// ---------------------------------------------------------------------------
// Settings tab
// ---------------------------------------------------------------------------

export function SettingsTab() {
  usePageTitle('Settings');
  const bellEnabled = useUiStore((s) => s.bellEnabled);
  const setBellEnabled = useUiStore((s) => s.setBellEnabled);
  const notificationsEnabled = useUiStore((s) => s.notificationsEnabled);
  const setNotificationsEnabled = useUiStore((s) => s.setNotificationsEnabled);
  const authRequired = useAuthStore((s) => s.authRequired);
  const logout = useAuthStore((s) => s.logout);
  const { canInstall, installed, promptInstall } = usePwaInstall();

  // System notification state. Tracked locally so we can re-render
  // when permission changes (the browser API doesn't give us an event
  // for that, so we read it on each render and update after a request).
  const [notifPermission, setNotifPermission] = useState<NotificationPermission | 'unsupported'>(
    () => {
      if (!notificationsSupported()) return 'unsupported';
      return Notification.permission;
    },
  );

  const notifSupported = notifPermission !== 'unsupported';
  const notifBlocked = notifPermission === 'denied';

  async function handleNotificationsToggle(want: boolean) {
    if (!want) {
      setNotificationsEnabled(false);
      return;
    }
    // Turning on: ensure permission is granted first. If the user
    // previously denied it, the browser won't re-prompt — surface that
    // explicitly so the toggle doesn't silently fail.
    if (notifPermission === 'granted') {
      setNotificationsEnabled(true);
      return;
    }
    const result = await requestNotificationPermission();
    setNotifPermission(result);
    setNotificationsEnabled(result === 'granted');
  }

  // The "App" section only renders when there's something actionable
  // to show: an install button (Chromium, not yet installed) or an
  // "already installed" confirmation. On Safari/Firefox or before the
  // browser has decided the page is installable the section is hidden
  // entirely, keeping the settings page tidy.
  const showAppSection = canInstall || installed;

  return (
    <div className="settings-page">
      <div className="settings-section">
        <h2 className="settings-section-title">Notifications</h2>
        {notifSupported && (
          <div className="settings-row">
            <div className="settings-row-info">
              <div className="settings-row-label">System notifications</div>
              <div className="settings-row-desc">
                {notifBlocked
                  ? 'Notifications are blocked by your browser. Allow them in your browser\u2019s site settings to enable this option.'
                  : 'Show a desktop notification when a session finishes or needs your input. Works best after installing ocman as an app.'}
              </div>
            </div>
            <label className="settings-toggle">
              <input
                type="checkbox"
                checked={notificationsEnabled && notifPermission === 'granted'}
                disabled={notifBlocked}
                onChange={(e) => { void handleNotificationsToggle(e.target.checked); }}
              />
              <span className="settings-toggle-track" />
            </label>
          </div>
        )}
        <div className="settings-row">
          <div className="settings-row-info">
            <div className="settings-row-label">Bell sound</div>
            <div className="settings-row-desc">
              Play a bell sound when the app is not in focus and a session
              finishes or asks a question.
            </div>
          </div>
          <label className="settings-toggle">
            <input
              type="checkbox"
              checked={bellEnabled}
              onChange={(e) => setBellEnabled(e.target.checked)}
            />
            <span className="settings-toggle-track" />
          </label>
        </div>
      </div>

      {showAppSection && (
        <div className="settings-section">
          <h2 className="settings-section-title">App</h2>
          <div className="settings-row">
            <div className="settings-row-info">
              <div className="settings-row-label">Install ocman</div>
              <div className="settings-row-desc">
                {installed
                  ? 'ocman is installed as an app on this device. Launch it from your dock or app launcher to use it in its own window.'
                  : 'Install ocman as a standalone app with its own window and dock icon. The web version keeps working in any browser tab.'}
              </div>
            </div>
            <button
              type="button"
              className="vscode-btn"
              disabled={installed || !canInstall}
              onClick={() => { void promptInstall(); }}
            >
              {installed ? 'Installed' : 'Install'}
            </button>
          </div>
        </div>
      )}

      {authRequired && (
        <div className="settings-section">
          <h2 className="settings-section-title">Account</h2>
          <div className="settings-row">
            <div className="settings-row-info">
              <div className="settings-row-label">Session</div>
              <div className="settings-row-desc">Sign out of the current session.</div>
            </div>
            <button
              type="button"
              className="vscode-btn"
              onClick={() => { void logout(); }}
            >
              Sign out
            </button>
          </div>
        </div>
      )}
    </div>
  );
}

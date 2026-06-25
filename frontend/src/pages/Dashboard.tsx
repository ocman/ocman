import { useState, useCallback, useMemo, useRef, useEffect } from 'react';
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
import { SessionTable, GroupedSessionTable } from '../components/SessionTable';
import { ErrorBoundary } from '../components/ErrorBoundary';
import { ProjectScopePicker } from '../components/ProjectScopePicker';
import { matchesScope } from '../lib/projectTree';
import { PromptTemplateSettings } from '../components/upstream/PromptTemplateSettings';
import { RemoteSettings } from '../components/RemoteSettings';
import { GettingStartedEmpty } from '../components/GettingStartedEmpty';
import { SaveStatus } from '../components/SaveStatus';
import { useSaveStatus } from '../lib/useSaveStatus';

import { useUiStore } from '../lib/uiStore';
import { useApiStore } from '../lib/apiStore';
import { useAgentLoops } from '../lib/useCapabilities';
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
  const agentLoopsAllowed = useAgentLoops();

  const isOnDashboard = location.pathname === '/' || location.pathname === '/projects' || location.pathname === '/stats' || location.pathname === '/usage' || location.pathname === '/loops' || location.pathname === '/settings';

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
          <NavLink to="/" end className={({ isActive }) => `nav-tab${isActive ? ' active' : ''}`}>Sessions</NavLink>
          <NavLink to="/projects" className={({ isActive }) => `nav-tab${isActive ? ' active' : ''}`}>Projects</NavLink>
          <NavLink to="/stats" className={({ isActive }) => `nav-tab${isActive ? ' active' : ''}`}>Stats</NavLink>
          <NavLink to="/usage" className={({ isActive }) => `nav-tab${isActive ? ' active' : ''}`}>Usage</NavLink>
          {agentLoopsAllowed && (
            <NavLink to="/loops" className={({ isActive }) => `nav-tab${isActive ? ' active' : ''}`}>Loops</NavLink>
          )}
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
  const dashboardGrouped = useUiStore((s) => s.dashboardGrouped);
  const toggleDashboardGrouped = useUiStore((s) => s.toggleDashboardGrouped);
  const collapsedProjects = useUiStore((s) => s.collapsedProjects);
  const toggleCollapsedProject = useUiStore((s) => s.toggleCollapsedProject);
  const openProjectSessionPalette = useUiStore((s) => s.openProjectSessionPalette);

  const collapsedProjectSet = useMemo(
    () => new Set(collapsedProjects),
    [collapsedProjects],
  );

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
        <button
          className={`oc-time-range-btn${dashboardGrouped ? ' active' : ''}`}
          onClick={toggleDashboardGrouped}
          title="Group sessions by project"
        >Group by project</button>
        <button
          type="button"
          className="oc-time-range-btn oc-dashboard-create-btn"
          onClick={openProjectSessionPalette}
          title="Create a new OpenCode session in a known project"
        >
          <i className="bi bi-plus-lg" aria-hidden="true" />
          New session
        </button>
      </div>
      {dashboardGrouped ? (
        <GroupedSessionTable
          sessions={sessions}
          loading={sessionsLoading && sessions.length === 0}
          includeArchived={!showArchived}
          collapsedProjects={collapsedProjectSet}
          toggleCollapsedProject={toggleCollapsedProject}
        />
      ) : (
        <SessionTable sessions={sessions} showProject loading={sessionsLoading && sessions.length === 0} includeArchived={!showArchived} />
      )}
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
  const openProjectPalette = useUiStore((s) => s.openProjectPalette);

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
      <div className="metrics-filters oc-projects-toolbar">
        <ProjectScopePicker projects={projects} value={dirScope} onChange={setDirScope} />
        <button
          type="button"
          className="vscode-btn oc-dashboard-primary-action"
          onClick={openProjectPalette}
          title="Start a session in a project directory"
        >
          <i className="bi bi-folder-plus" aria-hidden="true" />
          New project
        </button>
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
            <td colSpan={5} style={{ padding: 24 }}>
              {projects.length === 0 ? (
                <GettingStartedEmpty />
              ) : (
                <div style={{ textAlign: 'center', color: 'var(--text-dim)' }}>
                  No projects found
                </div>
              )}
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
// Prompt section editor (used inside SettingsTab)
// ---------------------------------------------------------------------------

function PromptSectionEditor({
  section,
  onChange,
  onRemove,
}: {
  section: { title: string; content: string; enabled?: boolean };
  onChange: (s: { title: string; content: string; enabled?: boolean }) => void;
  onRemove: () => void;
}) {
  // Track textarea height so it grows with content.
  const textareaRef = useRef<HTMLTextAreaElement>(null);
  // Missing `enabled` (legacy rows) is treated as enabled.
  const enabled = section.enabled !== false;
  return (
    <div className="settings-prompt-section">
      <div className="settings-prompt-section-header">
        <label className="settings-prompt-section-toggle">
          <input
            type="checkbox"
            checked={enabled}
            aria-label="Enable rule"
            onChange={(e) => onChange({ ...section, enabled: e.target.checked })}
          />
          <span aria-hidden="true" />
        </label>
        <input
          type="text"
          className="settings-prompt-section-title"
          placeholder="Section title"
          value={section.title}
          onChange={(e) => onChange({ ...section, title: e.target.value })}
        />
        <button
          type="button"
          className="settings-prompt-section-remove"
          aria-label="Remove section"
          onClick={onRemove}
        >
          &#x2715;
        </button>
      </div>
      <textarea
        ref={textareaRef}
        className="settings-prompt-section-content"
        placeholder="Describe the rule in plain language. The AI reviewer will follow this as an additional instruction."
        value={section.content}
        rows={3}
        onChange={(e) => {
          onChange({ ...section, content: e.target.value });
          // Auto-grow: reset height first so shrinking works too.
          if (textareaRef.current) {
            textareaRef.current.style.height = 'auto';
            textareaRef.current.style.height = `${textareaRef.current.scrollHeight}px`;
          }
        }}
      />
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
  const autoApproveDefault = useUiStore((s) => s.autoApproveDefault);
  const setAutoApproveDefault = useUiStore((s) => s.setAutoApproveDefault);
  const autoApproveDelayMs = useUiStore((s) => s.autoApproveDelayMs);
  const setAutoApproveDelayMs = useUiStore((s) => s.setAutoApproveDelayMs);
  const dashboardTimeRangeDefault = useUiStore((s) => s.dashboardTimeRangeDefault);
  const setDashboardTimeRangeDefault = useUiStore((s) => s.setDashboardTimeRangeDefault);
  const sidebarRecentHours = useUiStore((s) => s.sidebarRecentHours);
  const setSidebarRecentHours = useUiStore((s) => s.setSidebarRecentHours);
  const promptSections = useUiStore((s) => s.promptSections);
  const setPromptSections = useUiStore((s) => s.setPromptSections);
  const getPromptSections = useApiStore((s) => s.getPromptSections);
  const setPromptSectionsApi = useApiStore((s) => s.setPromptSectionsApi);
  const getJudgeDelay = useApiStore((s) => s.getJudgeDelay);
  const setJudgeDelayApi = useApiStore((s) => s.setJudgeDelayApi);
  const authRequired = useAuthStore((s) => s.authRequired);

  // On mount, load settings from the server and sync to uiStore.
  // This ensures the settings page reflects what the backend judge actually uses,
  // even if another client or direct API call changed them.
  useEffect(() => {
    getPromptSections().then((serverSections) => {
      setPromptSections(serverSections);
    }).catch(() => { /* best-effort — uiStore value survives */ });
    getJudgeDelay().then((ms) => {
      setAutoApproveDelayMs(ms);
    }).catch(() => { /* best-effort — uiStore value survives */ });
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);
  const logout = useAuthStore((s) => s.logout);
  const { canInstall, installed, promptInstall } = usePwaInstall();

  // Per-field save status (spinner while saving, checkmark for 5s after).
  const delaySave = useSaveStatus();
  const sectionsSave = useSaveStatus();

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

  // Sidebar groups. Conditional groups (App, Account) are filtered out so
  // the nav only lists what's actually rendered.
  const groups = [
    { id: 'notifications', label: 'Notifications', show: true },
    { id: 'sessions', label: 'Sessions', show: true },
    { id: 'remotes', label: 'Remotes', show: true },
    { id: 'auto-approve', label: 'Auto-approve', show: true },
    { id: 'templates', label: 'PR & Issue templates', show: true },
    { id: 'app', label: 'App', show: showAppSection },
    { id: 'account', label: 'Account', show: authRequired },
  ].filter((g) => g.show);
  const [active, setActive] = useState(groups[0].id);

  return (
    <div className="settings-page">
      <nav className="settings-nav" aria-label="Settings groups">
        {groups.map((g) => (
          <button
            key={g.id}
            type="button"
            className={`settings-nav-item${active === g.id ? ' active' : ''}`}
            aria-current={active === g.id ? 'page' : undefined}
            onClick={() => setActive(g.id)}
          >
            {g.label}
          </button>
        ))}
      </nav>
      <div className="settings-content">
      <div className="settings-section" hidden={active !== 'notifications'}>
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

      <div className="settings-section" hidden={active !== 'sessions'}>
        <h2 className="settings-section-title">Sessions</h2>
        <div className="settings-row">
          <div className="settings-row-info">
            <div className="settings-row-label">Start screen time range</div>
            <div className="settings-row-desc">
              Default lookback window for the Sessions list on the start screen.
              The time-range buttons still override it for the current view.
            </div>
          </div>
          <div className="settings-delay-input">
            <input
              type="number"
              min={1}
              max={365}
              step={1}
              aria-label="Start screen time range in days"
              value={Math.round((dashboardTimeRangeDefault / 24) * 10) / 10}
              onChange={(e) => setDashboardTimeRangeDefault((Number(e.target.value) || 0) * 24)}
            />
            <span className="settings-delay-unit">days</span>
          </div>
        </div>
        <div className="settings-row">
          <div className="settings-row-info">
            <div className="settings-row-label">Recent sessions window</div>
            <div className="settings-row-desc">
              How far back the &ldquo;Recent sessions&rdquo; sidebar looks while
              you&apos;re inside a session.
            </div>
          </div>
          <div className="settings-delay-input">
            <input
              type="number"
              min={1}
              max={365}
              step={1}
              aria-label="Recent sessions window in days"
              value={Math.round((sidebarRecentHours / 24) * 10) / 10}
              onChange={(e) => setSidebarRecentHours((Number(e.target.value) || 0) * 24)}
            />
            <span className="settings-delay-unit">days</span>
          </div>
        </div>
      </div>

      <div className="settings-section" hidden={active !== 'remotes'}>
        <h2 className="settings-section-title">Remotes</h2>
        <div className="settings-row-desc" style={{ marginBottom: 8 }}>
          Attach other ocman instances to manage their sessions from here.
          Copy a remote&rsquo;s access token from its own Settings page (run it
          with <code>-remote-listen</code>) and paste it below.
        </div>
        <RemoteSettings />
      </div>

      <div className="settings-section" hidden={active !== 'auto-approve'}>
        <h2 className="settings-section-title">Auto-approve</h2>
        <div className="settings-row">
          <div className="settings-row-info">
            <div className="settings-row-label">Enable by default</div>
            <div className="settings-row-desc">
              Automatically start the AI permission reviewer for every new session.
              You can also enable or disable it per session from the permission prompt.
            </div>
          </div>
          <label className="settings-toggle">
            <input
              type="checkbox"
              checked={autoApproveDefault}
              onChange={(e) => setAutoApproveDefault(e.target.checked)}
            />
            <span className="settings-toggle-track" />
          </label>
        </div>
        <div className="settings-row">
          <div className="settings-row-info">
            <div className="settings-row-label">Human review window</div>
            <div className="settings-row-desc">
              How long to wait after a permission prompt appears before the AI
              reviewer starts. Gives you time to approve or reject manually.
            </div>
          </div>
          <div className="settings-delay-input">
            <input
              type="number"
              min={0}
              max={60}
              step={1}
              value={Math.round(autoApproveDelayMs / 1000)}
              onChange={(e) => {
                const secs = Math.max(0, Math.min(60, Number(e.target.value) || 0));
                const ms = secs * 1000;
                setAutoApproveDelayMs(ms);
                void delaySave.track(() => setJudgeDelayApi(ms));
              }}
            />
            <span className="settings-delay-unit">s</span>
            <SaveStatus state={delaySave.state} />
          </div>
        </div>

        <div className="settings-row settings-row--block">
          <div className="settings-row-info">
            <div className="settings-row-label">
              Reviewer prompt sections
              <SaveStatus state={sectionsSave.state} />
            </div>
            <div className="settings-row-desc">
              Extra rules appended to the AI reviewer&apos;s prompt. Each section
              appears as a named block the model reads before deciding. Use this
              to allow or deny specific patterns your team knows are safe.
            </div>
          </div>
          <div className="settings-prompt-sections">
            {promptSections.map((section, i) => (
              <PromptSectionEditor
                key={i}
                section={section}
                onChange={(updated) => {
                  const next = [...promptSections];
                  next[i] = updated;
                  setPromptSections(next);
                  void sectionsSave.track(() => setPromptSectionsApi(next));
                }}
                onRemove={() => {
                  const next = promptSections.filter((_, j) => j !== i);
                  setPromptSections(next);
                  void sectionsSave.track(() => setPromptSectionsApi(next));
                }}
              />
            ))}
            <button
              type="button"
              className="settings-prompt-add"
              onClick={() => {
                const next = [...promptSections, { title: '', content: '' }];
                setPromptSections(next);
                void sectionsSave.track(() => setPromptSectionsApi(next));
              }}
            >
              + Add section
            </button>
          </div>
        </div>
      </div>

      <div className="settings-section" hidden={active !== 'templates'}>
        <h2 className="settings-section-title">PR &amp; Issue templates</h2>
        <div className="settings-row settings-row--block">
          <div className="settings-row-info">
            <div className="settings-row-label">Launch prompt templates</div>
            <div className="settings-row-desc">
              The prompt sent to a new agent session when you click
              &ldquo;Handle this PR/Issue&rdquo; in the sidebar. Edit the
              templates below; placeholders are substituted at launch
              time.
            </div>
          </div>
          <PromptTemplateSettings />
        </div>
      </div>

      {showAppSection && (
        <div className="settings-section" hidden={active !== 'app'}>
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
        <div className="settings-section" hidden={active !== 'account'}>
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
    </div>
  );
}

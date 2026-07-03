import { useState, useCallback, useMemo, useRef, useEffect } from 'react';
import './Dashboard.css';
import { useNavigate, NavLink, Outlet, useSearchParams, useLocation } from 'react-router-dom';
import { Chart as ChartJS, CategoryScale, LinearScale, BarElement, ArcElement, Tooltip, Legend, PointElement, LineElement } from 'chart.js';
import type { Project, Session } from '../lib/api';
import {
  cleanTitle,
  formatNumber,
  fuzzyMatch,
  relativeTime,
  shortPath,
} from '../lib/format';
import { usePageTitle } from '../lib/headerContext';
import { SessionTable } from '../components/SessionTable';
import { ErrorBoundary } from '../components/ErrorBoundary';
import { ProjectScopePicker } from '../components/ProjectScopePicker';
import { matchesScope } from '../lib/projectTree';
import { PromptTemplateSettings } from '../components/upstream/PromptTemplateSettings';
import { RemoteSettings } from '../components/RemoteSettings';
import { SharingSettings } from '../components/SharingSettings';
import { GettingStartedEmpty } from '../components/GettingStartedEmpty';
import { SaveStatus } from '../components/SaveStatus';
import { SettingRow, SettingToggle, SettingNumber } from '../components/SettingRow';
import { useSaveStatus, useSettingSave } from '../lib/useSaveStatus';

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
          {agentLoopsAllowed && (
            <NavLink to="/loops" className={({ isActive }) => `nav-tab${isActive ? ' active' : ''}`}>Loops</NavLink>
          )}
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
// Shared dashboard toolbar: project scope picker + fuzzy search + a
// primary "create" action. Used by both the Sessions and Projects tabs.
// ---------------------------------------------------------------------------

export function DashboardToolbar({
  projects,
  dirScope,
  setDirScope,
  search,
  setSearch,
  searchLabel,
  actionIcon,
  actionLabel,
  actionTitle,
  onAction,
}: {
  projects: Project[];
  dirScope: string;
  setDirScope: (v: string) => void;
  search: string;
  setSearch: (v: string) => void;
  searchLabel: string;
  actionIcon: string;
  actionLabel: string;
  actionTitle: string;
  onAction: () => void;
}) {
  return (
    <div className="metrics-filters oc-projects-toolbar">
      <ProjectScopePicker projects={projects} value={dirScope} onChange={setDirScope} />
      <input
        type="search"
        className="oc-project-search"
        placeholder={`${searchLabel}\u2026`}
        aria-label={searchLabel}
        value={search}
        onChange={(e) => setSearch(e.target.value)}
      />
      <button
        type="button"
        className="vscode-btn oc-dashboard-primary-action"
        onClick={onAction}
        title={actionTitle}
      >
        <i className={`bi ${actionIcon}`} aria-hidden="true" />
        {actionLabel}
      </button>
    </div>
  );
}

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

// ---------------------------------------------------------------------------
// Projects tab
// ---------------------------------------------------------------------------

export function ProjectsTab() {
  usePageTitle('Projects');
  const { projects, projectsLoading, dirScope, setDirScope } = useDashboardCtx();
  const navigate = useNavigate();
  const openProjectPalette = useUiStore((s) => s.openProjectPalette);
  const [search, setSearch] = useState('');

  // The picker is sourced from the full project list (so the user can
  // navigate up/down the tree); the table itself is filtered to the
  // active scope. matchesScope mirrors the SQL predicate used by the
  // backend (see spec/stats-project-filter/architecture.md, AD-7).
  const q = search.trim();
  const visibleProjects = projects
    .filter((p) => p.sessionCount > 0)
    .filter((p) => matchesScope(p.directory, dirScope))
    .filter((p) => !q || fuzzyMatch(q, p.directory));

  return projectsLoading && projects.length === 0 ? (
    <div className="oc-list-loading">
      <div className="oc-spinner" />
      Loading projects...
    </div>
  ) : (
    <div className="metrics-page" style={{ padding: 0 }}>
      <DashboardToolbar
        projects={projects}
        dirScope={dirScope}
        setDirScope={setDirScope}
        search={search}
        setSearch={setSearch}
        searchLabel="Search projects"
        actionIcon="bi-folder-plus"
        actionLabel="New project"
        actionTitle="Start a session in a project directory"
        onAction={openProjectPalette}
      />
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
  const notifSave = useSettingSave();
  const bellSave = useSettingSave();
  const timeRangeSave = useSettingSave();
  const recentSave = useSettingSave();
  const autoApproveSave = useSettingSave();

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
    { id: 'sharing', label: 'Sharing', show: true },
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
          <SettingRow
            label="System notifications"
            desc={notifBlocked
              ? 'Notifications are blocked by your browser. Allow them in your browser\u2019s site settings to enable this option.'
              : 'Show a desktop notification when a session finishes or needs your input. Works best after installing ocman as an app.'}
          >
            <SettingToggle
              ariaLabel="System notifications"
              checked={notificationsEnabled && notifPermission === 'granted'}
              disabled={notifBlocked}
              save={notifSave}
              onSave={(next) => handleNotificationsToggle(next)}
            />
          </SettingRow>
        )}
        <SettingRow
          label="Bell sound"
          desc="Play a bell sound when the app is not in focus and a session finishes or asks a question."
        >
          <SettingToggle
            ariaLabel="Bell sound"
            checked={bellEnabled}
            save={bellSave}
            onSave={(next) => setBellEnabled(next)}
          />
        </SettingRow>
      </div>

      <div className="settings-section" hidden={active !== 'sessions'}>
        <h2 className="settings-section-title">Sessions</h2>
        <SettingRow
          label="Start screen time range"
          desc="Default lookback window for the Sessions list on the start screen. The time-range buttons still override it for the current view."
        >
          <SettingNumber
            ariaLabel="Start screen time range in days"
            unit="days"
            min={1}
            max={365}
            value={Math.round((dashboardTimeRangeDefault / 24) * 10) / 10}
            parse={(raw) => raw * 24}
            save={timeRangeSave}
            onSave={(next) => setDashboardTimeRangeDefault(next)}
          />
        </SettingRow>
        <SettingRow
          label="Recent sessions window"
          desc={<>How far back the &ldquo;Recent sessions&rdquo; sidebar looks while you&apos;re inside a session.</>}
        >
          <SettingNumber
            ariaLabel="Recent sessions window in days"
            unit="days"
            min={1}
            max={365}
            value={Math.round((sidebarRecentHours / 24) * 10) / 10}
            parse={(raw) => raw * 24}
            save={recentSave}
            onSave={(next) => setSidebarRecentHours(next)}
          />
        </SettingRow>
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
        <SettingRow
          label="Enable by default"
          desc="Automatically start the AI permission reviewer for every new session. You can also enable or disable it per session from the permission prompt."
        >
          <SettingToggle
            ariaLabel="Enable auto-approve by default"
            checked={autoApproveDefault}
            save={autoApproveSave}
            onSave={(next) => setAutoApproveDefault(next)}
          />
        </SettingRow>
        <SettingRow
          label="Human review window"
          desc="How long to wait after a permission prompt appears before the AI reviewer starts. Gives you time to approve or reject manually."
        >
          <SettingNumber
            ariaLabel="Human review window in seconds"
            unit="s"
            min={0}
            max={60}
            value={Math.round(autoApproveDelayMs / 1000)}
            parse={(raw) => Math.max(0, Math.min(60, raw)) * 1000}
            save={delaySave}
            onSave={(ms) => {
              setAutoApproveDelayMs(ms);
              return setJudgeDelayApi(ms);
            }}
          />
        </SettingRow>

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

      <div className="settings-section" hidden={active !== 'sharing'}>
        <h2 className="settings-section-title">Sharing</h2>
        <SharingSettings />
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
          <div className="settings-row"> {/* ocman:allow-raw-setting — action button, no saved value */}
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
          <div className="settings-row"> {/* ocman:allow-raw-setting — action button, no saved value */}
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

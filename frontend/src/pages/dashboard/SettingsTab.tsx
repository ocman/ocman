import { useState, useRef, useEffect } from 'react';
import { usePageTitle } from '../../lib/headerContext';
import { PromptTemplateSettings } from '../../components/upstream/PromptTemplateSettings';
import { RemoteSettings } from '../../components/RemoteSettings';
import { SharingSettings } from '../../components/SharingSettings';
import { SaveStatus } from '../../components/SaveStatus';
import { SettingRow, SettingToggle, SettingNumber } from '../../components/SettingRow';
import { useSaveStatus, useSettingSave } from '../../lib/useSaveStatus';
import { useUiStore } from '../../lib/uiStore';
import { useApiStore } from '../../lib/apiStore';
import { useAuthStore } from '../../lib/authStore';
import { usePwaInstall } from '../../lib/usePwaInstall';
import {
  notificationsSupported,
  requestNotificationPermission,
} from '../../lib/useNotificationNotify';

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

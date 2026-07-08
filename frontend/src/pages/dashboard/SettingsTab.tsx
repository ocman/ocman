import { useState, useEffect } from 'react';
import { usePageTitle } from '../../lib/headerContext';
import { PromptTemplateSettings } from '../../components/upstream/PromptTemplateSettings';
import { RemoteSettings } from '../../components/RemoteSettings';
import { SharingSettings } from '../../components/SharingSettings';
import { useAuthStore } from '../../lib/authStore';
import { useUiStore } from '../../lib/uiStore';
import { useApiStore } from '../../lib/apiStore';
import { usePwaInstall } from '../../lib/usePwaInstall';
import {
  NotificationsSection,
  SessionsSection,
  AutoApproveSection,
} from './SettingsSections';

export function SettingsTab() {
  usePageTitle('Settings');

  // On mount, load settings from the server and sync to uiStore so the
  // settings page reflects what the backend judge actually uses, even if
  // another client or direct API call changed them.
  const setPromptSections = useUiStore((s) => s.setPromptSections);
  const setAutoApproveDelayMs = useUiStore((s) => s.setAutoApproveDelayMs);
  const getPromptSections = useApiStore((s) => s.getPromptSections);
  const getJudgeDelay = useApiStore((s) => s.getJudgeDelay);
  useEffect(() => {
    getPromptSections().then(setPromptSections).catch(() => { /* best-effort */ });
    getJudgeDelay().then(setAutoApproveDelayMs).catch(() => { /* best-effort */ });
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const authRequired = useAuthStore((s) => s.authRequired);
  const logout = useAuthStore((s) => s.logout);
  const { canInstall, installed, promptInstall } = usePwaInstall();

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
          <NotificationsSection />
        </div>

        <div className="settings-section" hidden={active !== 'sessions'}>
          <h2 className="settings-section-title">Sessions</h2>
          <SessionsSection />
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
          <AutoApproveSection />
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

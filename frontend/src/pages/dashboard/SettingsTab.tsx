import { useState, useEffect } from 'react';
import { usePageTitle } from '../../lib/headerContext';
import { PromptTemplateSettings } from '../../components/upstream/PromptTemplateSettings';
import { RemoteSettings } from '../../components/RemoteSettings';
import { SharingSettings } from '../../components/SharingSettings';
import { WebhookRelaySettings } from '../../components/WebhookRelaySettings';
import { PluginSettings } from '../../components/PluginSettings';
import { MaintenanceSettings } from '../../components/MaintenanceSettings';
import { useAuthStore } from '../../lib/authStore';
import { useUiStore } from '../../lib/uiStore';
import { useApiStore } from '../../lib/apiStore';
import { usePwaInstall } from '../../lib/usePwaInstall';
import { SettingRow, SettingToggle, SettingDescription } from '../../components/SettingRow';
import { useSettingSave } from '../../lib/useSaveStatus';
import { getOpenInChrome, isIOS, setOpenInChrome } from '../../lib/externalLinks';
import { Button, SearchField } from '../../components/Control';
import { SettingsSearchResults } from '../../components/SettingsSearch';
import { useRevealSetting, type RevealRequest } from '../../lib/useRevealSetting';
import { settingEntry, type SettingId, type SettingsGroupId } from '../../lib/settingsCatalog';
import { LinkPreviewTabs } from '../../components/LinkPreviewTabs';
import { NotificationsSection, SessionsSection } from './SettingsSections';
import { AutoApproveSection } from './AutoApproveSection';
import './SettingsTab.css';

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
  // Behaviour holds only the iOS-only Chrome toggle, so it is hidden elsewhere.
  const showBehaviour = isIOS();
  const [openInChrome, setOpenInChromeState] = useState(getOpenInChrome);
  const chromeSave = useSettingSave();

  // Sidebar groups. Conditional groups (App, Account) are filtered out so
  // the nav only lists what's actually rendered.
  const groups = ([
    { id: 'notifications', label: 'Notifications', show: true },
    { id: 'sessions', label: 'Sessions', show: true },
    { id: 'remotes', label: 'Remotes', show: true },
    { id: 'plugins', label: 'Plugins', show: true },
    { id: 'auto-approve', label: 'Auto-approve', show: true },
    { id: 'sharing', label: 'Sharing', show: true },
    { id: 'webhooks', label: 'Webhooks', show: true },
    { id: 'templates', label: 'PR & Issue templates', show: true },
    { id: 'link-previews', label: 'Link previews', show: true },
    { id: 'maintenance', label: 'Maintenance', show: true },
    { id: 'behaviour', label: 'Behaviour', show: showBehaviour },
    { id: 'app', label: 'App', show: showAppSection },
    { id: 'account', label: 'Account', show: authRequired },
  ] satisfies Array<{ id: SettingsGroupId; label: string; show: boolean }>).filter((g) => g.show);
  const [active, setActive] = useState<SettingsGroupId>(groups[0].id);
  const [query, setQuery] = useState('');
  const [target, setTarget] = useState<RevealRequest | null>(null);
  const searching = query.trim() !== '';
  const groupLabels = Object.fromEntries(groups.map((g) => [g.id, g.label]));
  const found = useRevealSetting(target);
  const missing = target && !found ? settingEntry(target.id) : null;

  const pick = (id: SettingId) => {
    setQuery('');
    setActive(settingEntry(id).group);
    setTarget((prev) => ({ id, seq: (prev?.seq ?? 0) + 1 }));
  };

  return (
    <div className="settings-page">
      <nav className="settings-nav" aria-label="Settings groups">
        <SearchField
          className="settings-search"
          aria-label="Search settings"
          placeholder="Search settings"
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          onKeyDown={(e) => { if (e.key === 'Escape') setQuery(''); }}
        />
        {groups.map((g) => (
          <Button variant="ghost"
            key={g.id}
            type="button"
            className={`settings-nav-item${active === g.id && !searching ? ' active' : ''}`}
            aria-current={active === g.id && !searching ? 'page' : undefined}
            onClick={() => { setQuery(''); setTarget(null); setActive(g.id); }}
          >
            {g.label}
          </Button>
        ))}
      </nav>
      <div className="settings-content">
        {searching && <SettingsSearchResults query={query} groupLabels={groupLabels} onPick={pick} />}
        <div hidden={searching}>
        {missing?.requires && (
          <SettingDescription role="status">
            <strong>{missing.title}</strong> is not shown right now. {missing.requires}
          </SettingDescription>
        )}
        {active === 'plugins' && <div className="settings-section">
          <h2 className="settings-section-title">Plugins</h2>
          <PluginSettings />
        </div>}
        {active === 'maintenance' && <div className="settings-section">
          <h2 className="settings-section-title">Maintenance</h2>
          <MaintenanceSettings />
        </div>}
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
          <SettingRow block setting="remotes">
            <RemoteSettings />
          </SettingRow>
        </div>

        <div className="settings-section" hidden={active !== 'auto-approve'}>
          <h2 className="settings-section-title">Auto-approve</h2>
          <AutoApproveSection />
        </div>

        <div className="settings-section" hidden={active !== 'sharing'}>
          <h2 className="settings-section-title">Sharing</h2>
          <SharingSettings />
        </div>

        {active === 'webhooks' && <div className="settings-section">
          <h2 className="settings-section-title">Webhooks</h2>
          <WebhookRelaySettings />
        </div>}

        <div className="settings-section" hidden={active !== 'templates'}>
          <h2 className="settings-section-title">PR &amp; Issue templates</h2>
          <SettingRow block setting="launch-prompt-templates">
            <PromptTemplateSettings />
          </SettingRow>
        </div>

        {active === 'link-previews' && <div className="settings-section">
          <h2 className="settings-section-title">Link previews</h2>
          <LinkPreviewTabs key={target?.seq ?? ''} tab={target ? settingEntry(target.id).tab : undefined} />
        </div>}

        {showBehaviour && (
          <div className="settings-section" hidden={active !== 'behaviour'}>
            <h2 className="settings-section-title">Behaviour</h2>
            <SettingRow setting="open-links-in-chrome">
              <SettingToggle
                ariaLabel="Open external links in Chrome"
                checked={openInChrome}
                save={chromeSave}
                onSave={(next) => { setOpenInChrome(next); setOpenInChromeState(next); }}
              />
            </SettingRow>
          </div>
        )}

        {showAppSection && (
          <div className="settings-section" hidden={active !== 'app'}>
            <h2 className="settings-section-title">App</h2>
            <SettingRow
              setting="install-app"
              desc={installed
                ? 'ocman is installed as an app on this device. Launch it from your dock or app launcher to use it in its own window.'
                : undefined}
            >
              <Button
                type="button"
                size="small"
                disabled={installed || !canInstall}
                onClick={() => { void promptInstall(); }}
              >
                {installed ? 'Installed' : 'Install'}
              </Button>
            </SettingRow>
          </div>
        )}

        {authRequired && (
          <div className="settings-section" hidden={active !== 'account'}>
            <h2 className="settings-section-title">Account</h2>
            <SettingRow setting="sign-out">
              <Button
                type="button"
                size="small"
                onClick={() => { void logout(); }}
              >
                Sign out
              </Button>
            </SettingRow>
          </div>
        )}
        </div>
      </div>
    </div>
  );
}

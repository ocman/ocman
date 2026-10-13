import { useState } from 'react';
import { usePageTitle } from '../../lib/headerContext';
import { PromptTemplateSettings } from '../../components/upstream/PromptTemplateSettings';
import { RemoteSettings } from '../../components/RemoteSettings';
import { MaintenanceSettings } from '../../components/MaintenanceSettings';
import { useAuthStore } from '../../lib/authStore';
import { usePwaInstall } from '../../lib/usePwaInstall';
import { SettingRow, SettingToggle, SettingDescription } from '../../components/SettingRow';
import { useSettingSave } from '../../lib/useSaveStatus';
import { getOpenInChrome, isIOS, setOpenInChrome } from '../../lib/externalLinks';
import { Button, SearchField, SelectField } from '../../components/Control';
import { SettingsSearchResults } from '../../components/SettingsSearch';
import { useRevealSetting, type RevealRequest } from '../../lib/useRevealSetting';
import { settingEntry, type SettingId, type SettingsGroupId } from '../../lib/settingsCatalog';
import { LinkPreviewTabs } from '../../components/LinkPreviewTabs';
import { NotificationsSection, SessionsSection } from './SettingsSections';
import { featureSettings } from '../../features';
import styles from './SettingsTab.module.css';

export function SettingsTab() {
  usePageTitle('Settings');

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
    ...featureSettings.map(({ id, label }) => ({ id, label, show: true })),
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
  const selectGroup = (id: SettingsGroupId) => {
    setQuery('');
    setTarget(null);
    setActive(id);
  };

  return (
    <div className={styles.page}>
      <nav className={styles.nav} aria-label="Settings groups">
        <SearchField
          className={styles.search}
          aria-label="Search settings"
          placeholder="Search settings"
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          onKeyDown={(e) => { if (e.key === 'Escape') setQuery(''); }}
        />
        <label className={styles.compact}>
          <span className={styles.caption}>Settings group</span>
          <SelectField aria-label="Settings group" value={searching ? '' : active} onChange={(event) => selectGroup(event.target.value as SettingsGroupId)}>
            {searching && <option value="" disabled>Search results</option>}
            {groups.map((group) => <option key={group.id} value={group.id}>{group.label}</option>)}
          </SelectField>
        </label>
        <div className={styles.groups}>{groups.map((g) => (
          <Button
            key={g.id}
            type="button"
            className={styles.group}
            variant={active === g.id && !searching ? 'accent' : 'ghost'}
            aria-current={active === g.id && !searching ? 'page' : undefined}
            onClick={() => selectGroup(g.id)}
          >
            {g.label}
          </Button>
        ))}</div>
      </nav>
      <div className={styles.content}>
        {searching && <SettingsSearchResults query={query} groupLabels={groupLabels} onPick={pick} />}
        <div hidden={searching}>
        {missing?.requires && (
          <SettingDescription role="status">
            <strong>{missing.title}</strong> is not shown right now. {missing.requires}
          </SettingDescription>
        )}
        {featureSettings.map(({ id, label, mount, Section }) => (mount === 'always' || active === id) && (
          <div key={id} className={styles.section} hidden={active !== id}>
            <h2 className={styles.sectionTitle}>{label}</h2>
            <Section />
          </div>
        ))}
        {active === 'maintenance' && <div className={styles.section}>
          <h2 className={styles.sectionTitle}>Maintenance</h2>
          <MaintenanceSettings />
        </div>}
        <div className={styles.section} hidden={active !== 'notifications'}>
          <h2 className={styles.sectionTitle}>Notifications</h2>
          <NotificationsSection />
        </div>

        <div className={styles.section} hidden={active !== 'sessions'}>
          <h2 className={styles.sectionTitle}>Sessions</h2>
          <SessionsSection />
        </div>

        <div className={styles.section} hidden={active !== 'remotes'}>
          <h2 className={styles.sectionTitle}>Remotes</h2>
          <SettingRow block setting="remotes">
            <RemoteSettings />
          </SettingRow>
        </div>

        <div className={styles.section} hidden={active !== 'templates'}>
          <h2 className={styles.sectionTitle}>PR &amp; Issue templates</h2>
          <SettingRow block setting="launch-prompt-templates">
            <PromptTemplateSettings />
          </SettingRow>
        </div>

        {active === 'link-previews' && <div className={styles.section}>
          <h2 className={styles.sectionTitle}>Link previews</h2>
          <LinkPreviewTabs key={target?.seq ?? ''} tab={target ? settingEntry(target.id).tab : undefined} />
        </div>}

        {showBehaviour && (
          <div className={styles.section} hidden={active !== 'behaviour'}>
            <h2 className={styles.sectionTitle}>Behaviour</h2>
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
          <div className={styles.section} hidden={active !== 'app'}>
            <h2 className={styles.sectionTitle}>App</h2>
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
          <div className={styles.section} hidden={active !== 'account'}>
            <h2 className={styles.sectionTitle}>Account</h2>
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

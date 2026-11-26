import { useState } from 'react';
import { useNavigationType, useSearchParams } from 'react-router-dom';
import { usePageTitle } from '../../lib/headerContext';
import { useAuthStore } from '../../lib/authStore';
import { usePwaInstall } from '../../lib/usePwaInstall';
import { SettingDescription } from '../../components/SettingRow';
import { isIOS } from '../../lib/externalLinks';
import { Button, SearchField, SelectField } from '../../components/Control';
import { SettingsSearchResults } from '../../components/SettingsSearch';
import { SettingsSection } from '../../components/SettingsSection';
import { useRevealSetting, type RevealRequest } from '../../lib/useRevealSetting';
import { settingEntry, type SettingId, type SettingsGroupId } from '../../lib/settingsCatalog';
import { SETTINGS_CATEGORIES, settingsCategory, settingsCategoryLabels } from '../../lib/settingsNavigation';
import { featureSettings } from '../../features';
import styles from './SettingsTab.module.css';

export function SettingsTab() {
  usePageTitle('Settings');
  const authRequired = useAuthStore((s) => s.authRequired);
  const { canInstall, installed } = usePwaInstall();
  const visibility = { device: isIOS() || canInstall || installed, account: authRequired };
  const [params, setParams] = useSearchParams();
  const active = settingsCategory(params.get('tab'));
  const query = params.get('q') ?? '';
  const updateView = (tab: SettingsGroupId, query: string, replace = true) => {
    setParams((prev) => {
      const next = new URLSearchParams(prev);
      next.set('tab', tab);
      if (query) next.set('q', query); else next.delete('q');
      return next;
    }, { replace });
  };
  const [request, setRequest] = useState<RevealRequest | null>(null);
  const searching = query.trim() !== '';
  const navigation = useNavigationType();
  // Back/Forward restores the view, not the one-off focus and scroll jump.
  const target = navigation !== 'POP' && !searching && request && settingEntry(request.id).group === active ? request : null;
  const found = useRevealSetting(target);
  const missing = target && !found ? settingEntry(target.id) : null;
  const pick = (id: SettingId) => {
    const group = settingEntry(id).group;
    updateView(group, '', group === active);
    setRequest((prev) => ({ id, seq: (prev?.seq ?? 0) + 1 }));
  };
  const selectGroup = (id: SettingsGroupId) => {
    updateView(id, '', id === active);
    setRequest(null);
  };
  const search = (value: string) => {
    updateView(active, value);
    setRequest(null);
  };

  return <div className={styles.page}>
    <nav className={styles.nav} aria-label="Settings groups">
      <SearchField className={styles.search} aria-label="Search settings" placeholder="Search settings"
        value={query} onChange={(e) => search(e.target.value)}
        onKeyDown={(e) => { if (e.key === 'Escape') search(''); }} />
      <label className={styles.compact}>
        <span className={styles.caption}>Settings group</span>
        <SelectField aria-label="Settings group" value={searching ? '' : active}
          onChange={(event) => selectGroup(event.target.value as SettingsGroupId)}>
          {searching && <option value="" disabled>Search results</option>}
          {SETTINGS_CATEGORIES.map((group) => <option key={group.id} value={group.id}>{group.label}</option>)}
        </SelectField>
      </label>
      <div className={styles.groups}>{SETTINGS_CATEGORIES.map((group) => <Button key={group.id} type="button"
        className={styles.group} variant={active === group.id && !searching ? 'accent' : 'ghost'}
        aria-current={active === group.id && !searching ? 'page' : undefined}
        onClick={() => selectGroup(group.id)}>{group.label}</Button>)}</div>
    </nav>
    <div className={styles.content}>
      {searching && <SettingsSearchResults query={query} groupLabels={settingsCategoryLabels} onPick={pick} />}
      <div hidden={searching}>
        <h2 className={styles.categoryTitle}>{settingsCategoryLabels[active]}</h2>
        {missing?.requires && <SettingDescription role="status">
          <strong>{missing.title}</strong> is not shown right now. {missing.requires}
        </SettingDescription>}
        {featureSettings.map(({ id, category, label, mount, Section, visible, ownSections }) =>
          (!visible || visibility[visible]) && (mount === 'always' || active === category) && (
            <div key={id} hidden={active !== category}>
              {ownSections ? <Section target={target} /> : <SettingsSection title={label}><Section target={target} /></SettingsSection>}
            </div>
          ))}
      </div>
    </div>
  </div>;
}

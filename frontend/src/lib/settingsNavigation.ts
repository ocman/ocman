import type { SettingsGroupId } from './settingsCatalog';

export const SETTINGS_CATEGORIES = [
  { id: 'app', label: 'App & notifications' },
  { id: 'sessions', label: 'Sessions' },
  { id: 'agents', label: 'Agents & models' },
  { id: 'permissions', label: 'Permissions' },
  { id: 'connections', label: 'Connections & sharing' },
  { id: 'plugins', label: 'Plugins' },
  { id: 'system', label: 'System' },
] as const satisfies ReadonlyArray<{ id: SettingsGroupId; label: string }>;

/** Old bookmarks and links elsewhere in the app still select the right page. */
const LEGACY_CATEGORIES: Record<string, SettingsGroupId> = {
  notifications: 'app', behaviour: 'app', account: 'app',
  conversation: 'sessions', templates: 'sessions',
  platforms: 'agents', 'provider-accounts': 'agents',
  'auto-approve': 'permissions',
  remotes: 'connections', sharing: 'connections',
  webhooks: 'connections', 'link-previews': 'connections',
  maintenance: 'system', 'idle-projects': 'system',
};

export function settingsCategory(tab: string | null): SettingsGroupId {
  return SETTINGS_CATEGORIES.find((category) => category.id === tab)?.id
    ?? (tab && Object.hasOwn(LEGACY_CATEGORIES, tab) ? LEGACY_CATEGORIES[tab] : 'app');
}

export const settingsCategoryLabels = Object.fromEntries(
  SETTINGS_CATEGORIES.map(({ id, label }) => [id, label]),
);

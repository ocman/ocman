import { useState } from 'react';
import type { PickerTabsConfig } from '../PickerTabs';
import { PlatformLogo } from '../PlatformBadge';
import type { ModelEntry } from './modelEntries';

// An unprefixed reference is OpenCode's (lib/modelRef).
const platformOf = (entry: ModelEntry) => entry.platform || 'opencode';

/**
 * Platform tabs over model entries: "All" plus one tab per platform, shown
 * only when the entries span platforms. A tab whose platform disappears
 * falls back to All.
 */
export function useModelPlatformTabs(entries: ModelEntry[]): { tabs?: PickerTabsConfig; visibleEntries: ModelEntry[]; inTab: (entry: ModelEntry) => boolean } {
  const [platform, setPlatform] = useState('all');
  const names = new Map<string, string>();
  for (const entry of entries) {
    const id = platformOf(entry);
    if (!names.get(id)) names.set(id, entry.platformName);
  }
  const platforms = Array.from(names, ([value, label]) => ({ value, label: <><PlatformLogo platform={value} />{label || value}</> }));
  const showTabs = platforms.length > 1;
  const active = showTabs && platforms.some((tab) => tab.value === platform) ? platform : 'all';
  const inTab = (entry: ModelEntry) => active === 'all' || platformOf(entry) === active;
  return {
    tabs: showTabs ? { label: 'Model platforms', value: active, items: [{ value: 'all', label: 'All' }, ...platforms], onChange: setPlatform } : undefined,
    visibleEntries: active === 'all' ? entries : entries.filter(inTab),
    inTab,
  };
}

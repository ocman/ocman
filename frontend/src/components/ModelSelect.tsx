import { useMemo } from 'react';
import type { SessionModelEntry } from '../lib/api';
import { weightedSearch } from '../lib/weightedSearch';
import { splitModelRef } from '../lib/modelRef';
import { buildModelEntries, fromStrings, isPinnedModel, modelSearchKeys, modelSectionOf, type ModelEntry } from './assistant/modelEntries';
import { ModelBadges } from './assistant/ModelBadges';
import { useModelPlatformTabs } from './assistant/useModelPlatformTabs';
import { ModelLabel } from './ModelLogo';
import { PlatformLogo } from './PlatformBadge';
import { SearchSelect, type SearchSelectOption } from './SearchSelect';

const sectionOrder = ['Favorites', 'Recent', 'Recommended', 'All models', 'Disconnected providers'];

interface ModelSelectProps {
  value: string;
  onChange: (value: string) => void;
  models: string[];
  // Rich entries unlock the picker's sections, pinning, and badges.
  modelEntries?: SessionModelEntry[];
  ariaLabel?: string;
  defaultLabel?: string;
  disabled?: boolean;
  allowCustom?: boolean;
}

/**
 * Inline select with the ModelPicker modal's ranking: weighted fuzzy search,
 * pinned favorites/defaults, sections, badges, and platform tabs. An empty
 * value is the default.
 */
export function ModelSelect({ value, onChange, models, modelEntries, ariaLabel = 'Model', defaultLabel = 'Default model', disabled, allowCustom }: ModelSelectProps) {
  const { rich, multiPlatform, entries } = useMemo(() => {
    const built = buildModelEntries(models, modelEntries, value);
    // Group by section (stable) so a favorite anywhere in the server order gets one header.
    if (built.rich) built.entries.sort((a, b) => sectionOrder.indexOf(modelSectionOf(a)) - sectionOrder.indexOf(modelSectionOf(b)));
    // Keep a saved value selectable even when the catalog no longer lists it.
    if (!value || built.entries.some((e) => e.value === value)) return built;
    const [saved] = fromStrings([value], value);
    const savedPlatform = saved.platform || 'opencode';
    saved.platformName = built.entries.find((e) => (e.platform || 'opencode') === savedPlatform)?.platformName || saved.platformName;
    const all = [saved, ...built.entries];
    return { ...built, multiPlatform: new Set(all.map((e) => e.platform || 'opencode')).size > 1, entries: all };
  }, [models, modelEntries, value]);
  const { tabs, visibleEntries, inTab } = useModelPlatformTabs(entries);
  const byValue = new Map(entries.map((e) => [e.value, e]));

  const toOption = (e: ModelEntry): SearchSelectOption => ({
    value: e.value,
    label: e.value,
    section: rich ? modelSectionOf(e) : undefined,
    displayLabel: <>
      <ModelLabel model={e.value}>{splitModelRef(e.value).model}</ModelLabel><ModelBadges entry={e} rich={rich} />
      {multiPlatform && e.platformName && <span className="oc-model-picker-badge oc-model-picker-badge--platform" data-testid="model-platform-badge"><PlatformLogo platform={e.platform} />{e.platformName}</span>}
    </>,
  });
  const options = [{ value: '', label: defaultLabel }, ...entries.map(toOption)];

  return (
    <SearchSelect
      value={value}
      options={options}
      search={(query) => {
        const matches = weightedSearch(visibleEntries, query, modelSearchKeys, isPinnedModel).slice(0, 200).map(toOption);
        const id = query.trim();
        return allowCustom && id.length <= 300 && /^[^\s/]+\/\S+$/.test(id) && !entries.some((entry) => entry.value === id)
          ? [{ value: id, label: `Use ${id}` }, ...matches] : matches;
      }}
      tabs={tabs}
      inTab={(option) => {
        const entry = byValue.get(option.value);
        return !entry || inTab(entry);
      }}
      ariaLabel={ariaLabel}
      placeholder={defaultLabel}
      searchLabel="Search models"
      disabled={disabled}
      onChange={onChange}
    />
  );
}

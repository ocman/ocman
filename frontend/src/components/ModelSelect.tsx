import { useCallback, useMemo } from 'react';
import type { SessionModelEntry } from '../lib/api';
import { weightedSearch } from '../lib/weightedSearch';
import { buildModelEntries, fromStrings, isPinnedModel, modelSearchKeys, modelSectionOf, type ModelEntry } from './assistant/modelEntries';
import { ModelBadges } from './assistant/ModelBadges';
import { ModelLabel } from './ModelLogo';
import { SearchSelect, type SearchSelectOption } from './SearchSelect';

const sectionOrder = ['Favorites', 'Recent', 'Recommended', 'All models', 'Archived'];

interface ModelSelectProps {
  value: string;
  onChange: (value: string) => void;
  models: string[];
  // Rich entries unlock the picker's sections, pinning, and badges.
  modelEntries?: SessionModelEntry[];
  ariaLabel?: string;
  defaultLabel?: string;
  disabled?: boolean;
}

/**
 * Inline select with the ModelPicker modal's ranking: weighted fuzzy search,
 * pinned favorites/defaults, sections, and badges. An empty value is the default.
 */
export function ModelSelect({ value, onChange, models, modelEntries, ariaLabel = 'Model', defaultLabel = 'Default model', disabled }: ModelSelectProps) {
  const { rich, entries } = useMemo(() => {
    const built = buildModelEntries(models, modelEntries, value);
    // Group by section (stable) so a favorite anywhere in the server order gets one header.
    if (built.rich) built.entries.sort((a, b) => sectionOrder.indexOf(modelSectionOf(a)) - sectionOrder.indexOf(modelSectionOf(b)));
    // Keep a saved value selectable even when the catalog no longer lists it.
    return value && !built.entries.some((e) => e.value === value) ? { ...built, entries: [...fromStrings([value], value), ...built.entries] } : built;
  }, [models, modelEntries, value]);

  const toOption = useCallback((e: ModelEntry): SearchSelectOption => ({
    value: e.value,
    label: e.value,
    section: rich ? modelSectionOf(e) : undefined,
    displayLabel: <><ModelLabel model={e.value}>{e.value}</ModelLabel><ModelBadges entry={e} rich={rich} /></>,
  }), [rich]);
  const options = useMemo(() => [{ value: '', label: defaultLabel }, ...entries.map(toOption)], [defaultLabel, entries, toOption]);

  return (
    <SearchSelect
      value={value}
      options={options}
      search={(query) => weightedSearch(entries, query, modelSearchKeys, isPinnedModel).slice(0, 200).map(toOption)}
      ariaLabel={ariaLabel}
      placeholder={defaultLabel}
      searchLabel="Search models"
      disabled={disabled}
      onChange={onChange}
    />
  );
}

import { EmptyState } from './EmptyState';
import { searchSettings, type SettingId, type SettingsGroupId } from '../lib/settingsCatalog';

/** Results for a Settings search; picking one jumps to its row. */
export function SettingsSearchResults({
  query,
  groupLabels,
  onPick,
}: {
  query: string;
  groupLabels: Partial<Record<SettingsGroupId, string>>;
  onPick: (id: SettingId) => void;
}) {
  const results = searchSettings(query, groupLabels);
  if (results.length === 0) {
    return <EmptyState role="status">No settings match “{query.trim()}”.</EmptyState>;
  }
  return (
    <ul className="settings-search-results" aria-label="Matching settings">
      {results.map((r) => (
        <li key={r.id}>
          <button type="button" className="settings-search-result" onClick={() => onPick(r.id)}>
            <span className="settings-search-result-group">{groupLabels[r.group]}</span>
            <span className="settings-row-label">{r.title}</span>
            <span className="settings-row-desc">{r.description}</span>
          </button>
        </li>
      ))}
    </ul>
  );
}

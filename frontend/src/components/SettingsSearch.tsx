import { EmptyState } from './EmptyState';
import { searchSettings, type SettingId, type SettingsGroupId } from '../lib/settingsCatalog';
import styles from './SettingsSearch.module.css';

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
    <ul className={styles.results} aria-label="Matching settings">
      {results.map((r) => (
        <li key={r.id}>
          <button type="button" className={styles.result} onClick={() => onPick(r.id)}>
            <span className={styles.group}>{groupLabels[r.group]}</span>
            <span className={styles.title}>{r.title}</span>
            <span className={styles.description}>{r.description}</span>
            {r.requires && <span className={styles.description}>{r.requires}</span>}
          </button>
        </li>
      ))}
    </ul>
  );
}

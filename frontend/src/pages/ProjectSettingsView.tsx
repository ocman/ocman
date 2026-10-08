import { useEffect, useState } from 'react';
import { useParams } from 'react-router-dom';
import { api } from '../lib/api';
import { usePageTitle } from '../lib/headerContext';
import { shortPath } from '../lib/format';
import { useSettingSave } from '../lib/useSaveStatus';
import { SettingRow, SettingSelect, SettingToggle } from '../components/SettingRow';
import { SaveStatus } from '../components/SaveStatus';
import { InlineAlert } from '../components/InlineAlert';
import { EmptyState } from '../components/EmptyState';
import { LoadingState } from '../components/LoadingState';
import { Button, ButtonGroup } from '../components/Control';
import { IconButton } from '../components/IconButton';
import type { SearchSelectOption } from '../components/SearchSelect';
import styles from './ProjectSettingsView.module.css';

/** Model choices: the catalogue of any session in the project, else the
 *  models the project has historically used. */
async function loadModelOptions(dir: string): Promise<SearchSelectOption[]> {
  const [first] = await api.sessions({ dir, limit: 1 }).catch(() => []);
  if (first) {
    try {
      const r = await api.sessionModels(first.id, first.platform);
      if (r.models.length) {
        return r.models.map((m) => ({
          value: `${m.provider}/${m.model}`,
          label: `${m.providerName ?? m.provider} / ${m.modelName ?? m.model}`,
        }));
      }
    } catch { /* fall back to history */ }
  }
  const used = await api.models({ dir }).catch(() => []);
  return used.map((m) => ({ value: `${m.provider}/${m.model}`, label: `${m.provider}/${m.model}` }));
}

export function ProjectSettingsView() {
  const { dir } = useParams();
  const directory = dir ? decodeURIComponent(dir) : '';
  usePageTitle(directory ? `${shortPath(directory)} · Settings` : 'Project settings');

  const [models, setModels] = useState<string[]>([]);
  const [off, setOff] = useState(false);
  const [loaded, setLoaded] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [options, setOptions] = useState<SearchSelectOption[]>([]);
  const listSave = useSettingSave();
  const addSave = useSettingSave();
  const offSave = useSettingSave();
  const saving = listSave.state === 'saving' || addSave.state === 'saving' || offSave.state === 'saving';

  useEffect(() => {
    if (!directory) return;
    const ac = new AbortController();
    api.projectSettings(directory, ac.signal).then((ps) => {
      setModels(ps.models);
      setOff(ps.off);
      setLoaded(true);
    }).catch((err) => { if (!ac.signal.aborted) setError(String(err?.message ?? err)); });
    void loadModelOptions(directory).then((o) => { if (!ac.signal.aborted) setOptions(o); });
    return () => ac.abort();
  }, [directory]);

  // Optimistic write of the whole settings blob; revert on failure.
  const persist = async (nextModels: string[], nextOff: boolean) => {
    const prev = { models, off };
    setModels(nextModels);
    setOff(nextOff);
    setError(null);
    try {
      await api.setProjectSettings(directory, nextModels, nextOff);
    } catch (err) {
      setModels(prev.models);
      setOff(prev.off);
      setError(err instanceof Error ? err.message : String(err));
      throw err;
    }
  };

  const edit = (next: string[]) => {
    void listSave.track(() => persist(next, off)).catch(() => {});
  };
  const move = (i: number, delta: number) => {
    const next = [...models];
    [next[i], next[i + delta]] = [next[i + delta], next[i]];
    edit(next);
  };

  if (!loaded) {
    return <div className={styles.section}>{error ? <InlineAlert>{error}</InlineAlert> : <LoadingState>Loading project settings…</LoadingState>}</div>;
  }

  return (
    <div className={styles.section} data-testid="project-settings">
      {error && <InlineAlert>{error}</InlineAlert>}
      <SettingRow block setting="project-models">
        {models.length === 0 ? (
          <EmptyState data-testid="project-models-empty">
            No models configured. Sessions use OpenCode's own default model and never switch provider when one runs out of tokens.
          </EmptyState>
        ) : (
          <ol className={styles.models} aria-label="Project models">
            {models.map((m, i) => (
              <li key={m} className={styles.model}>
                <span className={styles.name}>{m}</span>
                {i === 0 && <small data-testid="project-default-badge">Project default</small>}
                <ButtonGroup label={`Actions for ${m}`}>
                  <IconButton label={`Move ${m} up`} icon="bi-arrow-up" disabled={saving || i === 0} onClick={() => move(i, -1)} />
                  <IconButton label={`Move ${m} down`} icon="bi-arrow-down" disabled={saving || i === models.length - 1} onClick={() => move(i, 1)} />
                  <Button type="button" size="small" variant="danger" aria-label={`Remove ${m}`} disabled={saving} onClick={() => edit(models.filter((x) => x !== m))}>
                    Remove
                  </Button>
                </ButtonGroup>
              </li>
            ))}
          </ol>
        )}
        {models.length > 0 && (
          <ButtonGroup label="Project model list actions">
            <Button type="button" size="small" variant="danger" disabled={saving} onClick={() => edit([])}>Clear list</Button>
            <SaveStatus state={listSave.state} />
          </ButtonGroup>
        )}
      </SettingRow>
      <SettingRow setting="project-add-model">
        <SettingSelect
          value=""
          options={options.filter((o) => !models.includes(o.value))}
          save={addSave}
          onSave={(next) => persist([...models, next], off)}
          ariaLabel="Add model"
          placeholder="Add a model…"
          searchLabel="Search models"
          disabled={saving || models.length >= 10}
        />
      </SettingRow>
      <SettingRow setting="project-disable-fallthrough">
        <SettingToggle
          testId="project-fallthrough-off"
          ariaLabel="Disable fallthrough"
          checked={off}
          disabled={saving || models.length === 0}
          save={offSave}
          onSave={(next) => persist(models, next)}
        />
      </SettingRow>
    </div>
  );
}

import { useEffect, useState } from 'react';
import { useParams } from 'react-router-dom';
import { api } from '../lib/api';
import { usePageTitle } from '../lib/headerContext';
import { shortPath } from '../lib/format';
import { useSettingSave } from '../lib/useSaveStatus';
import { SettingRow, SettingSelect, SettingToggle } from '../components/SettingRow';
import { SaveStatus } from '../components/SaveStatus';
import type { SearchSelectOption } from '../components/SearchSelect';
import './Dashboard.css';

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
    return <div className="settings-section">{error ? <div role="alert">{error}</div> : 'Loading…'}</div>;
  }

  return (
    <div className="settings-section" data-testid="project-settings">
      {error && <div className="oc-share-menu-error" role="alert">{error}</div>}
      <SettingRow
        block
        label="Models"
        desc="The first model is the project default, used when a prompt names no model. When a provider runs out of tokens, the session continues on the next model in the list."
      >
        {models.length === 0 ? (
          <div className="oc-share-menu-empty" data-testid="project-models-empty">
            No models configured. Sessions use OpenCode's own default model and never switch provider when one runs out of tokens.
          </div>
        ) : (
          <ol className="settings-prompt-sections" aria-label="Project models">
            {models.map((m, i) => (
              <li key={m} className="settings-prompt-section">
                <span className="mono">{m}</span>
                {i === 0 && <small data-testid="project-default-badge">Project default</small>}
                <div>
                  <button type="button" aria-label={`Move ${m} up`} disabled={i === 0} onClick={() => move(i, -1)}>
                    <i className="bi bi-arrow-up" aria-hidden="true" />
                  </button>
                  <button type="button" aria-label={`Move ${m} down`} disabled={i === models.length - 1} onClick={() => move(i, 1)}>
                    <i className="bi bi-arrow-down" aria-hidden="true" />
                  </button>
                  <button type="button" aria-label={`Remove ${m}`} onClick={() => edit(models.filter((x) => x !== m))}>
                    Remove
                  </button>
                </div>
              </li>
            ))}
          </ol>
        )}
        {models.length > 0 && (
          <div>
            <button type="button" onClick={() => edit([])}>Clear list</button>
            <SaveStatus state={listSave.state} />
          </div>
        )}
      </SettingRow>
      <SettingRow label="Add model" desc="Choices come from a session in this project, or the models it has used before.">
        <SettingSelect
          value=""
          options={options.filter((o) => !models.includes(o.value))}
          save={addSave}
          onSave={(next) => persist([...models, next], off)}
          ariaLabel="Add model"
          placeholder="Add a model…"
          searchLabel="Search models"
          disabled={models.length >= 10}
        />
      </SettingRow>
      <SettingRow
        label="Disable fallthrough"
        desc="Keep the list and its project default, but never switch to another model when a provider runs out of tokens."
      >
        <SettingToggle
          testId="project-fallthrough-off"
          ariaLabel="Disable fallthrough"
          checked={off}
          disabled={models.length === 0}
          save={offSave}
          onSave={(next) => persist(models, next)}
        />
      </SettingRow>
    </div>
  );
}

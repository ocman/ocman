import { useEffect, useRef, useState } from 'react';
import { useParams, useSearchParams } from 'react-router-dom';
import { api } from '../lib/api';
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
import { ProjectShell } from './ProjectShell';
import { ProjectQuickSettings } from '../components/ProjectQuickSettings';
import { DataTable } from '../components/DataTable';
import { clearSettingsCache, useSettingsRevision, type ProjectDefaults } from '../lib/projectSettingsCache';
import { PERMISSION_MODES } from '../lib/permissionModes';

export function ProjectSettingsView() {
  const { dir } = useParams();
  const directory = dir ? decodeURIComponent(dir) : '';
  const [params] = useSearchParams();
  const remoteId = params.get('remoteId') || 'local';
  return <ProjectShell view="settings"><ProjectSettingsContent key={JSON.stringify([directory, remoteId])} directory={directory} remoteId={remoteId} /></ProjectShell>;
}

function ProjectSettingsContent({ directory, remoteId }: { directory: string; remoteId: string }) {
  const revision = useSettingsRevision();
  const initialized = useRef(false);
  const [attempt, setAttempt] = useState(0);
  const [defaults, setDefaults] = useState<ProjectDefaults>();

  const [models, setModels] = useState<string[]>([]);
  const [off, setOff] = useState(false);
  const [loaded, setLoaded] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [options, setOptions] = useState<SearchSelectOption[]>([]);
  const [catalogError, setCatalogError] = useState<string | null>(null);
  const [catalogAttempt, setCatalogAttempt] = useState(0);
  const listSave = useSettingSave();
  const addSave = useSettingSave();
  const offSave = useSettingSave();
  const saving = listSave.state === 'saving' || addSave.state === 'saving' || offSave.state === 'saving';

  useEffect(() => {
    if (!directory) return;
    const ac = new AbortController();
    api.projectSettings(directory, ac.signal, remoteId).then((ps) => {
      if (ac.signal.aborted) return;
      if (!initialized.current) {
        setModels(ps.models);
        setOff(ps.off);
        initialized.current = true;
      }
      setDefaults(ps.defaults);
      setLoaded(true);
      setError(null);
    }).catch((err) => { if (!ac.signal.aborted) setError(String(err?.message ?? err)); });
    return () => ac.abort();
  }, [directory, remoteId, revision, attempt]);

  useEffect(() => {
    const controller = new AbortController();
    api.prepareSession({ directory, remoteId }, controller.signal).then((catalog) => {
      if (controller.signal.aborted) return;
      setOptions(catalog.models.models.map((model) => ({ value: `${model.provider}/${model.model}`, label: `${model.providerName ?? model.provider} / ${model.modelName ?? model.model}` })));
      setCatalogError(null);
    }).catch((err) => { if (!controller.signal.aborted) setCatalogError(err instanceof Error ? err.message : String(err)); });
    return () => controller.abort();
  }, [directory, remoteId, catalogAttempt]);

  // Optimistic write of the whole settings blob; revert on failure.
  const persist = async (nextModels: string[], nextOff: boolean) => {
    const prev = { models, off };
    setModels(nextModels);
    setOff(nextOff);
    setError(null);
    try {
      await api.setProjectSettings(directory, nextModels, nextOff, remoteId);
      clearSettingsCache();
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
    return <div className={styles.section}>{error ? <InlineAlert onRetry={() => setAttempt((value) => value + 1)}>{error}</InlineAlert> : <LoadingState>Loading project settings…</LoadingState>}</div>;
  }

  return (
    <div className={styles.section} data-testid="project-settings">
      {error && <InlineAlert onRetry={() => setAttempt((value) => value + 1)}>{error}</InlineAlert>}
      {catalogError && <InlineAlert onRetry={() => setCatalogAttempt((value) => value + 1)}>{catalogError}</InlineAlert>}
      <DataTable framed className={styles.table} aria-label="Project settings">
        <thead><tr><th scope="col">Setting</th><th scope="col">Value</th><th scope="col">Actions</th></tr></thead>
        <tbody>
          {[
            ['Default model', defaults?.model || 'Use inherited default'],
            ['Default agent', defaults?.agent || 'Use inherited default'],
            ['Default worktree behavior', defaults?.worktree === 'worktree' ? 'New worktree when available' : defaults?.worktree === 'current' ? 'Current checkout' : 'Use inherited default'],
            ['Default permission mode', PERMISSION_MODES.find((mode) => mode.id === defaults?.permissionMode && mode.id !== 'default')?.label || 'Use inherited default'],
          ].map(([name, value], index) => <tr key={name}><th scope="row">{name}</th><td className={styles.value}>{value}</td>{index === 0 && <td rowSpan={4}>
            <ProjectQuickSettings directory={directory} remoteId={remoteId} triggerLabel="Edit project defaults" disabled={saving}>Edit defaults</ProjectQuickSettings>
          </td>}</tr>)}
          <tr><th scope="row">Fallback models</th><td>
        {models.length === 0 ? (
          <EmptyState data-testid="project-models-empty">
            No fallback models configured. New conversations use the default model above or the inherited default, without automatic model switching.
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
        </td><td>{models.length > 0 && (
          <ButtonGroup label="Project model list actions">
            <Button type="button" size="small" variant="danger" disabled={saving} onClick={() => edit([])}>Clear list</Button>
            <SaveStatus state={listSave.state} />
          </ButtonGroup>
        )}</td></tr>
      <tr><th scope="row">Disable fallthrough</th><td>{off ? 'No automatic fallback' : 'Try the next model'}</td><td>
        <SettingToggle
          testId="project-fallthrough-off"
          ariaLabel="Disable fallthrough"
          checked={off}
          disabled={saving || models.length === 0}
          save={offSave}
          onSave={(next) => persist(models, next)}
        />
      </td></tr>
      </tbody></DataTable>
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
    </div>
  );
}

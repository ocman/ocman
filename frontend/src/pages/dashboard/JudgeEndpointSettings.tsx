import { useEffect, useState } from 'react';
import { api } from '../../lib/api';
import type { JudgeEndpointSettings as EndpointSettings } from '../../lib/api.settings';
import { useSettingSave } from '../../lib/useSaveStatus';
import { SettingRow } from '../../components/SettingRow';
import { Button, SelectField, TextField } from '../../components/Control';
import { SaveStatus } from '../../components/SaveStatus';
import styles from './JudgeEndpointSettings.module.css';

export function JudgeEndpointSettings() {
  const [config, setConfig] = useState<EndpointSettings | null>(null);
  const [apiKey, setApiKey] = useState<string | undefined>();
  const [error, setError] = useState('');
  const save = useSettingSave();

  const load = async (signal?: AbortSignal) => {
    try {
      const next = await api.getJudgeEndpoint(signal);
      if (!signal?.aborted) { setConfig(next); setError(''); }
    } catch (err) {
      if (!signal?.aborted) setError(err instanceof Error ? err.message : 'Could not load reviewer endpoint.');
    }
  };
  useEffect(() => {
    const controller = new AbortController();
    api.getJudgeEndpoint(controller.signal)
      .then((next) => { if (!controller.signal.aborted) setConfig(next); })
      .catch((err: unknown) => { if (!controller.signal.aborted) setError(err instanceof Error ? err.message : 'Could not load reviewer endpoint.'); });
    return () => controller.abort();
  }, []);

  const update = (patch: Partial<EndpointSettings>) => setConfig((current) => current ? { ...current, ...patch } : current);
  const persist = async () => {
    if (!config) return;
    setError('');
    try {
      const input = { format: config.format, endpoint: config.endpoint, model: config.model, minSafeProbability: config.minSafeProbability };
      const next = await save.track(() => api.setJudgeEndpoint({ ...input, ...(apiKey !== undefined ? { apiKey } : {}) }));
      setConfig(next);
      setApiKey(undefined);
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Could not save reviewer endpoint.');
    }
  };

  return (
    <SettingRow setting="reviewer-endpoint" block>
      {config ? (
        <form className={styles.form} onSubmit={(event) => { event.preventDefault(); void persist(); }}>
          <fieldset className={styles.fields} disabled={save.state === 'saving'}>
            <label>
              Reviewer API
              <SelectField value={config.format} onChange={(event) => update({ format: event.target.value as EndpointSettings['format'] })}>
                <option value="">OpenCode</option>
                <option value="openai">OpenAI-compatible chat completions</option>
                <option value="typesafe">TypeSafe-compatible System One</option>
              </SelectField>
            </label>
            {config.format !== '' && <>
              <label>
                Endpoint URL
                <TextField type="url" required value={config.endpoint} placeholder={config.format === 'openai' ? 'http://127.0.0.1:8080/v1/chat/completions' : 'https://api.typesafe.ai/v1/systemone'} onChange={(event) => update({ endpoint: event.target.value })} />
              </label>
              <label>
                Model ID {config.format === 'typesafe' && '(optional)'}
                <TextField required={config.format === 'openai'} value={config.model} onChange={(event) => update({ model: event.target.value })} />
              </label>
              <label>
                API key (optional)
                <TextField type="password" autoComplete="new-password" value={apiKey ?? ''} placeholder={config.apiKeySet ? 'Key stored; leave blank to keep it' : 'No API key stored'} onChange={(event) => setApiKey(event.target.value || undefined)} />
              </label>
              {config.apiKeySet && <Button type="button" variant="ghost" onClick={() => setApiKey('')}>Remove stored API key</Button>}
              {apiKey === '' && <p>API key will be removed when you save.</p>}
              {config.format === 'typesafe' && <>
                <label>
                Minimum safe probability
                <TextField type="number" min={0.5} max={1} step={0.001} required value={config.minSafeProbability} onChange={(event) => update({ minSafeProbability: Number(event.target.value) })} />
                </label>
                <p className={styles.description}>Only safe answers meeting this threshold are auto-approved. Validate the threshold on your workload; it is not an error-rate guarantee.</p>
              </>}
            </>}
            <div className={styles.actions}>
              <Button type="submit" variant="accent">Save reviewer endpoint</Button>
              <SaveStatus state={save.state} />
            </div>
          </fieldset>
        </form>
      ) : !error && <p role="status">Loading reviewer endpoint…</p>}
      {error && <div role="alert">{error}{!config && <Button type="button" onClick={() => { setError(''); void load(); }}>Retry</Button>}</div>}
    </SettingRow>
  );
}

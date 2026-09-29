import { useEffect, useState } from 'react';
import { api } from '../lib/api';
import type { WebhookRelaySettings as View } from '../lib/api.types';
import { useSettingSave } from '../lib/useSaveStatus';
import { Button } from './Control';
import { SettingRow, SettingText } from './SettingRow';

/** Relay host and enrollment token used when creating webhook inboxes. */
export function WebhookRelaySettings() {
  const [view, setView] = useState<View>();
  const [error, setError] = useState('');
  const relaySave = useSettingSave();
  const tokenSave = useSettingSave();
  // Remount the token field after each save so the typed secret leaves the DOM.
  const [tokenKey, setTokenKey] = useState(0);

  useEffect(() => {
    api.getWebhookRelay().then(setView, (err: unknown) => setError(err instanceof Error ? err.message : 'Could not load webhook settings'));
  }, []);

  const save = async (input: { relayUrl?: string; enrollmentToken?: string }) => {
    setError('');
    try { setView(await api.setWebhookRelay(input)); } catch (err) { setError(err instanceof Error ? err.message : 'Could not save'); throw err; }
  };

  if (!view) return error ? <p role="alert" className="routine-error">{error}</p> : null;
  return (
    <div data-testid="webhook-relay-settings">
      {error && <p role="alert" className="routine-error">{error}</p>}
      <SettingRow label="Webhook relay" desc={<>Relay that receives provider webhooks for new inboxes. Leave empty to use the share relay{view.defaultRelayUrl ? <> (<code>{view.defaultRelayUrl}</code>)</> : ''}. Existing inboxes keep their relay.</>}>
        <SettingText type="url" ariaLabel="Webhook relay" value={view.relayUrl} placeholder={view.defaultRelayUrl || 'https://relay.example.com'} save={relaySave} onSave={(relayUrl) => save({ relayUrl })} />
      </SettingRow>
      <SettingRow label="Enrollment token" desc="Authorizes inbox registration on the relay. Stored on this machine and never shown again.">
        <SettingText key={tokenKey} type="password" ariaLabel="Enrollment token" value="" placeholder={view.hasEnrollmentToken ? 'Saved; type to replace' : 'Not set'} save={tokenSave} onSave={(enrollmentToken) => (enrollmentToken ? save({ enrollmentToken }).then(() => setTokenKey((k) => k + 1)) : undefined)} />
        {view.hasEnrollmentToken && <Button type="button" size="small" variant="ghost" onClick={() => void tokenSave.track(() => save({ enrollmentToken: '' })).catch(() => {})}>Clear</Button>}
      </SettingRow>
    </div>
  );
}

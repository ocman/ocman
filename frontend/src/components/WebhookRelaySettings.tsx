import { useState } from 'react';
import { useQueryClient } from '@tanstack/react-query';
import { useWebhookRelaySettings } from '../lib/settingsQueries';
import { api } from '../lib/api';
import type { WebhookRelaySettings as View } from '../lib/api.types';
import { useSettingSave } from '../lib/useSaveStatus';
import { Button } from './Control';
import { SettingRow, SettingText } from './SettingRow';

/** Relay host and enrollment token used when creating webhook inboxes. */
export function WebhookRelaySettings() {
  const queryClient = useQueryClient();
  const relayQ = useWebhookRelaySettings();
  const view = relayQ.data;
  const [saveError, setError] = useState('');
  const error = saveError || (relayQ.error ? (relayQ.error instanceof Error ? relayQ.error.message : 'Could not load webhook settings') : '');
  const relaySave = useSettingSave();
  const tokenSave = useSettingSave();
  // Remount the token field after each save so the typed secret leaves the DOM.
  const [tokenKey, setTokenKey] = useState(0);

  const save = async (input: { relayUrl?: string; enrollmentToken?: string }) => {
    setError('');
    try { queryClient.setQueryData<View>(['webhook-relay'], await api.setWebhookRelay(input)); } catch (err) { setError(err instanceof Error ? err.message : 'Could not save'); throw err; }
  };

  if (!view) return error ? <p role="alert" className="routine-error">{error}</p> : null;
  return (
    <div data-testid="webhook-relay-settings">
      {error && <p role="alert" className="routine-error">{error}</p>}
      <SettingRow setting="webhook-relay" detail={view.defaultRelayUrl ? <>Share relay: <code>{view.defaultRelayUrl}</code></> : undefined}>
        <SettingText type="url" ariaLabel="Webhook relay" value={view.relayUrl} placeholder={view.defaultRelayUrl || 'https://relay.example.com'} save={relaySave} onSave={(relayUrl) => save({ relayUrl })} />
      </SettingRow>
      <SettingRow setting="webhook-enrollment-token">
        <SettingText key={tokenKey} type="password" ariaLabel="Enrollment token" value="" placeholder={view.hasEnrollmentToken ? 'Saved; type to replace' : 'Not set'} save={tokenSave} onSave={(enrollmentToken) => (enrollmentToken ? save({ enrollmentToken }).then(() => setTokenKey((k) => k + 1)) : undefined)} />
        {view.hasEnrollmentToken && <Button type="button" size="small" variant="ghost" onClick={() => void tokenSave.track(() => save({ enrollmentToken: '' })).catch(() => {})}>Clear</Button>}
      </SettingRow>
    </div>
  );
}

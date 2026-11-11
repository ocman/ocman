import { useEffect, useState } from 'react';
import { useQueryClient } from '@tanstack/react-query';
import { api } from '../lib/api';
import type { WebhookRelaySettings } from '../lib/api.types';
import { AnchorButton, Button, ButtonGroup } from './Control';

type Connection = { authorizationUrl: string; expiresAt: number };
type Props = { configured: boolean; connected: boolean; onConnected?: (settings: WebhookRelaySettings) => void };

/** The browser sees a sign-in link, never the relay's polling or enrollment credentials. */
export function WebhookRelayConnect({ configured, connected, onConnected }: Props) {
  const queryClient = useQueryClient();
  const [connection, setConnection] = useState<Connection>();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');

  useEffect(() => {
    if (!connection) return;
    let stopped = false;
    let timer: ReturnType<typeof setTimeout>;
    const poll = async () => {
      try {
        if (Date.now() >= connection.expiresAt) throw new Error('Connection expired. Connect again.');
        const result = await api.pollWebhookRelay();
        if (stopped) return;
        if (!result.pending && result.settings) {
          queryClient.setQueryData(['webhook-relay'], result.settings);
          onConnected?.(result.settings);
          setConnection(undefined);
        } else {
          timer = setTimeout(() => void poll(), 1500);
        }
      } catch (err) {
        if (!stopped) {
          setError(err instanceof Error ? err.message : 'Could not connect relay');
          setConnection(undefined);
        }
      }
    };
    timer = setTimeout(() => void poll(), 1500);
    return () => { stopped = true; clearTimeout(timer); };
  }, [connection, queryClient, onConnected]);

  const start = async () => {
    setBusy(true);
    setError('');
    try { setConnection(await api.connectWebhookRelay()); }
    catch (err) { setError(err instanceof Error ? err.message : 'Could not start relay sign-in'); }
    finally { setBusy(false); }
  };

  const cancel = async () => {
    setBusy(true);
    setError('');
    try { await api.cancelWebhookRelayConnect(); setConnection(undefined); }
    catch (err) { setError(err instanceof Error ? err.message : 'Could not cancel connection'); }
    finally { setBusy(false); }
  };

  return <div>
    {error && <p role="alert">{error}</p>}
    {connection ? <>
      <p role="status">Waiting for relay sign-in. Choose a login provider in the relay tab, then approve the connection.</p>
      <ButtonGroup label="Relay sign-in">
        <AnchorButton href={connection.authorizationUrl} target="_blank" rel="noopener noreferrer" variant="accent">Sign in to relay</AnchorButton>
        <Button type="button" disabled={busy} onClick={() => void cancel()}>Cancel connection</Button>
      </ButtonGroup>
    </> : <>
      <p>{connected ? 'Relay connected. New inboxes use the saved enrollment token.' : 'Connect to the relay to create webhook inboxes. Sign in using one of the providers offered by the relay.'}</p>
      <Button type="button" variant={connected ? 'ghost' : 'accent'} disabled={!configured || busy} onClick={() => void start()}>{busy ? 'Connecting…' : connected ? 'Reconnect relay' : 'Connect relay'}</Button>
      {!configured && <p>Set a relay URL first.</p>}
    </>}
  </div>;
}

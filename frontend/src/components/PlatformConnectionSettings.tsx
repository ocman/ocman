import { useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { useSearchParams } from 'react-router-dom';
import { apiFetch, fetchJSON, raiseForUnauthorized, readJSON, throwForStatus } from '../lib/api.requests';
import type { ConnectionCheck, ConnectionInput, ConnectionPlatform, PlatformConnection } from '../lib/platformConnections';
import { readConnectionSetup, writeConnectionSetup } from '../lib/connectionSetupReceipt';
import { PlatformConnections } from './PlatformConnections';
import { PlatformSettings } from './PlatformSettings';
import { LoadingState } from './LoadingState';
import { PageState } from './PageState';
import { useUiStore } from '../lib/uiStore';
import { queryClient } from '../lib/queryClient';

export type ConnectionInventory = {
  supported: boolean;
  onboardingComplete: boolean;
  connections: PlatformConnection[];
  platforms: ConnectionPlatform[];
};

export function PlatformConnectionSettings() {
  const [params] = useSearchParams();
  const owner = params.get('remoteId') ?? 'local';
  return <OwnerConnectionSettings key={owner} owner={owner} />;
}

export function OwnerConnectionSettings({ owner }: { owner: string }) {
  const openProjectPalette = useUiStore(state => state.openProjectPalette);
  const endpoint = `/api/platform-connections`;
  const url = (path = '') => `${endpoint}${path}?remoteId=${encodeURIComponent(owner)}`;
  const query = useQuery({ queryKey: ['platform-connections', owner], queryFn: () => fetchJSON<ConnectionInventory>(url()), retry: false }, queryClient);
  const inventory = query.data;
  const [actionError, setError] = useState('');
  const error = actionError || (query.error instanceof Error ? query.error.message : '');
  const [receipt, setReceipt] = useState(() => readConnectionSetup(owner));
  async function load() {
    setError('');
    await query.refetch();
    await queryClient.invalidateQueries({ queryKey: ['capabilities'] });
  }

  async function request(path: string, method: string, body?: unknown) {
    const response = await apiFetch(url(path), { method, ...(body === undefined ? {} : { headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body) }) });
    await raiseForUnauthorized(response);
    if (!response.ok) await throwForStatus(response);
    return response;
  }
  function remember(value?: ConnectionInput) {
    setReceipt(value);
    writeConnectionSetup(owner, value);
  }
  async function abandon() {
    if (receipt?.reservationId) await request(`/setup/${encodeURIComponent(receipt.reservationId)}`, 'DELETE');
    remember();
    await load();
  }
  async function check(input: ConnectionInput): Promise<ConnectionCheck> {
    const sameSetup = receipt && receipt.kind === input.kind && receipt.configuration === input.configuration && receipt.directory === input.directory;
    const value = sameSetup ? { ...input, reservationId: input.reservationId ?? receipt.reservationId } : input;
    const body = input.connectionId ? { id: input.connectionId } : value;
    if (receipt && !sameSetup && !input.connectionId) await abandon();
    const response = await apiFetch(url('/check'), { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body) });
    await raiseForUnauthorized(response);
    if (!response.ok) {
      if (response.headers.get('content-type')?.includes('application/json')) {
        const failure = await readJSON<{ reservationId?: string }>(response);
        if (value.reservationId && failure.reservationId && failure.reservationId !== value.reservationId) throw new Error('The owner returned a different setup reservation. Your original setup is kept.');
        if (failure.reservationId) remember({ ...input, reservationId: failure.reservationId });
        throw new Error('Could not check setup. Your reservation is kept; retry when the owner is ready.');
      }
      await throwForStatus(response);
    }
    const result = await readJSON<ConnectionCheck>(response);
    if (value.reservationId && result.reservationId !== value.reservationId) throw new Error('The owner returned a different setup reservation. Your original setup is kept.');
    if (result.reservationId) remember({ ...input, reservationId: result.reservationId });
    return result;
  }
  async function update(id: string, body: unknown) { await request(`/${encodeURIComponent(id)}`, 'PATCH', body); await load(); }
  async function save(input: ConnectionInput, id?: string) {
    if (id) {
      await request(`/${encodeURIComponent(id)}`, 'PATCH', { name: input.name });
      await request(`/${encodeURIComponent(id)}`, 'PATCH', { enabled: input.enabled });
    } else {
      const reservationId = input.reservationId ?? receipt?.reservationId;
      if (!reservationId) throw new Error('Check setup before saving.');
      await request('', 'POST', { reservationId, name: input.name, enabled: input.enabled });
      remember();
    }
    await load();
  }
  if (!inventory) return error ? <PageState compact error title="Platforms unavailable" description={error} onRetry={() => { void load(); }} /> : <LoadingState>Loading platform connections…</LoadingState>;
  if (!inventory.supported) return owner === 'local' ? <PlatformSettings /> : <PageState compact title="Connections unavailable" description="This owner does not support named platform connections." />;
  return <PlatformConnections connections={inventory.connections} platforms={inventory.platforms} machineName={owner === 'local' ? 'This machine' : owner}
    error={error} onboarding={!inventory.onboardingComplete} recoveredInput={receipt} onAbandon={abandon}
    onRetry={() => { void load(); }} onCheck={check} onSave={save}
    onEnabledChange={(id, enabled) => update(id, { enabled })} onDefault={id => update(id, { default: true })}
    onRemove={async id => { await request(`/${encodeURIComponent(id)}`, 'DELETE'); await load(); }}
    onSkip={() => { void request('/onboarding', 'POST').then(load).catch(err => setError(String(err))); }}
    onContinue={() => { void request('/onboarding', 'POST').then(async () => { await load(); openProjectPalette(); }).catch(err => setError(String(err))); }} />;
}

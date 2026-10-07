// @vitest-environment jsdom
import { beforeEach, expect, it, vi } from 'vitest';
import { renderHook, waitFor } from '@testing-library/react';
import { discardFirstSubmission, getFirstSubmission, reconcileFirstSubmission, startFirstSubmission, useFirstSubmission, useSessionFirstSubmission } from './firstSubmission';
import { claimDraftStart, persistDraftStart, readDraftStart } from '../../lib/draftStartClaims';
const stored = vi.hoisted(() => new Map<string, import('../../lib/draftStartClaims').DraftStart>());
vi.mock('../../lib/draftStartClaims', () => ({
  readDraftStart: vi.fn(async (id: string) => stored.get(id)),
  persistDraftStart: vi.fn(async (id: string, record: import('../../lib/draftStartClaims').DraftStart) => {
    const current = stored.get(id);
    if (current && (current.attemptId !== record.attemptId || current.deliveryState === 'done')) return current;
    stored.set(id, record);
    return record;
  }),
  claimDraftStart: vi.fn(async (id: string, record: import('../../lib/draftStartClaims').DraftStart) => {
    const current = stored.get(id);
    if (current && !current.error) return { claimed: false, start: current };
    stored.set(id, record);
    return { claimed: true, start: record };
  }),
}));

beforeEach(() => { localStorage.clear(); stored.clear(); useFirstSubmission.setState({ entries: {}, ready: {} }); });

it('never executes before the durable claim succeeds, retaining a deliberate retry after failure', async () => {
  vi.mocked(claimDraftStart).mockRejectedValueOnce(new Error('reservation failed'));
  const execute = vi.fn(async () => {});
  await startFirstSubmission('failed-reservation', 'payload', execute);
  expect(execute).not.toHaveBeenCalled();
  expect(getFirstSubmission('failed-reservation')).toMatchObject({ pending: false, error: expect.stringContaining('reservation failed') });
  await startFirstSubmission('failed-reservation', 'payload', execute);
  await waitFor(() => expect(execute).toHaveBeenCalledTimes(1));
});

it('retains a live failed outcome over a stale pending durable record and repairs it', async () => {
  vi.mocked(persistDraftStart).mockRejectedValueOnce(new Error('terminal write failed'));
  await startFirstSubmission('failed-terminal', 'payload', async () => { throw new Error('upload failed'); });
  await waitFor(() => expect(getFirstSubmission('failed-terminal')?.error).toBe('upload failed'));
  await reconcileFirstSubmission('failed-terminal');
  expect(stored.get('first-delivery:failed-terminal')).toMatchObject({ deliveryState: 'failed', error: 'upload failed' });
});

it('immediately retries a known failure without discarding its retained execution after a failed terminal write', async () => {
  const execute = vi.fn().mockRejectedValueOnce(new Error('delivery failed')).mockResolvedValueOnce(undefined);
  vi.mocked(persistDraftStart).mockRejectedValueOnce(new Error('terminal write failed'));
  await startFirstSubmission('immediate-retry', 'payload', execute);
  await waitFor(() => expect(getFirstSubmission('immediate-retry')?.error).toBe('delivery failed'));
  expect(stored.get('first-delivery:immediate-retry')?.deliveryState).toBe('pending');
  await startFirstSubmission('immediate-retry', 'payload', execute);
  await waitFor(() => expect(execute).toHaveBeenCalledTimes(2));
  await waitFor(() => expect(getFirstSubmission('immediate-retry')).toBeUndefined());
});

it('adopts a newer attempt returned when a stale explicit release is rejected', async () => {
  const old = { version: 0, text: 'old', attemptId: 'old-release', deliveryState: 'pending' as const };
  stored.set('first-delivery:stale-release', old);
  await reconcileFirstSubmission('stale-release');
  const newer = { ...old, attemptId: 'new-release', text: 'new' };
  vi.mocked(persistDraftStart).mockImplementationOnce(async (id) => { stored.set(id, newer); return newer; });
  await discardFirstSubmission('stale-release');
  expect(getFirstSubmission('stale-release')).toMatchObject({ pending: true, text: 'new' });
});

it('does not adopt an old failure when terminal repair returns a newer pending attempt', async () => {
  const old = { version: 0, text: 'old', attemptId: 'old-repair', deliveryState: 'pending' as const };
  stored.set('first-delivery:stale-repair', old);
  const newer = { ...old, attemptId: 'new-repair', text: 'new' };
  vi.mocked(persistDraftStart).mockImplementationOnce(async (id) => { stored.set(id, newer); return newer; });
  await reconcileFirstSubmission('stale-repair', { ...old, deliveryState: 'failed', error: 'old failure' });
  expect(getFirstSubmission('stale-repair')).toMatchObject({ pending: true, text: 'new' });
});

it('does not let a delayed old read replace a newly claimed live attempt', async () => {
  const old = { version: 0, text: 'old', attemptId: 'delayed-old', deliveryState: 'failed' as const, error: 'old failure' };
  stored.set('first-delivery:delayed-read', old);
  let finishRead!: (record: typeof old) => void;
  vi.mocked(readDraftStart).mockImplementationOnce(() => new Promise((resolve) => { finishRead = resolve; }));
  const reconciliation = reconcileFirstSubmission('delayed-read');
  let finishDelivery!: () => void;
  const delivery = new Promise<void>((resolve) => { finishDelivery = resolve; });
  await startFirstSubmission('delayed-read', 'new', () => delivery);
  finishRead(old);
  await reconciliation;
  expect(getFirstSubmission('delayed-read')).toMatchObject({ pending: true, text: 'new' });
  finishDelivery();
  await waitFor(() => expect(getFirstSubmission('delayed-read')).toBeUndefined());
});

it('keeps an orphaned pending delivery blocked until explicit release without replay', async () => {
  stored.set('first-delivery:orphan', { version: 0, text: 'unknown payload', attemptId: 'orphan', deliveryOwner: 'closed-tab', deliveryState: 'pending' });
  await reconcileFirstSubmission('orphan');
  await waitFor(() => expect(getFirstSubmission('orphan')?.error).toContain('outcome is unknown'), { timeout: 2500 });
  expect(getFirstSubmission('orphan')?.pending).toBe(true);
  await reconcileFirstSubmission('orphan');
  expect(getFirstSubmission('orphan')?.error).toContain('outcome is unknown');
  await discardFirstSubmission('orphan');
  expect(getFirstSubmission('orphan')).toBeUndefined();
  expect(stored.get('first-delivery:orphan')?.deliveryState).toBe('done');
});

it('does not downgrade a completed delivery from a stale failure hint', async () => {
  const done = { version: 0, text: '', attemptId: 'completed', deliveryState: 'done' as const };
  stored.set('first-delivery:completed', done);
  await reconcileFirstSubmission('completed', { ...done, deliveryState: 'failed', error: 'old failure' });
  expect(getFirstSubmission('completed')).toBeUndefined();
  expect(stored.get('first-delivery:completed')).toEqual(done);
});

it('repairs an authoritative pending record from a same-attempt terminal hint', async () => {
  const pending = { version: 0, text: 'payload', attemptId: 'hint-attempt', deliveryState: 'pending' as const };
  stored.set('first-delivery:hint', pending);
  await reconcileFirstSubmission('hint', { ...pending, deliveryState: 'failed', error: 'known failure' });
  expect(stored.get('first-delivery:hint')).toMatchObject({ deliveryState: 'failed', error: 'known failure' });
  expect(getFirstSubmission('hint')).toMatchObject({ pending: false, error: 'known failure' });
});

it('repairs a saved terminal mirror after reload without waiting for a new notification', async () => {
  const pending = { version: 0, text: 'payload', attemptId: 'reload-terminal', deliveryState: 'pending' as const };
  stored.set('first-delivery:reload-terminal', pending);
  localStorage.setItem('ocman.firstSubmission.v1:reload-terminal', JSON.stringify({ ...pending, deliveryState: 'done', text: '' }));
  await reconcileFirstSubmission('reload-terminal');
  expect(getFirstSubmission('reload-terminal')).toBeUndefined();
  expect(stored.get('first-delivery:reload-terminal')?.deliveryState).toBe('done');
});

it('converges two tabs on equivalent cloned failure records without rewriting or rebroadcasting', async () => {
  await startFirstSubmission('converged-failure', 'payload', async () => { throw new Error('known failure'); });
  await waitFor(() => expect(stored.get('first-delivery:converged-failure')?.deliveryState).toBe('failed'));
  vi.mocked(readDraftStart).mockImplementation(async (id) => { const record = stored.get(id); return record && structuredClone(record); });
  vi.resetModules();
  const peer = await import('./firstSubmission');
  const writes = vi.mocked(persistDraftStart).mock.calls.length;
  await peer.reconcileFirstSubmission('converged-failure');
  await reconcileFirstSubmission('converged-failure');
  await peer.reconcileFirstSubmission('converged-failure');
  expect(getFirstSubmission('converged-failure')?.error).toBe('known failure');
  expect(peer.getFirstSubmission('converged-failure')?.error).toBe('known failure');
  expect(vi.mocked(persistDraftStart).mock.calls.length).toBe(writes);
});

it('fails closed on a hydration error and recovers after a successful read', async () => {
  vi.mocked(readDraftStart).mockRejectedValueOnce(new Error('record unavailable'));
  const { result } = renderHook(() => useSessionFirstSubmission('read-error'));
  await waitFor(() => expect(result.current).toMatchObject({ pending: true, error: 'record unavailable' }));
  await reconcileFirstSubmission('read-error');
  await waitFor(() => expect(result.current).toBeUndefined());
  await discardFirstSubmission('no-record');
  expect(getFirstSubmission('no-record')).toBeUndefined();
});

it('keeps a live owner pending when an independent tab probes its execution', async () => {
  let finish!: () => void;
  const delivery = new Promise<void>((resolve) => { finish = resolve; });
  await startFirstSubmission('live-owner', 'payload', () => delivery);
  const observer = new BroadcastChannel('ocman.first-delivery');
  const alive = new Promise<void>((resolve) => { observer.onmessage = (event) => { if (event.data.id === 'live-owner' && event.data.alive) resolve(); }; });
  vi.resetModules();
  const peer = await import('./firstSubmission');
  await peer.reconcileFirstSubmission('live-owner');
  await alive;
  await new Promise((resolve) => setTimeout(resolve, 1600));
  expect(peer.getFirstSubmission('live-owner')).toMatchObject({ pending: true });
  expect(peer.getFirstSubmission('live-owner')?.error).toBeUndefined();
  finish();
  await waitFor(() => expect(peer.getFirstSubmission('live-owner')).toBeUndefined());
  observer.close();
});

it('does not let a failed terminal mirror removal override live completion', async () => {
  const execute = vi.fn(async () => {});
  const remove = vi.spyOn(Storage.prototype, 'removeItem').mockImplementation(() => { throw new Error('storage blocked'); });
  try {
    await startFirstSubmission('terminal-removal', 'first', execute);
    await waitFor(() => expect(execute).toHaveBeenCalled());
    await waitFor(() => expect(useFirstSubmission.getState().entries['terminal-removal']).toBeUndefined());
    expect(getFirstSubmission('terminal-removal')?.pending).not.toBe(true);
  } finally { remove.mockRestore(); }
});

it('shares the first-delivery lock with an independent tab before completion navigation', async () => {
  let finish!: () => void;
  const delivery = new Promise<void>((resolve) => { finish = resolve; });
  await startFirstSubmission('peer-child', 'first message', () => delivery);
  vi.resetModules();
  const peer = await import('./firstSubmission');
  await peer.reconcileFirstSubmission('peer-child');
  expect(peer.useFirstSubmission.getState().entries['peer-child']?.pending).toBe(true);
  finish();
  await waitFor(() => expect(useFirstSubmission.getState().entries['peer-child']).toBeUndefined());
  window.dispatchEvent(new StorageEvent('storage', { key: 'ocman.firstSubmission.v1:peer-child', newValue: null }));
  await waitFor(() => expect(peer.useFirstSubmission.getState().entries['peer-child']).toBeUndefined());
});

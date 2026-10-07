import { useEffect } from 'react';
import { create } from 'zustand';
import { claimDraftStart, persistDraftStart, readDraftStart, type DraftStart } from '../../lib/draftStartClaims';
import { randomId } from '../../lib/randomId';

interface Submission {
  text: string;
  pending: boolean;
  error?: string;
  execute?: () => Promise<void>;
}

const PREFIX = 'ocman.firstSubmission.v1:';
const key = (id: string) => `first-delivery:${id}`;
const owner = randomId();
const records = new Map<string, DraftStart>();
const executions = new Map<string, () => Promise<void>>();
const probes = new Map<string, ReturnType<typeof setTimeout>>();
const channel = typeof BroadcastChannel === 'undefined' ? undefined : new BroadcastChannel('ocman.first-delivery');
const outcomeKey = (record?: DraftStart) => JSON.stringify([record?.attemptId, record?.deliveryOwner, record?.deliveryState, record?.text, record?.error, record?.version]);

// One lifecycle owns execution, persistence and recovery. Mirrors only notify;
// IndexedDB reserves a delivery before any upload/command or completion publication.
export const useFirstSubmission = create<{ entries: Record<string, Submission>; ready: Record<string, boolean> }>(() => ({ entries: {}, ready: {} }));
export const getFirstSubmission = (id: string) => useFirstSubmission.getState().entries[id];

function adopt(id: string, record: DraftStart | undefined) {
  if (record) records.set(id, record);
  useFirstSubmission.setState(({ entries, ready }) => {
    const next = { ...entries };
    if (!record || record.deliveryState === 'done') delete next[id];
    else next[id] = { text: record.text, pending: record.deliveryState !== 'failed',
      error: record.error, execute: executions.get(record.attemptId || '') };
    return { entries: next, ready: { ...ready, [id]: true } };
  });
}

function notify(id: string, record: DraftStart) {
  try { localStorage.setItem(PREFIX + id, JSON.stringify(record)); } catch { /* Hints are optional; the durable record already exists. */ }
  channel?.postMessage({ id, record });
}

async function persistOutcome(id: string, record: DraftStart) {
  const before = records.get(id);
  const persisted = await persistDraftStart(key(id), record);
  if (records.get(id) !== before) return;
  adopt(id, persisted);
  if (persisted) notify(id, persisted);
  return persisted;
}

async function settle(id: string, record: DraftStart) {
  adopt(id, record);
  try { await persistOutcome(id, record); }
  catch { if (records.get(id) === record) notify(id, record); }
}

function probeOwner(id: string, record: DraftStart) {
  if (executions.has(record.attemptId || '') || probes.has(id)) return;
  // Lack of an owner response means an unknown outcome, never permission to replay.
  const timer = setTimeout(() => {
    probes.delete(id);
    if (records.get(id)?.attemptId === record.attemptId && records.get(id)?.deliveryState === 'pending') {
      adopt(id, { ...record, deliveryState: 'interrupted', error: 'The originating tab is unavailable. First-delivery outcome is unknown.' });
    }
  }, 1500);
  probes.set(id, timer);
  channel?.postMessage({ id, probe: record.deliveryOwner, attemptId: record.attemptId });
}

export async function reconcileFirstSubmission(id: string, hint?: DraftStart) {
  const before = records.get(id);
  if (!hint) {
    try { hint = JSON.parse(localStorage.getItem(PREFIX + id) || 'null') || undefined; } catch { /* The durable record remains readable. */ }
  }
  const stored = await readDraftStart(key(id));
  if (records.get(id) !== before) return;
  let live = records.get(id);
  if (stored?.deliveryState === 'done') { adopt(id, stored); return; }
  if (live?.deliveryState !== 'done' && hint?.attemptId === stored?.attemptId && hint?.deliveryState &&
    ['failed', 'done'].includes(hint.deliveryState)) live = hint;
  const terminal = live?.deliveryState && live.deliveryState !== 'pending' && live.deliveryState !== 'interrupted';
  const interrupted = live?.deliveryState === 'interrupted' && live.attemptId === stored?.attemptId && stored?.deliveryState === 'pending';
  const record = (terminal && (!stored || live?.attemptId === stored.attemptId)) || interrupted ? live : stored;
  if (record && terminal && outcomeKey(record) !== outcomeKey(stored)) {
    const actual = await persistOutcome(id, record);
    if (actual?.deliveryState === 'pending') probeOwner(id, actual);
    return;
  }
  adopt(id, record);
  if (record?.deliveryState === 'pending' || record?.deliveryState === 'interrupted') probeOwner(id, record);
}

export function useSessionFirstSubmission(id: string) {
  const entry = useFirstSubmission((state) => state.entries[id]);
  const ready = useFirstSubmission((state) => state.ready[id]);
  useEffect(() => {
    const reconcile = () => void reconcileFirstSubmission(id).catch((error: unknown) => {
      useFirstSubmission.setState(({ entries }) => ({ entries: { ...entries, [id]: {
        text: '', pending: true, error: error instanceof Error ? error.message : String(error),
      } } }));
    });
    reconcile();
    const timer = setInterval(() => { if (getFirstSubmission(id)?.pending) reconcile(); }, 5000);
    return () => clearInterval(timer);
  }, [id]);
  return entry || (ready ? undefined : { text: '', pending: true });
}

export async function startFirstSubmission(id: string, text: string, execute: () => Promise<void>) {
  if (getFirstSubmission(id)?.pending) return;
  if (records.get(id)?.deliveryState === 'failed') {
    try { await reconcileFirstSubmission(id); }
    catch { return; } // Preserve the original execution until its known failure is durable.
    if (getFirstSubmission(id)?.pending || records.get(id)?.deliveryState === 'done') return;
  }
  const previousAttempt = records.get(id)?.attemptId;
  const next: DraftStart = { version: 0, text, attemptId: randomId(), deliveryOwner: owner, deliveryState: 'pending' };
  executions.set(next.attemptId!, execute);
  adopt(id, next);
  try {
    const result = await claimDraftStart(key(id), next);
    if (!result.claimed) { executions.delete(next.attemptId!); adopt(id, result.start); return; }
    if (previousAttempt) executions.delete(previousAttempt);
    adopt(id, next);
    notify(id, next);
  } catch (error) {
    // Fail closed: no execution starts without a durable reservation. Retry keeps the exact payload.
    adopt(id, { ...next, deliveryState: 'failed', error: `Could not reserve first delivery: ${error instanceof Error ? error.message : String(error)}` });
    return;
  }
  void (async () => {
    try {
      await execute();
      await settle(id, { ...next, deliveryState: 'done', text: '' });
      executions.delete(next.attemptId!);
    } catch (error) {
      await settle(id, { ...next, deliveryState: 'failed', error: error instanceof Error ? error.message : String(error) });
    }
  })();
}

/** Explicitly release an uncertain delivery; never reconstruct or automatically resend its payload. */
export async function discardFirstSubmission(id: string) {
  const before = records.get(id);
  const record = before || await readDraftStart(key(id));
  if (records.get(id) !== before) return;
  if (!record) { await reconcileFirstSubmission(id); return; }
  const actual = await persistOutcome(id, { ...record, deliveryState: 'done', text: '' });
  if (actual?.deliveryState === 'done') executions.delete(actual.attemptId || '');
}

if (channel) channel.onmessage = (event: MessageEvent) => {
  const { id, record, probe, alive, attemptId } = event.data || {};
  if (typeof id !== 'string') return;
  if (probe === owner && attemptId === records.get(id)?.attemptId && executions.has(attemptId)) { channel.postMessage({ id, alive: owner, attemptId }); return; }
  if (alive && alive === records.get(id)?.deliveryOwner && attemptId === records.get(id)?.attemptId) {
    clearTimeout(probes.get(id));
    probes.delete(id);
    const current = records.get(id)!;
    if (current.deliveryState === 'interrupted') adopt(id, { ...current, deliveryState: 'pending', error: undefined });
    return;
  }
  if (record && typeof record.text === 'string' && typeof record.version === 'number' && typeof record.attemptId === 'string') {
    void reconcileFirstSubmission(id, record).catch(() => undefined);
  }
};

if (typeof window !== 'undefined') window.addEventListener('storage', (event) => {
  if (!event.key?.startsWith(PREFIX)) return;
  const id = event.key.slice(PREFIX.length);
  let hint: DraftStart | undefined;
  try { hint = JSON.parse(event.newValue || 'null') || undefined; } catch { /* Read the authoritative record. */ }
  void reconcileFirstSubmission(id, hint).catch(() => undefined);
});

import { api } from './api';
import type { WebhookDelivery, WebhookInbox } from './api.types';

export type Op = 'equals' | 'oneOf' | 'exists' | 'missing';
export type FilterRow = { key: string; op: Op; value: string };
type Predicate = { exists?: boolean; equals?: unknown; oneOf?: unknown[]; op?: string; value?: unknown };

// Every value is stored as a JSON string: the matcher compares fmt.Sprint of
// both sides, so "5" still matches a numeric 5 and "true" a boolean.
export function encodeFilters(rows: FilterRow[]): string {
  const out: Record<string, Predicate> = {};
  for (const { key, op, value } of rows) {
    if (!key.trim()) continue;
    if (op === 'exists' || op === 'missing') out[key.trim()] = { exists: op === 'exists' };
    else if (op === 'equals') out[key.trim()] = { equals: value };
    else out[key.trim()] = { oneOf: value.split(',').map((v) => v.trim()).filter(Boolean) };
  }
  return JSON.stringify(out);
}

export function decodeFilters(json: string): FilterRow[] {
  let parsed: Record<string, Predicate>;
  try { parsed = JSON.parse(json || '{}') ?? {}; } catch { return []; }
  return Object.entries(parsed).map(([key, p]) => {
    // Check presence, not length: an empty list matches nothing, and reading
    // it back as 'exists' would widen the filter on the next save.
    if (Array.isArray(p.oneOf)) return { key, op: 'oneOf', value: p.oneOf.map(String).join(', ') };
    const equals = p.op === 'equals' ? p.value : p.equals;
    if (equals !== undefined) return { key, op: 'equals', value: String(equals) };
    return { key, op: p.exists === false ? 'missing' : 'exists', value: '' };
  });
}

const OUTCOME: Record<string, string> = { terminal: 'ran', ignored: 'no match', failure: 'failed', queued: 'queued' };
const EVENT_HEADERS = ['x-forgejo-event', 'x-gitea-event', 'x-github-event', 'x-gitlab-event'];

// A short "pull_request · opened" hint so a list of deliveries is scannable.
export function deliveryHint(delivery: WebhookDelivery): string {
  let headers: Record<string, string[]> = {};
  try { headers = JSON.parse(delivery.headers || '{}') ?? {}; } catch { /* keep empty */ }
  const lower = Object.fromEntries(Object.entries(headers).map(([k, v]) => [k.toLowerCase(), v]));
  const event = EVENT_HEADERS.map((h) => lower[h]?.[0]).find(Boolean);
  let action: unknown;
  try { action = (JSON.parse(delivery.body) as { action?: unknown })?.action; } catch { /* not JSON */ }
  return [event, typeof action === 'string' ? action : ''].filter(Boolean).join(' · ');
}

export type Outcome = { label: string; tone: string; href?: string };

// One badge per routine the delivery was offered to. With no subscriber
// matched, the dispatcher records a single routine-less "ignored" row.
export function outcomes(delivery: WebhookDelivery, routineName: (id: string) => string): Outcome[] {
  if (delivery.dispatches.length === 0) return [{ label: delivery.lastError ? 'error' : 'received', tone: delivery.lastError ? 'failure' : '' }];
  return delivery.dispatches.map((d) => ({
    label: d.routineId ? `${d.state === 'ignored' && d.error ? 'skipped' : OUTCOME[d.state] ?? d.state}: ${routineName(d.routineId)}` : 'no routine matched',
    tone: d.state === 'terminal' ? 'success' : d.state === 'failure' ? 'failure' : '',
    ...(d.sessionId ? { href: `/session/${encodeURIComponent(d.sessionId)}?platform=${encodeURIComponent(d.platform)}` } : {}),
  }));
}

export type Trigger = { inboxId: string; headers: FilterRow[]; fields: FilterRow[] };

export function triggerFor(routineId: string | undefined, inboxes: WebhookInbox[]): Trigger {
  for (const inbox of inboxes) {
    const sub = inbox.subscriptions.find((item) => item.routineId === routineId);
    if (routineId && sub) return { inboxId: inbox.id, headers: decodeFilters(sub.headerPredicates), fields: decodeFilters(sub.jsonPredicates) };
  }
  return { inboxId: '', headers: [], fields: [] };
}

// saveTrigger makes the routine subscribe to exactly the chosen inbox.
export async function saveTrigger(routineId: string, trigger: Trigger, inboxes: WebhookInbox[]) {
  for (const inbox of inboxes) {
    if (inbox.id !== trigger.inboxId && inbox.subscriptions.some((sub) => sub.routineId === routineId)) await api.webhookInboxes.unsubscribe(inbox.id, routineId);
  }
  if (trigger.inboxId) await api.webhookInboxes.subscribe(trigger.inboxId, { routineId, headerPredicates: encodeFilters(trigger.headers), jsonPredicates: encodeFilters(trigger.fields) });
}

// triggerLabel summarises what starts a routine: its schedule, its webhook
// inbox, both, or only a manual run.
export function triggerLabel(routine: { id: string; scheduleKind: string }, inboxes: WebhookInbox[]): string {
  const inbox = inboxes.find((item) => item.subscriptions.some((sub) => sub.routineId === routine.id));
  const parts = [routine.scheduleKind === 'none' ? '' : routine.scheduleKind, inbox ? `webhook: ${inbox.name || inbox.id}` : ''].filter(Boolean);
  return parts.join(' + ') || 'manual';
}

const OP_TEXT: Record<Op, string> = { equals: '=', oneOf: 'in', exists: 'exists', missing: 'is missing' };

// describeFilters renders a subscription's conditions in one short line.
export function describeFilters(headerPredicates: string, jsonPredicates: string): string {
  const rows = [...decodeFilters(headerPredicates), ...decodeFilters(jsonPredicates)];
  if (rows.length === 0) return 'every delivery';
  return rows.map((row) => (row.op === 'exists' || row.op === 'missing' ? `${row.key} ${OP_TEXT[row.op]}` : `${row.key} ${OP_TEXT[row.op]} ${row.value}`)).join(' and ');
}

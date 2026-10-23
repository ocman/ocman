// Message and part merging for the SessionDetail reducer, including the
// delta field-ownership rule described in sessionReducer.ts.

import type { Message, Part } from './api';
import { DELTA_FIELD_PATH } from './conversation/events';
import { findIndexById, replaceAt } from './conversation/partIndex';

/**
 * Streaming fields are appendable string values reachable by a
 * dotted path on `part.data`: exactly the paths a delta can target.
 * Everything else is snapshot-owned by definition.
 */
export const STREAMING_FIELDS: ReadonlySet<string> = new Set(Object.values(DELTA_FIELD_PATH));

/**
 * Return `snapshot` with every streaming field in `fields` taken from
 * `local` when the local string is longer. Streaming fields are
 * append-only, so the shorter value is always a prefix of the longer.
 */
export function keepLongerStreamedFields(
  snapshot: Record<string, unknown>,
  local: Record<string, unknown>,
  fields: Iterable<string> = STREAMING_FIELDS,
): Record<string, unknown> {
  let merged = snapshot;
  for (const field of fields) {
    const mine = getFieldByPath(local, field);
    if (typeof mine !== 'string') continue;
    const theirs = getFieldByPath(merged, field);
    if (typeof theirs !== 'string' || mine.length > theirs.length) merged = setFieldByPath(merged, field, mine);
  }
  return merged;
}

/**
 * Derive a `_deltaOwnedFields` map from a list of parts by marking
 * every non-empty streaming field as delta-owned. Used by the host
 * hook when seeding the reducer from the per-session cache: the
 * cached parts already carry SSE-accumulated text, but the cache
 * format doesn't persist the ownership map. Without re-seeding it,
 * a reconcile-mode load against a DB-lagging server response would
 * wipe streamed chunks that the server hadn't recorded yet.
 *
 * The rule is conservative — any streaming field with a non-empty
 * string is treated as delta-built. Snapshot-only parts (e.g.
 * step-finish blocks) have no streaming fields and won't be marked. Combined with the longest-wins rule in `upsertSnapshotPart`,
 * this guarantees that streamed text only ever grows, never shrinks.
 */
export function seedDeltaOwnedFields(parts: Part[]): Map<string, Set<string>> {

  const map = new Map<string, Set<string>>();
  for (const part of parts) {
    const data = decode(part);
    let owned: Set<string> | null = null;
    for (const field of STREAMING_FIELDS) {
      const value = getFieldByPath(data, field);
      if (typeof value === 'string' && value.length > 0) {
        if (!owned) owned = new Set<string>();
        owned.add(field);
      }
    }
    if (owned) map.set(part.id, owned);
  }
  return map;
}

/**
 * Decode `part.data` to a plain object. Mirrors the helper in `parsePart`.
 */
export function decode(part: Part): Record<string, unknown> {
  const raw = part.data as unknown;
  if (typeof raw === 'string') {
    try {
      return JSON.parse(raw) as Record<string, unknown>;
    } catch {
      return {};
    }
  }
  if (raw && typeof raw === 'object') return raw as Record<string, unknown>;
  return {};
}

export function encode(part: Part, data: Record<string, unknown>): Part {
  return { ...part, data: data as unknown as string };
}

export function getFieldByPath(obj: Record<string, unknown>, path: string): unknown {
  if (path.indexOf('.') < 0) return obj[path];
  const segs = path.split('.');
  let cur: unknown = obj;
  for (const seg of segs) {
    if (!cur || typeof cur !== 'object') return undefined;
    cur = (cur as Record<string, unknown>)[seg];
  }
  return cur;
}

export function setFieldByPath(
  obj: Record<string, unknown>,
  path: string,
  value: unknown,
): Record<string, unknown> {
  const dotIdx = path.indexOf('.');
  if (dotIdx < 0) return { ...obj, [path]: value };
  const parent = path.slice(0, dotIdx);
  const child = path.slice(dotIdx + 1);
  const parentObj = (obj[parent] && typeof obj[parent] === 'object')
    ? obj[parent] as Record<string, unknown>
    : {};
  return { ...obj, [parent]: { ...parentObj, [child]: value } };
}

/** Sorted insert by `timeCreated` ascending, replacing on id match. */
export function upsertMessage(prev: Message[], msg: Message): Message[] {
  const idx = findIndexById(prev, msg.id);
  if (idx >= 0) return replaceAt(prev, idx, msg);
  const insertAt = prev.findIndex((m) => m.timeCreated > msg.timeCreated);
  if (insertAt < 0) return [...prev, msg];
  return [...prev.slice(0, insertAt), msg, ...prev.slice(insertAt)];
}

/**
 * Apply a snapshot part to the parts list. Streaming fields that
 * have already received deltas use "longest string wins": the
 * accumulated text or output is append-only on the wire, so whichever
 * side has more characters is the more-complete value. Non-streaming
 * fields always take the snapshot.
 *
 * Why longest-wins (and not "local always wins"): the local value is
 * authoritative when SSE has streamed past the DB (the common case
 * — DB lags SSE by hundreds of ms). But the inverse can happen too:
 *   1. The cache mirror restored a part on session revisit, seeding
 *      its delta-owned fields from the cached text. If the user was
 *      away long enough that the server DB has caught up *past* the
 *      cached text, the snapshot is the more-complete value.
 *   2. A mid-stream reconcile fetch (e.g. `session.diff`) can return
 *      a snapshot that leads the SSE stream by a delta or two.
 * In both cases, taking the longer string is strictly safe because
 * streaming fields are append-only — the shorter string is always a
 * prefix of the longer one.
 */
export function upsertSnapshotPart(
  parts: Part[],
  snapshot: Part,
  ownedFields: Map<string, Set<string>>,
): Part[] {
  const idx = parts.findIndex((p) => p.id === snapshot.id);
  if (idx < 0) {
    return [...parts, snapshot];
  }
  const owned = ownedFields.get(snapshot.id);
  if (!owned || owned.size === 0) {
    const next = [...parts];
    next[idx] = snapshot;
    return next;
  }
  const patched = keepLongerStreamedFields(decode(snapshot), decode(parts[idx]), owned);
  const next = [...parts];
  next[idx] = encode(snapshot, patched);
  return next;
}

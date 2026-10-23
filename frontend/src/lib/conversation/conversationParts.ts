// Message and part folding for the conversation reducer: offset-anchored
// deltas, final upserts that replace, and non-final upserts that never
// shorten streamed text (design §5.2).

import type { Message, Part } from '../api';
import type { SessionView } from '../sessionViewTypes';
import { decode, encode, getFieldByPath, keepLongerStreamedFields, setFieldByPath, upsertMessage } from '../sessionViewParts';
import { DELTA_FIELD_PATH, type OcePart, type PartDelta } from './events';
import { findIndexById, replaceAt } from './partIndex';

function toPart(part: OcePart): Part {
  const out: Part = { id: part.id, messageId: part.messageId, sessionId: part.sessionId, data: part.data };
  if (part.timeCreated) out.timeCreated = part.timeCreated;
  return out;
}

/** A minimal assistant message so parts that lead their message can render. */
function ensureMessage(messages: Message[], messageId: string, sessionId: string, created: unknown): Message[] {
  if (findIndexById(messages, messageId) >= 0) return messages;
  const timeCreated = typeof created === 'number' ? created : Date.now();
  return upsertMessage(messages, { id: messageId, sessionId, timeCreated, data: { role: 'assistant' } });
}

function withStale(view: SessionView, partId: string, stale: boolean): ReadonlySet<string> | undefined {
  const current = view._stale;
  if (stale === !!current?.has(partId)) return current;
  const next = new Set(current);
  if (stale) next.add(partId);
  else next.delete(partId);
  return next;
}

function ownField(owned: SessionView['_deltaOwnedFields'], partId: string, path: string) {
  if (owned.get(partId)?.has(path)) return owned;
  const next = new Map(owned);
  next.set(partId, new Set(owned.get(partId)).add(path));
  return next;
}

export function upsertPart(view: SessionView, oce: OcePart, final: boolean): SessionView {
  const incoming = toPart(oce);
  const idx = findIndexById(view.parts, incoming.id);
  const messages = ensureMessage(view.messages, incoming.messageId, view.sessionId,
    (oce.data as { time?: { created?: unknown } }).time?.created);
  if (idx < 0) {
    return { ...view, messages, parts: [...view.parts, incoming], _stale: withStale(view, incoming.id, !!view._stale?.has(incoming.id) && !final) };
  }
  const part = final ? incoming : encode(incoming, keepLongerStreamedFields(decode(incoming), decode(view.parts[idx])));
  // A snapshot as long as our retained prefix may still not cover a rejected
  // delta beyond that prefix. Only a final/authoritative replacement heals it.
  const stale = !!view._stale?.has(incoming.id) && !final;
  return { ...view, messages, parts: replaceAt(view.parts, idx, part), _stale: withStale(view, incoming.id, stale) };
}

function stubData(delta: PartDelta, path: string, text: string): Record<string, unknown> {
  const type = delta.partType || (delta.field === 'text' ? 'text' : 'tool');
  return setFieldByPath({ type }, path, text);
}

export function applyAnchoredDelta(view: SessionView, delta: PartDelta): SessionView {
  const path = DELTA_FIELD_PATH[delta.field];
  if (!path || !delta.text) return view;
  const idx = findIndexById(view.parts, delta.partId);
  const data = idx >= 0 ? decode(view.parts[idx]) : null;
  const current = data ? getFieldByPath(data, path) : undefined;
  const cur = typeof current === 'string' ? current : '';
  if (delta.offset > cur.length) {
    const stale = withStale(view, delta.partId, true);
    return stale === view._stale ? view : { ...view, _stale: stale };
  }
  const overlap = cur.length - delta.offset;
  if (overlap >= delta.text.length) return view;
  const value = cur + delta.text.slice(overlap);
  const messages = ensureMessage(view.messages, delta.messageId, view.sessionId, undefined);
  const parts = data
    ? replaceAt(view.parts, idx, encode(view.parts[idx], setFieldByPath(data, path, value)))
    : [...view.parts, { id: delta.partId, messageId: delta.messageId, sessionId: view.sessionId, data: stubData(delta, path, value) as unknown as string }];
  return { ...view, messages, parts, _deltaOwnedFields: ownField(view._deltaOwnedFields, delta.partId, path) };
}

export function removeMessage(view: SessionView, messageId: string): SessionView {
  if (findIndexById(view.messages, messageId) < 0 && !view.parts.some((p) => p.messageId === messageId)) return view;
  return {
    ...view,
    messages: view.messages.filter((m) => m.id !== messageId),
    parts: view.parts.filter((p) => p.messageId !== messageId),
  };
}

export function removePart(view: SessionView, partId: string): SessionView {
  if (findIndexById(view.parts, partId) < 0) return view;
  return { ...view, parts: view.parts.filter((p) => p.id !== partId), _stale: withStale(view, partId, false) };
}

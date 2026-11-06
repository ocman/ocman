import type { Part, PartData } from './api';

// Immutable part snapshots are shared by conversion, accounting and speech.
// Keep parsing below those consumers so none needs to import another consumer.
const parsedPartCache = new WeakMap<Part, PartData>();

export function parsePart(p: Part): PartData {
  const cached = parsedPartCache.get(p);
  if (cached !== undefined) return cached;
  let result: PartData;
  try {
    result = typeof p.data === 'string' ? JSON.parse(p.data) : p.data;
  } catch {
    result = (p.data || {}) as PartData;
  }
  parsedPartCache.set(p, result);
  return result;
}

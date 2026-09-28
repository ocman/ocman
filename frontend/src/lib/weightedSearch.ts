import { fuzzyMatch, fuzzyRank, fuzzyScore } from './format';

export type SearchKey<T> = keyof T | { name: keyof T; weight?: number };

/**
 * Ranks entries by their best weighted per-field fuzzy score. A match that
 * only exists across fields (e.g. "anthropic opus") still counts, ranked
 * lowest; pinned entries outrank every other match.
 */
export function weightedSearch<T>(entries: T[], query: string, searchKeys: SearchKey<T>[], pinned?: (entry: T) => boolean): T[] {
  const q = query.trim().split(/\s+/).filter(Boolean).join(' ');
  if (!q) return entries;
  const keys = searchKeys
    .map((key) => typeof key === 'object' ? { name: key.name, weight: key.weight ?? 1 } : { name: key, weight: 1 })
    .filter((key): key is { name: keyof T & string; weight: number } => typeof key.name === 'string');
  const field = (entry: T, name: string) => String((entry as Record<string, unknown>)[name] ?? '');
  const score = (entry: T) => {
    let best = -1;
    for (const { name, weight } of keys) {
      const s = fuzzyScore(q, field(entry, name));
      if (s >= 0) best = Math.max(best, s * weight);
    }
    if (best < 0 && fuzzyMatch(q, keys.map(({ name }) => field(entry, name)).join(' '))) best = 0;
    return best;
  };
  return fuzzyRank(entries, score, pinned);
}

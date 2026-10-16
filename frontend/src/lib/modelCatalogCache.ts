import type { SessionModelEntry } from './api';

// Last live model catalog per platform (compound ids keep machines apart),
// so the picker opens with data instead of an empty list and a failed
// refresh never downgrades it. Any session of the platform that reaches a
// live instance refreshes it for all of them.
// Memory keeps the full catalog; localStorage keeps only favorites, recents
// and the session default so a reload still has the short list.
// v3 keys by platform only (v2 was per directory and went stale).
const KEY = 'ocman.modelCatalog.v3';
const MAX_PLATFORMS = 30;

type Stored = Record<string, SessionModelEntry[]>;

const memory = new Map<string, SessionModelEntry[]>();

const modelKey = (e: SessionModelEntry) => `${e.provider}/${e.model}`;
const isPinned = (e: SessionModelEntry) => !!e.isFavorite || !!e.isSessionDefault || (e.recentRank ?? 0) > 0;

/** Without the provider catalog nobody knows what is available; treat every
 *  entry as usable rather than flagging (and archiving) all of them. */
export const availabilityUnknown = (entries: SessionModelEntry[]) => entries.map((e) => ({ ...e, isAvailable: true }));

function load(): Stored {
  try {
    const parsed = JSON.parse(window.localStorage.getItem(KEY) || '{}') as Stored;
    return parsed && typeof parsed === 'object' ? parsed : {};
  } catch {
    return {};
  }
}

/** The platform's last live catalog. */
export function readModelCatalog(platform: string | undefined): SessionModelEntry[] | undefined {
  if (!platform) return undefined;
  return memory.get(platform) ?? load()[platform];
}

/** Fresh entries from a response without provider data, completed with the
 *  cached provider details. Favorites, recents and the session default come
 *  from the response, which is authoritative for them. */
export function mergeWithCatalog(fresh: SessionModelEntry[], cached: SessionModelEntry[]): SessionModelEntry[] {
  const known = new Map(cached.map((e) => [modelKey(e), e]));
  const head = fresh.map((e) => {
    const c = known.get(modelKey(e));
    known.delete(modelKey(e));
    if (!c) return { ...e, isAvailable: true };
    return {
      ...e,
      providerName: e.providerName || c.providerName,
      modelName: e.modelName || c.modelName,
      isAvailable: c.isAvailable,
      isProviderDefault: c.isProviderDefault,
      reasoning: e.reasoning ?? c.reasoning,
    };
  });
  const tail = [...known.values()].map((e) => ({ ...e, isFavorite: false, isSessionDefault: false, recentRank: undefined }));
  return [...head, ...tail];
}

export function writeModelCatalog(platform: string | undefined, entries: SessionModelEntry[]) {
  if (!platform || entries.length === 0) return;
  memory.delete(platform);
  memory.set(platform, entries);
  if (memory.size > MAX_PLATFORMS) memory.delete(memory.keys().next().value!);
  const stored = load();
  delete stored[platform];
  stored[platform] = entries.filter(isPinned);
  const keys = Object.keys(stored);
  for (const k of keys.slice(0, Math.max(0, keys.length - MAX_PLATFORMS))) delete stored[k];
  try {
    window.localStorage.setItem(KEY, JSON.stringify(stored));
  } catch {
    // Storage full or unavailable: the memory copy still serves this tab.
  }
}

/** Test hook; `keepStorage` simulates a page reload. */
export function clearModelCatalogCache(keepStorage = false) {
  memory.clear();
  if (keepStorage) return;
  try { window.localStorage.removeItem(KEY); } catch { /* ignore */ }
}

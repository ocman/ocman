import type { SessionModelEntry } from './api';

// Last live model catalog per project directory, so the picker opens with
// data instead of an empty list and a failed refresh never downgrades it.
// Memory keeps the full catalog; localStorage keeps only favorites, recents
// and the session default so a reload still has the short list.
// v2 drops availability flags inferred from model catalog membership.
const KEY = 'ocman.modelCatalog.v2';
const MAX_PROJECTS = 30;

type Stored = Record<string, SessionModelEntry[]>;

const memory = new Map<string, SessionModelEntry[]>();

const keyOf = (platform: string, directory: string) => `${platform}\n${directory}`;
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

/** The last live catalog of exactly this directory. */
export function readModelCatalog(platform: string | undefined, directory: string | undefined): SessionModelEntry[] | undefined {
  if (!platform || !directory) return undefined;
  const key = keyOf(platform, directory);
  return memory.get(key) ?? load()[key];
}

/** Favorites and recents from the newest catalog of the platform, for a
 *  directory with none yet (a new worktree). They are global, but another
 *  project's provider state (availability, defaults, reasoning variants) is
 *  not, so it is dropped. */
export function readModelShortlist(platform: string | undefined): SessionModelEntry[] | undefined {
  if (!platform) return undefined;
  const prefix = `${platform}\n`;
  // Map and object keys keep insertion order and writes re-insert, so the
  // last match is the newest.
  const newest = (keys: string[]) => keys.filter((k) => k.startsWith(prefix)).pop();
  const mem = newest([...memory.keys()]);
  const stored = mem ? undefined : load();
  const disk = stored && newest(Object.keys(stored));
  const entries = mem ? memory.get(mem) : disk ? stored[disk] : undefined;
  const pinned = entries?.filter((e) => e.isFavorite || (e.recentRank ?? 0) > 0)
    .map((e) => ({ ...e, isSessionDefault: false, isProviderDefault: false, reasoning: undefined }));
  return pinned?.length ? availabilityUnknown(pinned) : undefined;
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

export function writeModelCatalog(platform: string | undefined, directory: string | undefined, entries: SessionModelEntry[]) {
  if (!platform || !directory || entries.length === 0) return;
  const key = keyOf(platform, directory);
  memory.delete(key);
  memory.set(key, entries);
  if (memory.size > MAX_PROJECTS) memory.delete(memory.keys().next().value!);
  const stored = load();
  delete stored[key];
  stored[key] = entries.filter(isPinned);
  const keys = Object.keys(stored);
  for (const k of keys.slice(0, Math.max(0, keys.length - MAX_PROJECTS))) delete stored[k];
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

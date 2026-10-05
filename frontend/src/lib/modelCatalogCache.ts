import type { SessionModelEntry } from './api';

// Last live model catalog per project directory, so the picker opens with
// data instead of an empty list and a failed refresh never downgrades it.
// Memory keeps the full catalog; localStorage keeps only favorites, recents
// and the session default so a reload still has the short list.
const KEY = 'ocman.modelCatalog.v1';
const MAX_PROJECTS = 30;

type Stored = Record<string, SessionModelEntry[]>;

const memory = new Map<string, SessionModelEntry[]>();

const keyOf = (platform: string, directory: string) => `${platform}\n${directory}`;

function load(): Stored {
  try {
    const parsed = JSON.parse(window.localStorage.getItem(KEY) || '{}') as Stored;
    return parsed && typeof parsed === 'object' ? parsed : {};
  } catch {
    return {};
  }
}

/** Cached catalog for the project; a new worktree falls back to the newest
 *  catalog of the same platform, since recents and favorites are global. */
export function readModelCatalog(platform: string | undefined, directory: string | undefined): SessionModelEntry[] | undefined {
  if (!platform || !directory) return undefined;
  const key = keyOf(platform, directory);
  const hit = memory.get(key) ?? load()[key];
  if (hit) return hit;
  const prefix = `${platform}\n`;
  // Map and object keys keep insertion order and writes re-insert, so the
  // last match is the newest.
  const newest = (keys: string[]) => keys.filter((k) => k.startsWith(prefix)).pop();
  const mem = newest([...memory.keys()]);
  if (mem) return memory.get(mem);
  const stored = load();
  const disk = newest(Object.keys(stored));
  return disk ? stored[disk] : undefined;
}

export function writeModelCatalog(platform: string | undefined, directory: string | undefined, entries: SessionModelEntry[]) {
  if (!platform || !directory || entries.length === 0) return;
  const key = keyOf(platform, directory);
  memory.delete(key);
  memory.set(key, entries);
  if (memory.size > MAX_PROJECTS) memory.delete(memory.keys().next().value!);
  const stored = load();
  delete stored[key];
  stored[key] = entries.filter((e) => e.isFavorite || e.isSessionDefault || (e.recentRank ?? 0) > 0);
  const keys = Object.keys(stored);
  for (const k of keys.slice(0, Math.max(0, keys.length - MAX_PROJECTS))) delete stored[k];
  try {
    window.localStorage.setItem(KEY, JSON.stringify(stored));
  } catch {
    // Storage full or unavailable: the memory copy still serves this tab.
  }
}

/** Test hook. */
export function clearModelCatalogCache() {
  memory.clear();
  try { window.localStorage.removeItem(KEY); } catch { /* ignore */ }
}

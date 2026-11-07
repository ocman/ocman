import type { Session } from './api';
import { compareSidebarCompletion } from './sidebarHelpers';

// Track only keys changed during active HTTP reads. Abort/completion releases
// each fence, so neither session objects nor a permanent revision index accumulate.
const active = new Map<Set<string>, () => void>();
const key = (row: Session) => `${row.platform}:${row.id}`;
export function beginCompactSidebarRead(signal?: AbortSignal): Set<string> {
  const changed = new Set<string>();
  const abort = () => finishCompactSidebarRead(changed);
  active.set(changed, () => signal?.removeEventListener('abort', abort));
  signal?.addEventListener('abort', abort, { once: true });
  if (signal?.aborted) finishCompactSidebarRead(changed);
  return changed;
}

export function finishCompactSidebarRead(read: Set<string>) {
  active.get(read)?.();
  active.delete(read);
}

export function markCompactSidebarUpdate(row: Session) {
  for (const changed of active.keys()) changed.add(key(row));
}

export function preserveCompactSidebarRows(incoming: Session[], current: Session[], changed: ReadonlySet<string>): Session[] {
  const liveRows = new Map(current.map(row => [key(row), row]));
  const seen = new Set<string>();
  const rows = incoming.flatMap(row => {
    const id = key(row);
    seen.add(id);
    if (!changed.has(id)) return [row];
    const live = liveRows.get(id);
    return live ? [live] : []; // A compact event removed the now-hidden row.
  });
  for (const row of current) if (!seen.has(key(row)) && changed.has(key(row))) rows.push(row);
  return rows.sort(compareSidebarCompletion);
}

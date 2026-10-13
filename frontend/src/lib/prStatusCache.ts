import type { PRMergeability } from './upstreamApi';
import { PR_CHECKS_REFRESH_EVENT } from './prChecksCache';
import { PREVIEW_AUTH_EVENT } from './previews';

type Entry = { value?: PRMergeability; promise: Promise<PRMergeability>; controller: AbortController; readers: number };
const entries = new Map<string, Entry>();

// Owner-scoped, memory-only: private preview data never enters localStorage.
export function acquirePRStatus(key: string, load: (signal: AbortSignal) => Promise<PRMergeability>, refresh = false, knownMergeability = false) {
  let entry = entries.get(key);
  if (refresh && entry?.value) entry = undefined;
  if (!entry) {
    if (entries.size >= 1000) entries.delete(entries.keys().next().value!);
    const controller = new AbortController();
    const created: Entry = { controller, readers: 0, promise: load(controller.signal) };
    entry = created;
    entries.set(key, created);
    created.promise.then((value) => {
      if (!controller.signal.aborted && value.approved != null && (knownMergeability || value.mergeable != null)) created.value = value;
      else if (entries.get(key) === created) entries.delete(key);
    }, () => { if (entries.get(key) === created) entries.delete(key); });
  }
  entry.readers++;
  const current = entry;
  return {
    promise: current.promise,
    release: () => {
      if (--current.readers === 0 && !current.value) {
        current.controller.abort();
        if (entries.get(key) === current) entries.delete(key);
      }
    },
  };
}

if (typeof window !== 'undefined') {
  window.addEventListener(PR_CHECKS_REFRESH_EVENT, (event) => {
    const repositories = (event as CustomEvent<string[]>).detail;
    for (const key of entries.keys()) {
      if (!repositories || repositories.some((repo) => key.split('\0')[1] === repo)) entries.delete(key);
    }
  });
  window.addEventListener(PREVIEW_AUTH_EVENT, () => entries.clear());
}

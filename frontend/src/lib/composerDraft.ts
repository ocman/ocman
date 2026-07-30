import { useMemo, useSyncExternalStore } from 'react';

const DRAFTS_KEY = 'ocman.composerDrafts.v1';

type Drafts = Record<string, string>;

function loadDrafts(): Drafts {
  if (typeof window === 'undefined') return {};

  try {
    const raw = window.localStorage.getItem(DRAFTS_KEY);
    if (!raw) return {};
    const parsed = JSON.parse(raw) as Drafts;
    if (!parsed || typeof parsed !== 'object') return {};
    return parsed;
  } catch {
    return {};
  }
}

function saveDrafts(data: Drafts) {
  if (typeof window === 'undefined') return;

  try {
    window.localStorage.setItem(DRAFTS_KEY, JSON.stringify(data));
  } catch {
    // Ignore storage errors (private mode, quotas, etc.)
  }
}

export function getDraft(sessionId: string): string {
  const drafts = loadDrafts();
  return drafts[sessionId] || '';
}

export function saveDraft(sessionId: string, text: string) {
  const drafts = loadDrafts();
  if (text) {
    drafts[sessionId] = text;
  } else {
    delete drafts[sessionId];
  }
  saveDrafts(drafts);
  emit();
}

export function clearDraft(sessionId: string) {
  const drafts = loadDrafts();
  delete drafts[sessionId];
  saveDrafts(drafts);
  emit();
}

// --- which sessions have an unsent draft (sidebar indicator) ---
// ponytail: the snapshot is the sorted id list joined into a string so
// useSyncExternalStore gets a stable primitive without a cache layer.

const listeners = new Set<() => void>();
let snapshot: string | null = null;

function computeSnapshot() {
  return Object.keys(loadDrafts()).sort().join('\n');
}

function emit() {
  const next = computeSnapshot();
  if (next === snapshot) return;
  snapshot = next;
  for (const l of listeners) l();
}

function subscribe(cb: () => void) {
  listeners.add(cb);
  // Another tab wrote drafts for the same user.
  const onStorage = (e: StorageEvent) => { if (e.key === DRAFTS_KEY) emit(); };
  window.addEventListener('storage', onStorage);
  return () => {
    listeners.delete(cb);
    window.removeEventListener('storage', onStorage);
  };
}

function getSnapshot() {
  if (snapshot === null) snapshot = computeSnapshot();
  return snapshot;
}

/** Session ids that currently hold an unsent composer draft. */
export function useDraftSessionIds(): Set<string> {
  const key = useSyncExternalStore(subscribe, getSnapshot, () => '');
  return useMemo(() => new Set(key ? key.split('\n') : []), [key]);
}

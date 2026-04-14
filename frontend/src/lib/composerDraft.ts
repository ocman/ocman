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
}

export function clearDraft(sessionId: string) {
  const drafts = loadDrafts();
  delete drafts[sessionId];
  saveDrafts(drafts);
}

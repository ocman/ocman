import { readStored, writeStorage } from './storage';

const SESSION_MODELS_KEY = 'ocman.sessionModels.v1';

function load(): Record<string, string> {
  return readStored(SESSION_MODELS_KEY, {}, (raw) => {
    const parsed: unknown = JSON.parse(raw);
    if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) return {};
    return Object.fromEntries(Object.entries(parsed).filter(([, value]) => typeof value === 'string'));
  });
}

// Only deliberate model choices belong here, never models observed in replies.
export function getSessionModel(sessionId: string): string {
  return sessionId ? load()[sessionId] || '' : '';
}

export function saveSessionModel(sessionId: string, model: string) {
  if (!sessionId || !model) return;
  writeStorage(SESSION_MODELS_KEY, JSON.stringify({ ...load(), [sessionId]: model }));
}

import { useEffect, useState } from 'react';
import { postJSON } from '../lib/api.requests';
import type { Session } from '../lib/api';
import { fuzzyRank } from '../lib/format';

type SessionSummary = Pick<Session, 'id' | 'title' | 'directory' | 'timeUpdated'>;
type SessionMatch = SessionSummary & { snippet: string };
export type PaletteSession = {
  kind: 'session';
  session: SessionSummary & Partial<Pick<Session, 'status' | 'seen' | 'pendingPermission' | 'pendingQuestion'>>;
  snippet?: string;
};

export function rankPaletteSessions(sessions: Session[], matches: SessionMatch[], score: (session: Session) => number): PaletteSession[] {
  const byID = new Map(sessions.map(session => [session.id, session]));
  const snippets = new Map(matches.map(match => [match.id, match.snippet]));
  const results = new Map<string, PaletteSession>();
  for (const session of fuzzyRank(sessions, score).slice(0, 20)) {
    results.set(session.id, { kind: 'session', session, snippet: snippets.get(session.id) });
  }
  for (const match of matches) {
    if (results.size >= 20) break;
    if (!results.has(match.id)) {
      results.set(match.id, { kind: 'session', session: byID.get(match.id) ?? match, snippet: match.snippet });
    }
  }
  return [...results.values()];
}

type ContentSearch = { matches: SessionMatch[]; status: string };
const idle: ContentSearch = { matches: [], status: '' };
const pending: ContentSearch = { matches: [], status: 'Searching conversations…' };

export function usePaletteContentSearch(enabled: boolean, input: string): ContentSearch {
  const query = enabled ? input.trim() : '';
  const searchable = query.length <= 256 && [...query].length >= 3;
  const [result, setResult] = useState<(ContentSearch & { query: string }) | null>(null);
  useEffect(() => {
    if (!searchable) return;
    const controller = new AbortController();
    const timer = window.setTimeout(() => {
      void postJSON<{ matches: SessionMatch[]; indexing: boolean }>('/api/sessions/search', { query }, { signal: controller.signal }).then(({ matches, indexing }) => {
        if (!controller.signal.aborted) setResult({ query, matches, status: indexing ? 'Still indexing older conversations…' : '' });
      }).catch(() => {
        if (!controller.signal.aborted) setResult({ query, matches: [], status: 'Conversation search unavailable' });
      });
    }, 200);
    return () => {
      window.clearTimeout(timer);
      controller.abort();
    };
  }, [query, searchable]);
  if (!searchable) return idle;
  return result?.query === query ? result : pending;
}

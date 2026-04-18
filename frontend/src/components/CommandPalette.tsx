import { useState, useEffect, useRef, useCallback, useMemo } from 'react';
import './CommandPalette.css';
import { useNavigate } from 'react-router-dom';
import Fuse from 'fuse.js';
import { useApiStore } from '../lib/apiStore';
import { cleanTitle, relativeTime, shortPath } from '../lib/format';
import { useShortcut } from '../lib/shortcutRegistry';
import type { Session } from '../lib/api';

export function CommandPalette() {
  const [open, setOpen] = useState(false);
  const [query, setQuery] = useState('');
  const [selectedIndex, setSelectedIndex] = useState(0);
  const inputRef = useRef<HTMLInputElement>(null);
  const listRef = useRef<HTMLDivElement>(null);
  const navigate = useNavigate();
  // Render directly from the cached sessions in the store. The palette picks
  // up updates automatically when the background refresh completes.
  const sessions = useApiStore((s) => s.cachedSessions);
  const refreshCachedSessions = useApiStore((s) => s.refreshCachedSessions);

  const close = useCallback(() => {
    setOpen(false);
    setQuery('');
    setSelectedIndex(0);
  }, []);

  const toggleOpen = useCallback(() => {
    setOpen((prev) => {
      if (prev) {
        setQuery('');
        setSelectedIndex(0);
        return false;
      }
      return true;
    });
  }, []);

  useShortcut({
    id: 'site.command-palette',
    scope: 'site',
    keys: { code: 'Space', alt: true },
    description: 'Open command palette',
    handler: toggleOpen,
  });

  // Refresh the cached session list in the background whenever the palette
  // opens. The list itself comes straight from the store, so the palette
  // renders instantly with whatever data we already have and then updates
  // when the fetch resolves.
  useEffect(() => {
    if (!open) return;
    const controller = new AbortController();
    refreshCachedSessions(controller.signal).catch(() => {});
    return () => controller.abort();
  }, [open, refreshCachedSessions]);

  // Focus input when opened
  useEffect(() => {
    if (open) {
      // Small delay to ensure the DOM is ready
      requestAnimationFrame(() => inputRef.current?.focus());
    }
  }, [open]);

  // Fuse instance
  const fuse = useMemo(
    () =>
      new Fuse(sessions ?? [], {
        // Search on the cleaned title so markdown markers (#, **, _, etc.)
        // don't interfere with user queries. `directory` is used as-is.
        keys: [
          { name: 'title', getFn: (s) => cleanTitle(s.title) },
          'directory',
        ],
        threshold: 0.4,
        includeScore: true,
      }),
    [sessions],
  );

  const results = useMemo(() => {
    if (!sessions) return [];
    if (!query.trim()) {
      // Show all sessions sorted by most recent
      return sessions
        .slice()
        .sort((a, b) => b.timeUpdated - a.timeUpdated)
        .slice(0, 20);
    }
    return fuse.search(query, { limit: 20 }).map((r) => r.item);
  }, [query, fuse, sessions]);

  // Scroll selected item into view
  useEffect(() => {
    if (!listRef.current) return;
    const item = listRef.current.children[selectedIndex] as HTMLElement | undefined;
    item?.scrollIntoView({ block: 'nearest' });
  }, [selectedIndex]);

  function selectSession(session: Session) {
    close();
    navigate(`/session/${session.id}`);
  }

  function onInputKeyDown(e: React.KeyboardEvent) {
    if (e.key === 'Escape') {
      e.preventDefault();
      close();
    } else if (e.key === 'ArrowDown') {
      e.preventDefault();
      setSelectedIndex((i) => Math.min(i + 1, results.length - 1));
    } else if (e.key === 'ArrowUp') {
      e.preventDefault();
      setSelectedIndex((i) => Math.max(i - 1, 0));
    } else if (e.key === 'Enter') {
      e.preventDefault();
      if (results[selectedIndex]) {
        selectSession(results[selectedIndex]);
      }
    }
  }

  if (!open) return null;

  return (
    <div className="oc-cmd-backdrop" onClick={close}>
      <div className="oc-cmd-palette" onClick={(e) => e.stopPropagation()}>
        <div className="oc-cmd-input-wrap">
          <i className="bi bi-search oc-cmd-search-icon" />
          <input
            ref={inputRef}
            className="oc-cmd-input"
            type="text"
            placeholder="Search sessions..."
            value={query}
            onChange={(e) => { setQuery(e.target.value); setSelectedIndex(0); }}
            onKeyDown={onInputKeyDown}
          />
          <kbd className="oc-cmd-kbd">ESC</kbd>
        </div>
        <div className="oc-cmd-results" ref={listRef}>
          {results.length === 0 && (
            <div className="oc-cmd-empty">
              {sessions === null ? 'Loading sessions...' : 'No sessions found'}
            </div>
          )}
          {results.map((session, i) => (
            <div
              key={session.id}
              className={`oc-cmd-item${i === selectedIndex ? ' oc-cmd-item--selected' : ''}`}
              onClick={() => selectSession(session)}
              onMouseEnter={() => setSelectedIndex(i)}
            >
              <span
                className="oc-cmd-status"
                data-status={session.pendingPermission || session.pendingQuestion ? 'pending' : session.status}
                title={session.pendingPermission || session.pendingQuestion ? 'Waiting for your response' : undefined}
              />
              <div className="oc-cmd-item-content">
                <span className="oc-cmd-title">
                  {cleanTitle(session.title) || 'Untitled'}
                </span>
                <span className="oc-cmd-meta">
                  {shortPath(session.directory)} &middot; {relativeTime(session.timeUpdated)}
                </span>
              </div>
            </div>
          ))}
        </div>
      </div>
    </div>
  );
}

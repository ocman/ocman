import { useState, useEffect, useRef, useCallback, useMemo } from 'react';
import { useNavigate } from 'react-router-dom';
import Fuse from 'fuse.js';
import { useHotkeys } from 'react-hotkeys-hook';
import { useApiStore } from '../lib/apiStore';
import { relativeTime, shortPath } from '../lib/format';
import type { Session } from '../lib/api';
import { isEditableTarget } from '../lib/shortcuts';

export function CommandPalette() {
  const [open, setOpen] = useState(false);
  const [query, setQuery] = useState('');
  const [sessions, setSessions] = useState<Session[]>([]);
  const [selectedIndex, setSelectedIndex] = useState(0);
  const inputRef = useRef<HTMLInputElement>(null);
  const listRef = useRef<HTMLDivElement>(null);
  const navigate = useNavigate();
  const getSessions = useApiStore((s) => s.getSessions);

  const close = useCallback(() => {
    setOpen(false);
    setQuery('');
    setSelectedIndex(0);
  }, []);

  useEffect(() => {
    const onKeyDown = (e: KeyboardEvent) => {
      if (e.defaultPrevented || e.repeat) return;

      // Ctrl+/ or Cmd+/ works everywhere including inside composer
      const isModSlash = (e.ctrlKey || e.metaKey) && (e.key === '/' || e.code === 'Slash');
      // Bare / only when not in an editable field
      const isBareSlash = e.key === '/' && !e.metaKey && !e.ctrlKey && !e.altKey && !isEditableTarget(e.target);

      if (!isModSlash && !isBareSlash) return;

      e.preventDefault();
      setOpen((prev) => {
        if (prev) {
          setQuery('');
          setSelectedIndex(0);
          return false;
        }
        return true;
      });
    };

    document.addEventListener('keydown', onKeyDown, true);
    return () => document.removeEventListener('keydown', onKeyDown, true);
  }, []);

  useHotkeys('esc', () => close(), {
    enabled: open,
    enableOnFormTags: ['INPUT', 'TEXTAREA', 'SELECT'],
    preventDefault: true,
  }, [close, open]);

  // Fetch sessions when palette opens
  useEffect(() => {
    if (!open) return;
    let cancelled = false;
    getSessions().then((data) => {
      if (!cancelled) setSessions(data);
    }).catch(() => {});
    return () => { cancelled = true; };
  }, [open, getSessions]);

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
      new Fuse(sessions, {
        keys: ['title', 'directory'],
        threshold: 0.4,
        includeScore: true,
      }),
    [sessions],
  );

  const results = useMemo(() => {
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
    if (e.key === 'ArrowDown') {
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
            <div className="oc-cmd-empty">No sessions found</div>
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
                data-status={session.status}
              />
              <div className="oc-cmd-item-content">
                <span className="oc-cmd-title">
                  {session.title || 'Untitled'}
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

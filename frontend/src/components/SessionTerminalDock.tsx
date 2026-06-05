import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { TerminalPane } from './TerminalPane';
import { ErrorBoundary } from './ErrorBoundary';
import type { TermWindow } from '../lib/api';
import { api } from '../lib/api';
import { remoteLog } from '../lib/remoteLog';
import './SessionTerminalDock.css';

const MIN_HEIGHT = 120;
const MAX_HEIGHT = 700;
const DEFAULT_HEIGHT = 280;
const HEIGHT_STORAGE_KEY = 'ocman.terminalDock.height';
const TITLE_POLL_MS = 3000;

interface SessionTerminalDockProps {
  /** Whether tmux is available on the host. */
  tmuxAvailable: boolean;
  /**
   * The OpenCode session's working directory. All terminal windows for
   * this dock live in the single `ocman` tmux session and are tracked
   * by this directory; the backend owns session/window management.
   */
  directory: string | undefined;
}

/** The numeric tab index from a window name (`ocman-<hash>-<n>` -> `n`). */
function tabIndex(window: string): string {
  const m = /-(\d+)$/.exec(window);
  return m ? m[1] : window;
}

/**
 * tabLabel is the display text for a tab: the live title (running
 * command / program-set pane title) when present, otherwise the tab
 * number. Long titles are truncated by CSS, not here.
 */
function tabLabel(win: TermWindow): string {
  return win.title.trim() || tabIndex(win.name);
}

function readStoredHeight(): number {
  // localStorage can throw (private mode, disabled storage, test envs
  // without a DOM storage shim); fall back to the default height.
  try {
    const raw = Number(localStorage.getItem(HEIGHT_STORAGE_KEY));
    if (!Number.isFinite(raw) || raw <= 0) return DEFAULT_HEIGHT;
    return Math.min(MAX_HEIGHT, Math.max(MIN_HEIGHT, raw));
  } catch {
    return DEFAULT_HEIGHT;
  }
}

/**
 * SessionTerminalDock renders a horizontal "Terminal" toggle centered
 * below the conversation. Opening it reveals a resizable panel with a
 * tab row — one tab per dedicated tmux terminal window for this
 * session's directory — plus a "+" to add terminals. Each tab attaches
 * an xterm.js terminal to its window; closing a tab kills the window.
 *
 * Windows are rediscovered from tmux on open, so reloading restores the
 * terminals that are still running.
 */
export function SessionTerminalDock({ tmuxAvailable, directory }: SessionTerminalDockProps) {
  const [open, setOpen] = useState(false);
  const [height, setHeight] = useState(readStoredHeight);
  const dragRef = useRef<{ startY: number; startHeight: number } | null>(null);

  const [windows, setWindows] = useState<TermWindow[]>([]);
  const [active, setActive] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  // A terminal can be mounted whenever tmux is available and we have a
  // directory to scope windows to. The backend owns the ocman session.
  const canOpen = useMemo(
    () => tmuxAvailable && !!directory,
    [tmuxAvailable, directory],
  );

  // Rediscover live terminal windows whenever a directory is available
  // so the strip shows tabs even while collapsed. While the panel is
  // open we also poll on an interval so tab titles track the running
  // command / program-set pane title. Does NOT auto-create — the first
  // window is created when the user opens the panel or clicks a tab/"+".
  useEffect(() => {
    if (!directory) {
      setWindows([]);
      setActive(null);
      return;
    }
    let cancelled = false;
    const refresh = async () => {
      try {
        const { windows: live } = await api.term.listWindows(directory);
        if (cancelled) return;
        setWindows(live);
        setActive((prev) =>
          prev && live.some((w) => w.name === prev) ? prev : live[0]?.name ?? null,
        );
      } catch (e) {
        if (!cancelled) remoteLog.error('terminal: listing windows failed', e);
      }
    };
    void refresh();
    // Poll for live titles only while the panel is open (avoids work
    // when the terminal isn't visible).
    const id = open ? window.setInterval(refresh, TITLE_POLL_MS) : undefined;
    return () => {
      cancelled = true;
      if (id !== undefined) window.clearInterval(id);
    };
  }, [directory, open]);

  // Opening the panel with no terminals yet creates the first one.
  useEffect(() => {
    if (!open || !directory || windows.length > 0 || busy) return;
    let cancelled = false;
    (async () => {
      setBusy(true);
      try {
        const { window } = await api.term.createWindow(directory);
        if (cancelled) return;
        setWindows([{ name: window, title: '' }]);
        setActive(window);
      } catch (e) {
        if (!cancelled) remoteLog.error('terminal: create window failed', e);
      } finally {
        if (!cancelled) setBusy(false);
      }
    })();
    return () => { cancelled = true; };
  }, [open, directory, windows.length, busy]);

  const handleAdd = useCallback(async () => {
    if (!directory || busy) return;
    setBusy(true);
    try {
      const { window } = await api.term.createWindow(directory);
      setWindows((prev) =>
        prev.some((w) => w.name === window) ? prev : [...prev, { name: window, title: '' }],
      );
      setActive(window);
      setOpen(true);
    } catch (e) {
      remoteLog.error('terminal: create window failed', e);
    } finally {
      setBusy(false);
    }
  }, [directory, busy]);

  // Clicking a tab selects it and ensures the panel is open.
  const handleSelect = useCallback((window: string) => {
    setActive(window);
    setOpen(true);
  }, []);

  const handleClose = useCallback(async (window: string) => {
    if (!directory) return;
    // Optimistically drop the tab; restore on failure.
    setWindows((prev) => {
      const next = prev.filter((w) => w.name !== window);
      setActive((cur) => (cur === window ? next[next.length - 1]?.name ?? null : cur));
      return next;
    });
    try {
      await api.term.killWindow(directory, window);
    } catch (e) {
      remoteLog.error('terminal: kill window failed', e);
      // Re-fetch to resync with reality.
      try {
        const { windows: live } = await api.term.listWindows(directory);
        setWindows(live);
        setActive((cur) =>
          cur && live.some((w) => w.name === cur) ? cur : live[0]?.name ?? null,
        );
      } catch { /* leave optimistic state */ }
    }
  }, [directory]);

  const onResizeStart = useCallback(
    (e: React.PointerEvent) => {
      e.preventDefault();
      (e.target as HTMLElement).setPointerCapture(e.pointerId);
      dragRef.current = { startY: e.clientY, startHeight: height };
    },
    [height],
  );

  const onResizeMove = useCallback((e: React.PointerEvent) => {
    const drag = dragRef.current;
    if (!drag) return;
    // Dragging up (smaller clientY) grows the panel.
    const next = Math.min(
      MAX_HEIGHT,
      Math.max(MIN_HEIGHT, drag.startHeight + (drag.startY - e.clientY)),
    );
    setHeight(next);
  }, []);

  const onResizeEnd = useCallback((e: React.PointerEvent) => {
    if (!dragRef.current) return;
    dragRef.current = null;
    (e.target as HTMLElement).releasePointerCapture(e.pointerId);
    try {
      localStorage.setItem(HEIGHT_STORAGE_KEY, String(height));
    } catch {
      /* storage unavailable — height just won't persist */
    }
  }, [height]);

  if (!canOpen) return null;

  return (
    <div className="oc-term-dock" data-testid="terminal-dock">
      {open && (
        <div
          className="oc-term-dock-resizer"
          role="separator"
          aria-orientation="horizontal"
          aria-label="Resize terminal"
          onPointerDown={onResizeStart}
          onPointerMove={onResizeMove}
          onPointerUp={onResizeEnd}
        />
      )}
      {/* Tab strip: Terminal | {tab} x | {tab} x | +
          Sits above the terminal pane when open; collapses to just this
          row when closed. */}
      <div className="oc-term-dock-strip" role="tablist" aria-label="Terminals">
        <button
          type="button"
          className={`oc-term-dock-tab${open ? ' active' : ''}`}
          onClick={() => setOpen((v) => !v)}
          title={open ? 'Hide terminal' : 'Show terminal'}
        >
          <i className="bi bi-terminal" aria-hidden="true" />
          <span>Terminal</span>
        </button>
        {windows.map((win) => (
          <div
            key={win.name}
            className={`oc-term-dock-tabitem${open && win.name === active ? ' active' : ''}`}
          >
            <button
              type="button"
              role="tab"
              aria-selected={open && win.name === active}
              className="oc-term-dock-tabbtn"
              onClick={() => handleSelect(win.name)}
              title={win.title || win.name}
            >
              <i className="bi bi-terminal-fill oc-term-dock-tabicon" aria-hidden="true" />
              <span className="oc-term-dock-tablabel">{tabLabel(win)}</span>
            </button>
            <button
              type="button"
              className="oc-term-dock-tabclose"
              onClick={() => { void handleClose(win.name); }}
              title="Close terminal"
              aria-label={`Close terminal ${tabLabel(win)}`}
            >
              <i className="bi bi-x" aria-hidden="true" />
            </button>
          </div>
        ))}
        <button
          type="button"
          className="oc-term-dock-tabadd"
          onClick={() => { void handleAdd(); }}
          disabled={busy}
          title="New terminal"
          aria-label="New terminal"
        >
          <i className="bi bi-plus" aria-hidden="true" />
        </button>
      </div>
      {open && (
        <div className="oc-term-dock-panel" style={{ height }}>
          {active && directory ? (
            <ErrorBoundary
              name="terminal:pane"
              inline
              resetKey={`${directory}:${active}`}
            >
              <TerminalPane
                key={`${directory}:${active}`}
                dir={directory}
                window={active}
              />
            </ErrorBoundary>
          ) : (
            <div className="oc-term-dock-empty">
              {busy ? 'Loading…' : 'No terminals'}
            </div>
          )}
        </div>
      )}
    </div>
  );
}

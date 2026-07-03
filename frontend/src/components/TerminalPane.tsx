import { useEffect, useRef, useState } from 'react';
import { Terminal } from '@xterm/xterm';
import { FitAddon } from '@xterm/addon-fit';
import { TerminalSkeleton } from './Skeleton';
import '@xterm/xterm/css/xterm.css';
import './TerminalPane.css';

type ConnState = 'connecting' | 'open' | 'closed' | 'error';

interface TerminalPaneProps {
  /**
   * The session's working directory (absolute). Identifies which set of
   * terminal windows this belongs to; required by the backend.
   */
  dir: string;
  /**
   * The specific terminal window to attach to. When omitted, the
   * backend reuses the first window for `dir` or creates one.
   */
  window?: string;
  /** Read-only attach (watch without sending input). */
  readonly?: boolean;
  /**
   * Owning machine id. When set (and not 'local'), the terminal attaches
   * to a PTY on that remote host instead of the hub.
   */
  remoteId?: string;
}

/**
 * TerminalPane renders an interactive xterm.js terminal attached to a
 * dedicated window in the single `ocman` tmux session, via the backend
 * WebSocket bridge (/api/term/ws). The window is sized independently per
 * viewer so multiple browser tabs don't fight over one client size.
 */
export function TerminalPane({ dir, window: win, readonly = false, remoteId }: TerminalPaneProps) {
  const containerRef = useRef<HTMLDivElement>(null);
  const [state, setState] = useState<ConnState>('connecting');

  useEffect(() => {
    const el = containerRef.current;
    if (!el) return;

    const term = new Terminal({
      convertEol: true,
      cursorBlink: !readonly,
      disableStdin: readonly,
      fontFamily: '"JetBrains Mono", ui-monospace, monospace',
      fontSize: 13,
      scrollback: 5000,
      theme: terminalTheme(),
    });
    const fit = new FitAddon();
    term.loadAddon(fit);
    term.open(el);
    fit.fit();

    // The dev server (vite :8228) and prod binary (:8229) both serve
    // /api on the same origin, so derive ws(s):// from window.location.
    const proto = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
    const params = new URLSearchParams({ dir });
    if (win) params.set('window', win);
    if (readonly) params.set('readonly', '1');
    // A remote project's terminal is tunnelled by the hub to the owning
    // machine; the hub still serves the WebSocket on this origin.
    if (remoteId && remoteId !== 'local') params.set('remoteId', remoteId);
    const ws = new WebSocket(
      `${proto}//${window.location.host}/api/term/ws?${params.toString()}`,
    );
    ws.binaryType = 'arraybuffer';

    const sendResize = () => {
      if (ws.readyState !== WebSocket.OPEN) return;
      ws.send(
        JSON.stringify({ type: 'resize', cols: term.cols, rows: term.rows }),
      );
    };

    ws.onopen = () => {
      setState('open');
      fit.fit();
      sendResize();
      if (!readonly) term.focus();
    };
    ws.onmessage = (e) => {
      if (typeof e.data === 'string') {
        term.write(e.data);
      } else {
        term.write(new Uint8Array(e.data as ArrayBuffer));
      }
    };
    ws.onclose = () => setState((s) => (s === 'error' ? s : 'closed'));
    ws.onerror = () => setState('error');

    const dataSub = readonly
      ? undefined
      : term.onData((d) => {
          if (ws.readyState === WebSocket.OPEN) ws.send(d);
        });

    // The browser hijacks a few Ctrl combos (Ctrl+C copy, Ctrl+V paste,
    // Ctrl+X cut, Ctrl+A select-all) when the terminal has a selection
    // or via the OS edit menu, pre-empting xterm so e.g. Ctrl+C never
    // emits onData and the running program is never interrupted. Send
    // the control byte ourselves for these and swallow the event, so
    // they behave like a real terminal. (Copy/paste are available via
    // Ctrl+Shift+C / Ctrl+Shift+V, which we leave for xterm/browser.)
    if (!readonly) {
      const HIJACKED: Record<string, number> = {
        c: 0x03, // ETX  -> SIGINT
        v: 0x16, // SYN  -> literal Ctrl+V (shell handles paste-as-needed)
        x: 0x18, // CAN
        a: 0x01, // SOH  -> start of line
      };
      term.attachCustomKeyEventHandler((e) => {
        if (e.type !== 'keydown') return true;
        const ctrlOnly = e.ctrlKey && !e.metaKey && !e.altKey && !e.shiftKey;
        if (!ctrlOnly) return true;
        const byte = HIJACKED[e.key.toLowerCase()];
        if (byte === undefined) return true; // let xterm handle the rest
        if (ws.readyState === WebSocket.OPEN) {
          ws.send(String.fromCharCode(byte));
        }
        e.preventDefault();
        e.stopPropagation();
        return false; // don't let xterm/browser also handle it
      });
    }

    const ro = new ResizeObserver(() => {
      try {
        fit.fit();
      } catch {
        /* element detached mid-resize */
      }
      sendResize();
    });
    ro.observe(el);

    return () => {
      ro.disconnect();
      dataSub?.dispose();
      ws.close();
      term.dispose();
    };
  }, [dir, win, readonly, remoteId]);

  return (
    <div className="oc-term" data-testid="terminal-pane">
      <div className="oc-term-screen" ref={containerRef} />
      {/* While connecting, overlay a loading skeleton so clicking a tab
          gives instant feedback before the PTY attaches. The xterm
          screen mounts underneath and the skeleton is removed once the
          socket is open. */}
      {state === 'connecting' && (
        <div className="oc-term-overlay">
          <TerminalSkeleton rows={7} />
        </div>
      )}
      {(state === 'closed' || state === 'error') && (
        <div className="oc-term-status" data-testid="terminal-status">
          {state === 'closed' && 'Disconnected'}
          {state === 'error' && 'Connection failed'}
        </div>
      )}
    </div>
  );
}

/**
 * terminalTheme maps the app's CSS design tokens onto xterm's theme so
 * the terminal matches the surrounding UI (Catppuccin-style palette).
 * Falls back to xterm defaults when a token is unset.
 */
function terminalTheme(): NonNullable<ConstructorParameters<typeof Terminal>[0]>['theme'] {
  const css = getComputedStyle(document.documentElement);
  const v = (name: string, fallback: string) =>
    css.getPropertyValue(name).trim() || fallback;
  return {
    background: v('--bg', '#1e1e2e'),
    foreground: v('--text', '#cdd6f4'),
    cursor: v('--accent', '#cdd6f4'),
    selectionBackground: v('--bg-active', 'rgba(255,255,255,0.2)'),
  };
}

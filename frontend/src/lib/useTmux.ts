import { useState, useEffect, useCallback } from 'react';
import type { TmuxClient, TmuxSession } from './api';
import { useApiStore } from './apiStore';

function checkIsLocal(): boolean {
  const h = window.location.hostname;
  return h === 'localhost' || h === '127.0.0.1' || h === '::1';
}

export interface TmuxState {
  available: boolean;
  /** True when accessing from localhost -- client defaults to /dev/ttys000. */
  isLocal: boolean;
  sessions: TmuxSession[];
  clients: TmuxClient[];
  /** Switch the given tmux client to the session. Returns a promise. */
  switchSession: (tmuxSessionName: string, clientTTY?: string) => Promise<void>;
  /** Find the tmux session whose resolved path matches the given directory. */
  findSession: (directory: string) => TmuxSession | undefined;
  /**
   * Find or create a tmux session for the given directory and run
   * `opencode --port 0` in a new window. Returns the tmux session name.
   */
  launchOpencode: (directory: string) => Promise<{ session: string }>;
}

export function useTmux(): TmuxState {
  const [available, setAvailable] = useState(false);
  const [sessions, setSessions] = useState<TmuxSession[]>([]);
  const [clients, setClients] = useState<TmuxClient[]>([]);
  const isLocal = checkIsLocal();
  const getTmuxSessions = useApiStore((state) => state.getTmuxSessions);
  const getTmuxClients = useApiStore((state) => state.getTmuxClients);
  const switchTmuxSession = useApiStore((state) => state.switchTmuxSession);
  const launchOpencodeInTmux = useApiStore((state) => state.launchOpencodeInTmux);

  useEffect(() => {
    let cancelled = false;
    (async () => {
      try {
        // Always fetch sessions. Only fetch clients for remote users
        // (local users always use /dev/ttys000 via the server default).
        const sessRes = await getTmuxSessions();
        if (cancelled) return;

        let tmuxClients: TmuxClient[] = [];
        if (!isLocal) {
          const cliRes = await getTmuxClients();
          if (cancelled) return;
          tmuxClients = cliRes.clients || [];
        }

        setAvailable(sessRes.available);
        setSessions(sessRes.sessions || []);
        setClients(tmuxClients);
      } catch {
        if (!cancelled) setAvailable(false);
      }
    })();
    return () => { cancelled = true; };
  }, [getTmuxClients, getTmuxSessions, isLocal]);

  const switchSession = useCallback(async (tmuxSessionName: string, clientTTY?: string) => {
    await switchTmuxSession(tmuxSessionName, clientTTY);
  }, [switchTmuxSession]);

  const findSession = useCallback((directory: string) => {
    return sessions.find(ts => ts.resolvedPath === directory);
  }, [sessions]);

  const launchOpencode = useCallback((directory: string) => {
    return launchOpencodeInTmux(directory);
  }, [launchOpencodeInTmux]);

  return { available, isLocal, sessions, clients, switchSession, findSession, launchOpencode };
}

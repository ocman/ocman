import { useState, useEffect, useCallback } from 'react';
import { api } from './api';
import type { TmuxClient, TmuxSession } from './api';

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
}

export function useTmux(): TmuxState {
  const [available, setAvailable] = useState(false);
  const [sessions, setSessions] = useState<TmuxSession[]>([]);
  const [clients, setClients] = useState<TmuxClient[]>([]);
  const isLocal = checkIsLocal();

  useEffect(() => {
    let cancelled = false;
    (async () => {
      try {
        // Always fetch sessions. Only fetch clients for remote users
        // (local users always use /dev/ttys000 via the server default).
        const sessRes = await api.tmuxSessions();
        if (cancelled) return;

        let tmuxClients: TmuxClient[] = [];
        if (!isLocal) {
          const cliRes = await api.tmuxClients();
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
  }, [isLocal]);

  const switchSession = useCallback(async (tmuxSessionName: string, clientTTY?: string) => {
    await api.tmuxSwitch(tmuxSessionName, clientTTY);
  }, []);

  const findSession = useCallback((directory: string) => {
    return sessions.find(ts => ts.resolvedPath === directory);
  }, [sessions]);

  return { available, isLocal, sessions, clients, switchSession, findSession };
}

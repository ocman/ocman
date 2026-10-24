import { useQuery } from '@tanstack/react-query';
import { api, type TmuxClient, type TmuxSession } from './api';
import { queryClient } from './queryClient';

function checkIsLocal(): boolean {
  const h = window.location.hostname;
  return h === 'localhost' || h === '127.0.0.1' || h === '::1';
}

export interface TmuxState {
  available: boolean;
  /** True when accessing from localhost. */
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
  const query = useQuery({
    queryKey: ['tmux'],
    queryFn: async ({ signal }) => {
      const [sessions, clients] = await Promise.all([api.tmuxSessions(signal), api.tmuxClients(signal)]);
      return { available: sessions.available, sessions: sessions.sessions ?? [], clients: clients.clients ?? [] };
    },
    staleTime: 0,
    retry: false,
    refetchOnWindowFocus: false,
  }, queryClient);
  const available = !query.error && query.data?.available === true;
  const sessions = query.data?.sessions ?? EMPTY_SESSIONS;
  const clients = query.data?.clients ?? EMPTY_CLIENTS;
  const isLocal = checkIsLocal();

  const findSession = (directory: string) => {
    return sessions.find(ts => ts.resolvedPath === directory);
  };

  return { available, isLocal, sessions, clients, switchSession: api.tmuxSwitch, findSession, launchOpencode: api.tmuxLaunchOpencode };
}

const EMPTY_SESSIONS: TmuxSession[] = [];
const EMPTY_CLIENTS: TmuxClient[] = [];

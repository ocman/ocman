import { useCallback, useEffect, useRef, useState } from 'react';
import type { Dispatch, MutableRefObject, SetStateAction } from 'react';
import { api } from '../../lib/api';
import type { AgentInfo, SessionModelEntry } from '../../lib/api';
import { NEW_SESSION_ID } from '../../lib/newSessionPath';
import { useModelCatalog } from './useModelCatalog';

export interface UseSessionCapabilitiesOptions {
  /** Active session id from the URL. */
  id: string | undefined;
  /** Owning platform of the session — passed through to the
   *  add/removeFavorite calls. Falls back to `''` when unknown. */
  platform: string | undefined;
  /** Whether the session has a live connection (mirrors session.liveConnection). */
  liveConnection: boolean;
  /** Session's working directory; agents are scoped per-directory. */
  directory: string | undefined;
  /** Whether the loaded session belongs to `id`. Right after a switch
   *  the page still holds the previous session, whose live bit would
   *  make the model fetch run twice. */
  sessionLoaded: boolean;
}

export interface UseSessionCapabilitiesResult {
  /** Live-connection availability. Mirrors session.liveConnection
   *  but also flips to true the moment SSE opens (so the composer
   *  un-greys before the next /api/sessions poll lands). */
  portAvailable: boolean;
  setPortAvailable: Dispatch<SetStateAction<boolean>>;
  /** Ref-mirror so palette commands and the SSE handler read the
   *  latest value without needing it as an effect dependency. */
  portAvailableRef: MutableRefObject<boolean>;
  /** Whether the agent catalog has been fetched (or known to be
   *  empty). UI defers per-agent coloring until this flips true. */
  agentsLoaded: boolean;
  setAgentsLoaded: Dispatch<SetStateAction<boolean>>;
  /** Agents reported by the platform's /agent endpoint. */
  agents: AgentInfo[];
  setAgents: Dispatch<SetStateAction<AgentInfo[]>>;
  /** Provider/model strings for the model picker drop-down. */
  modelOptions: string[];
  setModelOptions: Dispatch<SetStateAction<string[]>>;
  /** Detailed model entries (with favorites + recents metadata)
   *  used by the rich model picker. */
  modelEntries: SessionModelEntry[];
  setModelEntries: Dispatch<SetStateAction<SessionModelEntry[]>>;
  /** Selected model / agent / reasoning level for the next message. */
  selectedModel: string;
  setSelectedModel: Dispatch<SetStateAction<string>>;
  selectedAgent: string;
  setSelectedAgent: Dispatch<SetStateAction<string>>;
  selectedReasoning: string;
  setSelectedReasoning: Dispatch<SetStateAction<string>>;
  /** Re-fetch the session-scoped model list. Falls back to the
   *  historical list when /api/session-models is unreachable. */
  refreshModels: (signal?: AbortSignal) => void;
  /** Re-fetch both the agent catalog and the model list. Used after
   *  the session's OpenCode instance is restarted, since the new
   *  instance may expose a different config. */
  reloadCapabilities: () => void;
  /** Toggle a favorite model on/off. Optimistic with revert. */
  handleToggleFavorite: (provider: string, model: string, nextFavorite: boolean) => Promise<void>;
}

/**
 * Owns the per-session capability state: port availability, agent
 * catalog, model picker contents, and the selected model / agent /
 * reasoning trio. The session-change effect (which resets these on
 * navigation) stays in the page since it also touches messages /
 * cache / SSE state — exposing setters here is enough for the page
 * to drive the reset.
 */
export function useSessionCapabilities({
  id,
  platform,
  liveConnection,
  directory,
  sessionLoaded,
}: UseSessionCapabilitiesOptions): UseSessionCapabilitiesResult {
  const [portAvailable, setPortAvailable] = useState(false);
  const {
    modelOptions, setModelOptions, modelEntries, setModelEntries, refreshModels, handleToggleFavorite,
  } = useModelCatalog(id, platform, directory, sessionLoaded);
  const [selectedModel, setSelectedModel] = useState('');
  const [selectedAgent, setSelectedAgent] = useState('');
  const [selectedReasoning, setSelectedReasoning] = useState('');
  const [agents, setAgents] = useState<AgentInfo[]>([]);
  const [agentsLoaded, setAgentsLoaded] = useState(false);
  // Bumped to force a re-fetch of the agent catalog when nothing in
  // the session identity changed (e.g. after an OpenCode restart).
  const [reloadNonce, setReloadNonce] = useState(0);

  // Ref-mirror for callers that must read portAvailable without
  // re-running on every change (palette commands, SSE handler).
  const portAvailableRef = useRef(portAvailable);
  useEffect(() => {
    portAvailableRef.current = portAvailable;
  }, [portAvailable]);

  // Mirror session.liveConnection into portAvailable. This must flow
  // both ways: once a previous session made the composer available,
  // navigating to a disconnected session must explicitly clear the
  // bit or the next page inherits a stale write capability.
  useEffect(() => {
    // eslint-disable-next-line react-hooks/set-state-in-effect
    setPortAvailable(liveConnection);
  }, [id, liveConnection]);

  // Fetch the platform's composer-agent catalog. Platforms without
  // an agent catalog return an empty list, leaving agentColor to
  // fall back to its deterministic defaults. The synchronous
  // setAgents/setAgentsLoaded calls in the early-return branches
  // mirror the pre-extraction behaviour: they fire once per
  // session-change, not on every render.
  /* eslint-disable react-hooks/set-state-in-effect */
  useEffect(() => {
    if (!directory) {
      setAgents([]);
      setAgentsLoaded(false);
      return;
    }
    if (!portAvailable) {
      // No live instance to query — fallback colours are all we'll
      // get, mark loaded so UI can apply them without flicker.
      setAgents([]);
      setAgentsLoaded(true);
      return;
    }
    if (!id) {
      setAgents([]);
      setAgentsLoaded(true);
      return;
    }
    setAgentsLoaded(false);
    const controller = new AbortController();
    api.agents(id, controller.signal, platform)
      .then((list) => {
        if (controller.signal.aborted) return;
        setAgents(list || []);
        setAgentsLoaded(true);
      })
      .catch((e) => {
        if (e instanceof DOMException && e.name === 'AbortError') return;
        setAgents([]);
        setAgentsLoaded(true);
      });
    return () => controller.abort();
  }, [id, directory, platform, portAvailable, reloadNonce]);
  /* eslint-enable react-hooks/set-state-in-effect */

  // Fetch the session-scoped model list once the session has loaded,
  // and once more if OpenCode becomes reachable afterwards so the
  // picker picks up the full /config/providers catalog. A ref gates
  // the effect because the live bit can settle after the first fetch;
  // an effect-scoped abort would cancel the only request on that churn.
  // `liveConnection` belongs to the loaded session; `portAvailable` can
  // still hold the previous session's bit for a render, and only ever
  // turns true when `liveConnection` is.
  const live = liveConnection;
  const modelsFetchRef = useRef<{ id: string; platform: string | undefined; directory: string | undefined; live: boolean; controller: AbortController } | null>(null);
  // A session, owner or directory change cancels the outgoing request at once, even while the
  // next session is still loading, so its response cannot reach the
  // next session's picker.
  useEffect(() => () => {
    modelsFetchRef.current?.controller.abort();
    modelsFetchRef.current = null;
  }, [id, platform, directory]);
  useEffect(() => {
    if (!id || id === NEW_SESSION_ID || !sessionLoaded) return;
    const last = modelsFetchRef.current;
    if (last?.id === id && last.platform === platform && last.directory === directory && (last.live || !live)) return;
    last?.controller.abort();
    const controller = new AbortController();
    modelsFetchRef.current = { id, platform, directory, live, controller };
    refreshModels(controller.signal);
  }, [id, platform, directory, live, sessionLoaded, refreshModels]);

  const reloadCapabilities = useCallback(() => {
    setReloadNonce((n) => n + 1);
    refreshModels();
  }, [refreshModels]);

  return {
    portAvailable,
    setPortAvailable,
    portAvailableRef,
    agentsLoaded,
    setAgentsLoaded,
    agents,
    setAgents,
    modelOptions,
    setModelOptions,
    modelEntries,
    setModelEntries,
    selectedModel,
    setSelectedModel,
    selectedAgent,
    setSelectedAgent,
    selectedReasoning,
    setSelectedReasoning,
    refreshModels,
    reloadCapabilities,
    handleToggleFavorite,
  };
}

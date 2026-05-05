import { useCallback, useEffect, useRef, useState } from 'react';
import type { Dispatch, MutableRefObject, SetStateAction } from 'react';
import { api } from '../../lib/api';
import type { AgentInfo, SessionModelEntry } from '../../lib/api';
import { useApiStore } from '../../lib/apiStore';
import { formatModelRef } from '../../lib/sessionStatus';

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
}: UseSessionCapabilitiesOptions): UseSessionCapabilitiesResult {
  const getModels = useApiStore((s) => s.getModels);

  const [portAvailable, setPortAvailable] = useState(false);
  const [modelOptions, setModelOptions] = useState<string[]>([]);
  const [modelEntries, setModelEntries] = useState<SessionModelEntry[]>([]);
  const [selectedModel, setSelectedModel] = useState('');
  const [selectedAgent, setSelectedAgent] = useState('');
  const [selectedReasoning, setSelectedReasoning] = useState('');
  const [agents, setAgents] = useState<AgentInfo[]>([]);
  const [agentsLoaded, setAgentsLoaded] = useState(false);

  // Ref-mirror for callers that must read portAvailable without
  // re-running on every change (palette commands, SSE handler).
  const portAvailableRef = useRef(portAvailable);
  useEffect(() => {
    portAvailableRef.current = portAvailable;
  }, [portAvailable]);

  // Mirror session.liveConnection into portAvailable. The SSE
  // handler also flips this to true on connection so the composer
  // un-greys without waiting for the next /api/sessions poll. The
  // synchronous setState here is intentional — `liveConnection`
  // changes infrequently (once per session-load) so the cascading
  // render is unavoidable and harmless.
  useEffect(() => {
    // eslint-disable-next-line react-hooks/set-state-in-effect
    if (liveConnection) setPortAvailable(true);
  }, [liveConnection]);

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
    api.agents(id, controller.signal)
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
  }, [id, directory, portAvailable]);
  /* eslint-enable react-hooks/set-state-in-effect */

  const refreshModels = useCallback((signal?: AbortSignal) => {
    if (!id) return;
    api.sessionModels(id).then((resp) => {
      if (signal?.aborted) return;
      setModelEntries(resp.models || []);
      setModelOptions(
        Array.from(new Set((resp.models || []).map((m) => formatModelRef(m.provider, m.model)))),
      );
    }).catch(() => {
      if (signal?.aborted) return;
      // Fallback: historical-only list. Only seed empties when we
      // don't already have data so a transient picker-open refresh
      // failure doesn't wipe out the catalog the user is currently
      // looking at.
      getModels()
        .then((models) => {
          if (signal?.aborted) return;
          const ordered = [...models]
            .sort((a, b) => b.count - a.count)
            .map((m) => formatModelRef(m.provider, m.model));
          setModelEntries((prev) => prev.length > 0 ? prev : models.map((m) => ({
            provider: m.provider,
            model: m.model,
          })));
          setModelOptions((prev) => prev.length > 0 ? prev : Array.from(new Set(ordered)));
        })
        .catch(() => { /* keep existing data on failure */ });
    });
  }, [id, getModels]);

  // Re-fetch the session-scoped model list once OpenCode becomes
  // reachable so the picker picks up the full /config/providers
  // catalog. The initial fetch in the load() flow may have run
  // before discovery completed.
  useEffect(() => {
    if (!id || !portAvailable) return;
    const controller = new AbortController();
    refreshModels(controller.signal);
    return () => controller.abort();
  }, [id, portAvailable, refreshModels]);

  // Toggle a favorite model. Optimistic flip in the picker first,
  // then re-fetch for authoritative ordering. On error revert.
  const handleToggleFavorite = useCallback(async (
    provider: string,
    model: string,
    nextFavorite: boolean,
  ) => {
    if (!platform || !id) return;
    setModelEntries((prev) => prev.map((e) =>
      e.provider === provider && e.model === model ? { ...e, isFavorite: nextFavorite } : e,
    ));
    try {
      if (nextFavorite) {
        await api.addFavorite(platform, provider, model);
      } else {
        await api.removeFavorite(platform, provider, model);
      }
      // Re-fetch for authoritative ordering.
      const resp = await api.sessionModels(id);
      setModelEntries(resp.models || []);
    } catch {
      // Revert on error.
      setModelEntries((prev) => prev.map((e) =>
        e.provider === provider && e.model === model ? { ...e, isFavorite: !nextFavorite } : e,
      ));
    }
  }, [platform, id]);

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
    handleToggleFavorite,
  };
}

import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react';
import { api } from '../../lib/api';
import type { SessionModelEntry, SessionModelsResponse } from '../../lib/api';
import { useApiStore } from '../../lib/apiStore';
import { formatModelRef } from '../../lib/sessionStatus';
import {
  availabilityUnknown, mergeWithCatalog, readModelCatalog, readModelShortlist, writeModelCatalog,
} from '../../lib/modelCatalogCache';

const refsOf = (entries: SessionModelEntry[]) => Array.from(new Set(entries.map((m) => formatModelRef(m.provider, m.model))));

/** The session's model picker contents: the project's cached catalog first,
 *  replaced by the server's list, with favorites toggled in place. */
export function useModelCatalog(id: string | undefined, platform: string | undefined, directory: string | undefined, sessionLoaded: boolean) {
  const scope = useMemo(() => ({ id, platform, directory }), [id, platform, directory]);
  const activeScope = useRef<typeof scope | null>(scope);
  const requestGeneration = useRef(0);
  const pendingFavorites = useRef(new Set<string>());
  useLayoutEffect(() => {
    activeScope.current = scope;
    pendingFavorites.current.clear();
    return () => { activeScope.current = null; };
  }, [scope]);
  const getModels = useApiStore((s) => s.getModels);
  const [modelOptions, setModelOptions] = useState<string[]>([]);
  const [modelEntries, setModelEntries] = useState<SessionModelEntry[]>([]);

  const applyModels = useCallback((entries: SessionModelEntry[]) => {
    setModelEntries(entries);
    setModelOptions(refsOf(entries));
  }, []);
  // Seed only an empty picker so a failed refresh never wipes the list on screen.
  const seedModels = useCallback((entries: SessionModelEntry[]) => {
    setModelEntries((prev) => prev.length > 0 ? prev : entries);
    setModelOptions((prev) => prev.length > 0 ? prev : refsOf(entries));
  }, []);

  // Show the project's last known catalog the moment the session loads;
  // the fetch replaces it.
  useEffect(() => {
    if (!sessionLoaded) return;
    const cached = readModelCatalog(platform, directory) ?? readModelShortlist(platform);
    // eslint-disable-next-line react-hooks/set-state-in-effect
    if (cached) applyModels(cached);
  }, [sessionLoaded, platform, directory, applyModels]);

  // Only a response with the live provider catalog says which models are
  // available. Without it, complete the response from this directory's last
  // live catalog; failing that, show it without flagging every model as
  // unavailable, which put a warning on the model of a running session.
  const receiveModels = useCallback((resp: SessionModelsResponse) => {
    const entries = resp.models || [];
    if (resp.hasProviders) {
      writeModelCatalog(platform, directory, entries);
      applyModels(entries);
      return;
    }
    const cached = readModelCatalog(platform, directory);
    applyModels(cached ? mergeWithCatalog(entries, cached) : availabilityUnknown(entries));
  }, [platform, directory, applyModels]);

  const refreshModels = useCallback((signal?: AbortSignal) => {
    if (!id || activeScope.current !== scope) return;
    const generation = ++requestGeneration.current;
    const isCurrent = () => activeScope.current === scope && requestGeneration.current === generation && !signal?.aborted;
    api.sessionModels(id, platform).then((resp) => {
      if (isCurrent()) receiveModels(resp);
    }).catch(() => {
      if (!isCurrent()) return;
      // Fallback: the cached catalog, else the historical-only list.
      const cached = readModelCatalog(platform, directory) ?? readModelShortlist(platform);
      if (cached) {
        seedModels(cached);
        return;
      }
      getModels()
        .then((models) => {
          if (!isCurrent()) return;
          const ordered = [...models].sort((a, b) => b.count - a.count);
          seedModels(availabilityUnknown(ordered.map(({ provider, model }) => ({ provider, model }))));
        })
        .catch(() => { /* keep existing data on failure */ });
    });
  }, [id, platform, directory, getModels, receiveModels, seedModels, scope]);

  // Toggle a favorite model. Optimistic flip in the picker first,
  // then re-fetch for authoritative ordering. On error revert.
  const handleToggleFavorite = useCallback(async (provider: string, model: string, nextFavorite: boolean) => {
    if (!platform || !id || activeScope.current !== scope) return;
    const key = JSON.stringify([provider, model]);
    // Only one write per model until it settles; different models stay independent.
    if (pendingFavorites.current.has(key)) return;
    pendingFavorites.current.add(key);
    requestGeneration.current++;
    const isCurrent = () => activeScope.current === scope;
    const flip = (favorite: boolean) => setModelEntries((prev) => prev.map((e) =>
      e.provider === provider && e.model === model ? { ...e, isFavorite: favorite } : e,
    ));
    flip(nextFavorite);
    try {
      if (nextFavorite) {
        await api.addFavorite(platform, provider, model);
      } else {
        await api.removeFavorite(platform, provider, model);
      }
      if (isCurrent()) refreshModels();
    } catch {
      if (isCurrent()) flip(!nextFavorite);
    } finally {
      if (isCurrent()) pendingFavorites.current.delete(key);
    }
  }, [platform, id, refreshModels, scope]);

  return { modelOptions, setModelOptions, modelEntries, setModelEntries, refreshModels, handleToggleFavorite };
}

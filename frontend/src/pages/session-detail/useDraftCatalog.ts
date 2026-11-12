import { useEffect, useMemo, useState } from 'react';
import { api, type NewSessionTarget, type SessionModelsResponse } from '../../lib/api';
import { cacheNewSessionCatalog, getNewSessionCatalog } from '../../lib/newSessionCatalogCache';
import { loadProjectSettings } from '../../lib/projectSettingsCache';
import { prepareDraftCatalog, type DraftCatalog } from './draftCatalog';
import { basePlatform, splitModelRef } from '../../lib/modelRef';

// Optional model discovery enriches the picker, never the submission gate.
export function useDraftCatalog(target: NewSessionTarget, key: string, request: string): {
  catalog: DraftCatalog | undefined; defaultCatalog: DraftCatalog | undefined; catalogReady: boolean; catalogError: string;
} {
  const { directory, remoteId, platform } = target;
  const cached = useMemo(() => getNewSessionCatalog(key), [key]);
  const [prepared, setPrepared] = useState<{ key: string; request: string; catalog: DraftCatalog }>();
  const [inventory, setInventory] = useState<{ request: string; models: SessionModelsResponse }>();
  const [failure, setFailure] = useState<{ request: string; message: string }>();
  useEffect(() => {
    const controller = new AbortController();
    const target = { directory, remoteId, platform };
    Promise.all([
      api.prepareSession({ ...target, selectedOnly: true }, controller.signal),
      loadProjectSettings(directory, remoteId),
    ]).then(async ([initial, settings]) => {
      const preferred = splitModelRef(settings.defaults?.model || initial.projectDefaultModel || '').platform;
      const selected = preferred && preferred.startsWith('cnx_') && !platform && basePlatform(initial.platform) !== preferred;
      const result = selected ? await api.prepareSession({ ...target, platform: !remoteId || remoteId === 'local' ? preferred : `r-${remoteId}:${preferred}`, selectedOnly: true }, controller.signal) : initial;
      if (controller.signal.aborted) return;
      const catalog = prepareDraftCatalog(result, settings);
      cacheNewSessionCatalog(key, catalog);
      setPrepared({ key, request, catalog });
      if (result.additionalModels === false) return;
      // The existing full inventory remains available to the picker. Its
      // failures cannot invalidate a successfully prepared selected platform.
      void api.prepareSession({ ...target, platform: result.platform }, controller.signal).then((inventory) => {
        if (controller.signal.aborted || inventory.platform !== result.platform) return;
        const enriched = { ...catalog, models: prepareDraftCatalog(inventory, settings).models };
        cacheNewSessionCatalog(key, enriched);
        setInventory({ request, models: enriched.models });
      }).catch(() => {});
    }).catch((err) => {
      if (controller.signal.aborted) return;
      setFailure({ request, message: err instanceof Error ? err.message : String(err) });
    });
    return () => controller.abort();
  }, [directory, remoteId, platform, key, request]);
  const selected = prepared?.key === key ? prepared.catalog : cached;
  const catalog = useMemo(() => selected && inventory?.request === request ? { ...selected, models: inventory.models } : selected, [selected, inventory, request]);
  return {
    catalog,
    defaultCatalog: selected,
    catalogReady: prepared?.request === request,
    catalogError: failure?.request === request ? failure.message : '',
  };
}

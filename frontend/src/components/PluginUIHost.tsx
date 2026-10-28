import { createElement, Suspense, useEffect, useMemo, useRef, useState, type ComponentType } from 'react';
import { useLocation } from 'react-router-dom';
import { useQueryClient } from '@tanstack/react-query';
import { pluginUIQueryKey, type PluginUI } from '../lib/pluginUI';
import { importPluginModule } from '../lib/pluginUIModule';
import type { PluginActionRequest } from '../lib/plugins';
import { PluginHostContext, type PluginHostValue } from '../plugin-ui/context';
// Plugins load @ocman/plugin-ui by URL, which never attaches its CSS. Import
// it here so its styles ship with the app.
import '../plugin-ui';
import { ErrorBoundary, type FallbackRender } from './ErrorBoundary';
import { ErrorState } from './ErrorState';
import { LoadingState } from './LoadingState';
import { Button } from './Control';
import { PluginActionDialog } from './PluginActionDialog';

const modules = new Map<string, Promise<ComponentType>>();
// A failed import stays failed in the browser's module map, so a retry uses
// a new URL. Remember it per bundle so a later visit keeps the working URL.
const attempts = new Map<string, number>();

function bundleURL(base: string) {
  const attempt = attempts.get(base);
  return attempt ? `${base}&attempt=${attempt}` : base;
}

function loadModule(url: string) {
  let module = modules.get(url);
  if (!module) {
    module = importPluginModule(url);
    modules.set(url, module);
  }
  return module;
}

type Loaded = { url: string; Index?: ComponentType; error?: unknown };

function PluginIndex({ url, label }: { url: string; label: string }) {
  const [loaded, setLoaded] = useState<Loaded | null>(null);
  useEffect(() => {
    let live = true;
    loadModule(url).then((Index) => { if (live) setLoaded({ url, Index }); }, (error: unknown) => { if (live) setLoaded({ url, error }); });
    return () => { live = false; };
  }, [url]);
  if (loaded?.url !== url) return <LoadingState>Loading {label}…</LoadingState>;
  // Rethrow so the plugin's error boundary shows the failure.
  if (!loaded.Index) throw loaded.error instanceof Error ? loaded.error : new Error('The plugin UI bundle failed to load.');
  return createElement(loaded.Index);
}

type PendingAction = { label: string; request: PluginActionRequest; done: () => void };

/** Mounts one plugin's Index with a per-plugin Suspense and error boundary. */
export function PluginUIHost({ ui }: { ui: PluginUI & { index: NonNullable<PluginUI['index']> } }) {
  const location = useLocation();
  const client = useQueryClient();
  const [, setRetries] = useState(0);
  const [action, setAction] = useState<PendingAction | null>(null);
  const pending = useRef<PendingAction | null>(null);
  const url = bundleURL(ui.index.url);
  // An unmounted dialog can no longer close, so settle its caller.
  useEffect(() => () => { pending.current?.done(); pending.current = null; }, []);
  const host = useMemo<PluginHostValue>(() => ({
    pluginId: ui.pluginId,
    pluginLabel: ui.pluginLabel,
    ownerId: ui.ownerId,
    basePath: `/plugins/${ui.pluginId}`,
    runAction: (actionId, context = {}) => new Promise<void>((resolve, reject) => {
      const declared = ui.actions.find((a) => a.id === actionId);
      if (!declared) { reject(new Error(`Unknown plugin action: ${actionId}`)); return; }
      if (pending.current) { reject(new Error('Another plugin action is already open')); return; }
      pending.current = {
        label: declared.label,
        request: { pluginId: ui.pluginId, ownerId: ui.ownerId, actionId, operationId: crypto.randomUUID(), placement: declared.placement,
          surface: 'command-palette', context: { ...context, ownerId: ui.ownerId } },
        done: resolve,
      };
      setAction(pending.current);
    }),
  }), [ui.pluginId, ui.pluginLabel, ui.ownerId, ui.actions]);
  const fallback: FallbackRender = ({ reset }) => <ErrorState title="This plugin's interface failed"
    description={`${ui.pluginLabel} could not load or render. Its declarative views and settings are unaffected.`}>
    <Button onClick={() => {
      modules.delete(url);
      attempts.set(ui.index.url, (attempts.get(ui.index.url) ?? 0) + 1);
      setRetries((n) => n + 1);
      reset();
    }}>Try again</Button>
  </ErrorState>;
  return <PluginHostContext.Provider value={host}>
    <ErrorBoundary name={`plugin-ui:${ui.pluginId}`} resetKey={location.pathname} fallbackRender={fallback}>
      <Suspense fallback={<LoadingState>Loading {ui.pluginLabel}…</LoadingState>}>
        <PluginIndex url={url} label={ui.pluginLabel} />
      </Suspense>
    </ErrorBoundary>
    {action && <PluginActionDialog label={action.label} request={action.request} onClose={() => {
      action.done();
      pending.current = null;
      setAction(null);
      void client.invalidateQueries({ queryKey: pluginUIQueryKey(ui.pluginId) });
    }} />}
  </PluginHostContext.Provider>;
}

import { useEffect, useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import type { PluginActionHandle, PluginCallOptions, PluginContextValue } from '@ocman/plugin-ui';
import { callPluginUI, pluginHeaderTitle, pluginUIQueryKey, usePluginTitles } from '../lib/pluginUI';
import { usePageTitle as useDocumentTitle } from '../lib/headerContext';
import { formatExactDate as hostFormatExactDate } from '../lib/format';
export { useConfirm } from '../lib/useConfirm';
export { useUnsavedChangesGuard } from '../lib/useUnsavedChangesGuard';
import { usePluginHost } from './context';

export function usePluginContext(): PluginContextValue {
  const { pluginId, ownerId, basePath } = usePluginHost();
  return { pluginId, ownerId, basePath };
}

export function usePluginCall<T>(method: string, params?: unknown, options: PluginCallOptions = {}) {
  const { pluginId } = usePluginHost();
  return useQuery<T, Error>({
    queryKey: [...pluginUIQueryKey(pluginId), method, params ?? null],
    queryFn: ({ signal }) => callPluginUI<T>(pluginId, method, params, signal),
    enabled: options.enabled,
    refetchInterval: options.refetchInterval,
    retry: false,
  });
}

export function usePluginMutation<T, P = unknown>(method: string) {
  const { pluginId } = usePluginHost();
  const client = useQueryClient();
  return useMutation<T, Error, P>({
    mutationFn: (params: P) => callPluginUI<T>(pluginId, method, params),
    onSuccess: () => client.invalidateQueries({ queryKey: pluginUIQueryKey(pluginId) }),
  });
}

export function usePluginAction(actionId: string): PluginActionHandle {
  const { runAction } = usePluginHost();
  const [pending, setPending] = useState(false);
  return {
    pending,
    run: async (context) => {
      setPending(true);
      try { await runAction(actionId, context); } finally { setPending(false); }
    },
  };
}

export function usePageTitle(title: string) {
  const { pluginId, pluginLabel } = usePluginHost();
  const setTitle = usePluginTitles((state) => state.setTitle);
  useDocumentTitle(pluginHeaderTitle(pluginLabel, title));
  useEffect(() => {
    setTitle(pluginId, title);
    return () => setTitle(pluginId);
  }, [pluginId, title, setTitle]);
}

export function formatExactDate(value: string | number | Date) {
  return hostFormatExactDate(value);
}

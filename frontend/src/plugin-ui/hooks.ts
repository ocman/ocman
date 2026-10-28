import { useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import type { PluginActionHandle, PluginCallOptions, PluginContextValue } from '@ocman/plugin-ui';
import { callPluginUI, pluginUIQueryKey } from '../lib/pluginUI';
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

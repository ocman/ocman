import { useQuery, useQueryClient } from '@tanstack/react-query';
import { useState } from 'react';
import { api } from './api';

const HOOK_PLUGIN_KEY = ['hookPlugin'];

/** Shared state and install/uninstall action for the hook plugin (FR-6). */
export function useHookPlugin() {
  const client = useQueryClient();
  const query = useQuery({ queryKey: HOOK_PLUGIN_KEY, queryFn: ({ signal }) => api.hookPlugin(signal), staleTime: 60_000, retry: false });
  const [error, setError] = useState('');
  const set = async (install: boolean) => {
    setError('');
    try {
      client.setQueryData(HOOK_PLUGIN_KEY, await api.setHookPlugin(install));
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    }
  };
  return { status: query.data, error, set };
}

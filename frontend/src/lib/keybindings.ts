import { useQuery } from '@tanstack/react-query';
import { fetchJSON } from './api.requests';
import { queryClient } from './queryClient';
import type { KeyBinding } from './shortcutRegistry';

export type Keybindings = Record<string, KeyBinding[]>;
export const KEYBINDINGS_QUERY_KEY = ['keybindings'] as const;

// Every action shares one request and cache, including actions mounted later
// during navigation. There are deliberately no frontend fallback bindings.
export function useKeybindings() {
  return useQuery({
    queryKey: KEYBINDINGS_QUERY_KEY,
    queryFn: () => fetchJSON<Keybindings>('/api/keybindings'),
    staleTime: Infinity,
    gcTime: Infinity,
  }, queryClient);
}

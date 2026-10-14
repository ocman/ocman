import { useQuery } from '@tanstack/react-query';
import { api } from './api';

// Server reads behind the Settings pages. Mutations stay plain api calls
// followed by a refetch/invalidate of the matching key.

export function useRemotes() {
  return useQuery({
    queryKey: ['remotes'],
    // Single-host instances answer with an error: show no remotes.
    queryFn: ({ signal }) => api.listRemotes(signal).catch(() => []),
  });
}

export function useRemoteAccess() {
  return useQuery({
    queryKey: ['remote-access'],
    queryFn: ({ signal }) => api.remoteAccess(signal),
  });
}

export function useWebhookRelaySettings() {
  return useQuery({
    queryKey: ['webhook-relay'],
    queryFn: ({ signal }) => api.getWebhookRelay(signal),
  });
}

export function useWhisperStatus() {
  return useQuery({
    queryKey: ['whisper-status'],
    queryFn: ({ signal }) => api.whisperStatus(signal),
    // Machine state: one read per app load.
    staleTime: Infinity,
  });
}

export function useSharingSettings() {
  return useQuery({
    queryKey: ['sharing-settings'],
    queryFn: ({ signal }) => api.getSharingEnabled(signal),
  });
}

export function useHostCapabilities() {
  return useQuery({
    queryKey: ['capabilities', 'hosts'],
    queryFn: async ({ signal }) => (await api.capabilities(signal)).hosts ?? [],
  });
}

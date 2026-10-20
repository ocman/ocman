import { fetchJSON, queryString } from './api.requests';

export interface SessionIdentity {
  id: string;
  nativeId: string;
  platform: string;
  ownerId: string;
}

export function resolveLegacySession(nativeId: string, platform?: string, signal?: AbortSignal) {
  return fetchJSON<SessionIdentity>(`/api/session-identity${queryString({ nativeId, platform })}`, signal);
}

export function lookupSessionIdentity(id: string, platform?: string, signal?: AbortSignal) {
  return fetchJSON<SessionIdentity>(`/api/session-identity${queryString({ id, platform })}`, signal);
}

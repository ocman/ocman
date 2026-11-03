import { fetchJSON, postJSON } from './api.requests';

/** A selectable platform from GET /api/settings/platforms. */
export type PlatformOption = { id: string; label: string; setup?: boolean };

/** GET /api/settings/platforms. */
export type PlatformConfiguration = {
  enabled: string[];
  reloadPending?: boolean;
  overridden?: boolean;
  platforms?: PlatformOption[];
};

/** A platform's prerequisite status (hostsvc.PlatformSetup). */
export type PlatformSetup = {
  ready: boolean;
  installable: boolean;
  command?: string;
  notes?: string[];
  checks: { id: string; label: string; ok: boolean; detail: string; hint?: string }[];
};

export const platformSettingsEndpoint = '/api/settings/platforms';

const setupPath = (id: string, action: 'setup' | 'install') =>
  `${platformSettingsEndpoint}/${encodeURIComponent(id)}/${action}`;

export function checkPlatformSetup(id: string) {
  return fetchJSON<PlatformSetup>(setupPath(id, 'setup'));
}

export function installPlatformSetup(id: string) {
  return postJSON<PlatformSetup>(setupPath(id, 'install'), {});
}

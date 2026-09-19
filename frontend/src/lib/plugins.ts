import { fetchJSON, postJSON } from './api';

export interface PluginSetting {
  key: string;
  label: string;
  type: 'string' | 'boolean' | 'number' | 'integer';
  secret?: boolean;
  required?: boolean;
  default?: string | boolean | number;
  enum?: string[];
}

export interface PluginRegistration {
  approval: string;
  ownerId: string;
  description: {
    id: string;
    name: string;
    version: string;
    scope: 'hub' | 'owner';
    capabilities?: { name: string; version: { major: number; minor: number } }[];
    requestedGrants?: string[];
    settings?: PluginSetting[];
  };
  checksum: string;
  enabled: boolean;
  removed: boolean;
  grants: string[] | null;
  configuration: {
    values: Record<string, string | boolean | number> | null;
    secrets: Record<string, boolean> | null;
  };
  health: { status: string; restartCount: number; lastError?: string };
}

export interface PluginInput {
  approval?: string;
  grants?: string[];
  values?: Record<string, string | boolean | number>;
  secrets?: Record<string, string>;
}

export type PluginMutation = 'enable' | 'disable' | 'retry' | 'restart' | 'grants' | 'configuration' | 'remove-data';

function url(owner: string, path = '') {
  return `/api/plugins${path}?ownerId=${encodeURIComponent(owner)}`;
}

export const plugins = {
  discovery: (owner: string, signal?: AbortSignal) => fetchJSON<{ filename: string; error: string }[]>(url(owner, '/discovery'), signal),
  list: (owner: string, signal?: AbortSignal) => fetchJSON<PluginRegistration[] | null>(url(owner), signal),
  rescan: (owner: string) => postJSON<PluginRegistration[] | null>(url(owner, '/rescan'), {}),
  mutate: (owner: string, id: string, action: PluginMutation, input: PluginInput = {}) =>
    postJSON<unknown>(url(owner, `/${encodeURIComponent(id)}/${action}`), input),
  stderr: (owner: string, id: string) => fetchJSON<{ stderr: string }>(url(owner, `/${encodeURIComponent(id)}/stderr`)),
};

export interface PluginActionContext {
  ownerId: string;
  projectId?: string;
  sessionId?: string;
  route?: string;
}

export interface PluginAction {
  pluginId: string;
  ownerId: string;
  scope: 'hub' | 'owner';
  action: {
    id: string;
    label: string;
    placement: 'global' | 'project' | 'session';
    confirmation?: string;
  };
}

export interface PluginActionRequest {
  ownerId: string;
  pluginId: string;
  actionId: string;
  operationId: string;
  placement: PluginAction['action']['placement'];
  surface: 'command-palette';
  context: PluginActionContext;
  confirmationToken?: string;
}

export type PluginActionResult =
  | { kind: 'notice'; text: string }
  | { kind: 'link'; label: string; url: string }
  | { kind: 'artifact'; label: string; handle: string }
  | { kind: 'navigation'; target: 'sessions' | 'projects' | 'settings' | 'inbox' | 'routines' }
  | { kind: 'refresh'; target: 'actions' | 'projects' | 'sessions' };

export interface PluginActionResponse {
  results?: PluginActionResult[];
  confirmation?: { text: string; token: string; expiresAt: number };
  error?: { category: string };
}

export const pluginActions = {
  list: (placement: PluginActionRequest['placement'], context: PluginActionContext, signal?: AbortSignal) => {
    const params = new URLSearchParams({ placement, surface: 'command-palette' });
    for (const [key, value] of Object.entries(context)) {
      if (value) params.set(key, value);
    }
    return fetchJSON<PluginAction[] | null>(`/api/plugins/actions?${params}`, signal);
  },
  invoke: (request: PluginActionRequest, signal?: AbortSignal) =>
    postJSON<PluginActionResponse>('/api/plugins/actions/invoke', request, { signal, acceptStatus: 409 }),
  artifactURL: (ownerId: string, handle: string) =>
    `/api/plugins/actions/artifact?${new URLSearchParams({ ownerId, handle })}`,
};

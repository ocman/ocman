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

/** Conversation plugins are the only ones that owe replies to a provider. */
export function hasConversationCapability(plugin: PluginRegistration) {
  return (plugin.description.capabilities ?? []).some((c) => c.name === 'conversation');
}

export interface PluginInput {
  approval?: string;
  grants?: string[];
  values?: Record<string, string | boolean | number>;
  secrets?: Record<string, string>;
  deliveryId?: number;
}

export type PluginMutation =
  | 'enable' | 'disable' | 'retry' | 'restart' | 'grants' | 'configuration' | 'remove-data'
  | 'conversations/retry' | 'conversations/discard';

/** One completed reply that exhausted its retries and needs a decision. */
export interface PluginDeadLetter {
  id: number;
  accountId: string;
  threadId: string;
  attempts: number;
  lastError: string;
  updatedAt: number;
  bytes: number;
}

/**
 * Delivery backlog for a conversation plugin: undelivered replies, the limits
 * that pause new work, and the dead letters awaiting a retry or a discard.
 */
export interface PluginBacklog {
  pending: number;
  dead: number;
  bytes: number;
  retrying: number;
  paused: boolean;
  maxRows: number;
  maxBytes: number;
  oldestUnsent: number;
  deadLetters?: PluginDeadLetter[] | null;
}

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
  projectCatalog: (owner: string, directory: string, signal?: AbortSignal) =>
    postJSON<{ agents: string[]; models: string[] }>(url(owner, '/project-catalog'), { directory }, { signal }),
  backlog: (owner: string, id: string, signal?: AbortSignal) =>
    fetchJSON<PluginBacklog>(url(owner, `/${encodeURIComponent(id)}/conversations`), signal),
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

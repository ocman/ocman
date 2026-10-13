import type { fetchJSON, postJSON } from './api';
import type { ModelFallthroughSettings, SharingSettings, WebhookRelaySettings } from './api.types';

export type JudgeEndpointSettings = {
  format: '' | 'openai' | 'typesafe';
  endpoint: string;
  model: string;
  minSafeProbability: number;
  apiKeySet: boolean;
};

export function settingsApi(get: typeof fetchJSON, post: typeof postJSON) {
  return {
    getJudgeEndpoint: (signal?: AbortSignal) =>
      get<JudgeEndpointSettings>('/api/settings/judge-endpoint', signal),
    setJudgeEndpoint: (input: Omit<JudgeEndpointSettings, 'apiKeySet'> & { apiKey?: string }) =>
      post<JudgeEndpointSettings>('/api/settings/judge-endpoint', input),
    getSharingEnabled: (signal?: AbortSignal) =>
      get<SharingSettings>('/api/settings/sharing', signal),
    setSharingEnabled: (enabled: boolean) =>
      post<SharingSettings>('/api/settings/sharing', { enabled }),
    getWebhookRelay: (signal?: AbortSignal) =>
      get<WebhookRelaySettings>('/api/settings/webhook-relay', signal),
    setWebhookRelay: (input: { relayUrl?: string; enrollmentToken?: string }) =>
      post<WebhookRelaySettings>('/api/settings/webhook-relay', input),
    getWorktreeInheritPermissions: (signal?: AbortSignal) =>
      get<{ enabled: boolean }>('/api/settings/worktree-inherit-permissions', signal),
    setWorktreeInheritPermissions: (enabled: boolean) =>
      post<{ enabled: boolean }>('/api/settings/worktree-inherit-permissions', { enabled }),
    getDefaultPlatform: (signal?: AbortSignal) =>
      get<{ platform: string }>('/api/settings/default-platform', signal),
    setDefaultPlatform: (platform: string) =>
      post<{ platform: string }>('/api/settings/default-platform', { platform }),
    getAutoArchiveSettings: (signal?: AbortSignal) =>
      get<{ enabled: boolean; ttlDays: number }>('/api/settings/auto-archive', signal),
    setAutoArchiveSettings: (settings: { enabled: boolean; ttlDays: number }) =>
      post<{ enabled: boolean; ttlDays: number }>('/api/settings/auto-archive', settings),
    getModelFallthroughSettings: (signal?: AbortSignal) =>
      get<ModelFallthroughSettings>('/api/settings/model-fallthrough', signal),
    setModelFallthroughSettings: (settings: ModelFallthroughSettings) =>
      post<ModelFallthroughSettings>('/api/settings/model-fallthrough', settings),
    getArchiveResurface: (signal?: AbortSignal) =>
      get<{ mode: string }>('/api/settings/archive-resurface', signal),
    setArchiveResurface: (mode: string) =>
      post<{ mode: string }>('/api/settings/archive-resurface', { mode }),
  };
}

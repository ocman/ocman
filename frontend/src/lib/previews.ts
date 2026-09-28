import { createContext } from 'react';
import { fetchJSON, postJSON } from './api';

/** Owner (`remoteId`) of the conversation whose links are previewed. */
export const PreviewOwnerContext = createContext('local');

/** Mirrors internal/linkpreview.State. */
export type PreviewState =
  | 'ok' | 'connect' | 'expired' | 'denied' | 'not_found' | 'rate_limited' | 'ambiguous' | 'error';

/** One provider resource (internal/linkpreview.Ref). */
export interface PreviewRef {
  provider: string;
  workspace?: string;
  kind: string;
  id: string;
  /** Canonical resource URL; absent for an unresolved ticket identifier. */
  url?: string;
}

/** Normalized preview result (internal/linkpreview.Preview). */
export interface PreviewResult extends PreviewRef {
  title?: string;
  status?: string;
  /** Bootstrap icon class, e.g. `bi-kanban`. */
  icon?: string;
  meta?: string[];
  updatedAt?: string;
  state: PreviewState;
  /** Cached data served while the provider is rate limited. */
  stale?: boolean;
  /** Candidates for an ambiguous ticket identifier (state `ambiguous`). */
  choices?: { title: string; url: string }[];
}

/** One saved token or grant: display names only (internal/previewauth.Connection). */
export interface PreviewConnection {
  workspaceId: string;
  workspaceName: string;
  accountName: string;
  sites: { id: string; name: string }[];
  state: 'connected' | 'expired';
}

/** A supported provider (internal/server.previewProviderView). */
export interface PreviewProvider {
  id: string;
  name: string;
  /** Link hosts: exact, or `*.example.com` for any subdomain. */
  hosts: string[];
  /** Links on these hosts are looked up; others are never sent. */
  configured: boolean;
  /** Machine credential used without a saved token: CLI login or anonymous. */
  source?: 'cli' | 'public';
  /** A personal token can be pasted. */
  token: boolean;
  /** A sign-in app is configured. */
  oauth: boolean;
  notice?: string;
  tokenHelp?: string;
  accounts: PreviewConnection[];
}

/** A provider whose hosts are added with a token (Forgejo, GitLab). */
export interface PreviewHostKind {
  kind: string;
  name: string;
  help: string;
}

export interface PreviewConfig {
  providers: PreviewProvider[];
  /** Custom-rule patterns routed to a configured provider. */
  rules: string[];
  hostKinds: PreviewHostKind[];
}

/** A kind of sign-in app ocman can register (internal/server.previewAppKind). */
export interface PreviewAppKind {
  kind: string;
  name: string;
  /** One app per host (Forgejo, GitLab). */
  hosted?: boolean;
  secretOptional?: boolean;
  /** Environment variable prefix, or the host list variable for hosted kinds. */
  env: string;
}

/** A configured sign-in app. Secrets are write-only and never returned. */
export interface PreviewApp {
  id: string;
  kind: string;
  host?: string;
  clientId: string;
  hasSecret: boolean;
  source: 'env' | 'settings';
  /** The environment also defines it: removing the saved app falls back to it. */
  inEnv: boolean;
}

export interface PreviewApps {
  kinds: PreviewAppKind[];
  apps: PreviewApp[];
  /** The redirect URI to register with every provider. */
  callbackUrl: string;
}

export interface PreviewAppInput {
  kind: string;
  host?: string;
  clientId: string;
  /** Empty keeps the saved secret. */
  clientSecret?: string;
}

/** Fired after a token, grant or app changes: previews reload. */
export const PREVIEW_AUTH_EVENT = 'ocman:preview-auth-changed';

const q = (remoteId: string) => `?remoteId=${encodeURIComponent(remoteId)}`;

const configCache = new Map<string, Promise<PreviewConfig>>();

/** Forgets the cached configuration and tells mounted previews to reload. */
export function previewAuthChanged(): void {
  configCache.clear();
  window.dispatchEvent(new Event(PREVIEW_AUTH_EVENT));
}

export function loadPreviewConfig(remoteId = 'local'): Promise<PreviewConfig> {
  let p = configCache.get(remoteId);
  if (!p) {
    p = fetchJSON<Partial<PreviewConfig>>(`/api/previews/providers${q(remoteId)}`)
      .then((r) => ({ providers: r.providers ?? [], rules: r.rules ?? [], hostKinds: r.hostKinds ?? [] }));
    p.catch(() => configCache.delete(remoteId));
    configCache.set(remoteId, p);
  }
  return p;
}

const hostMatches = (host: string, pattern: string) => pattern.startsWith('*.')
  ? host.endsWith(pattern.slice(1))
  : host === pattern;

/**
 * Whether `text` can produce a preview: a link on a configured provider's
 * host, or a match for a routed rule. Anything else is never sent.
 */
export function mayPreview(text: string, config: PreviewConfig): boolean {
  const hosts = config.providers.filter((p) => p.configured).flatMap((p) => p.hosts);
  for (const [raw] of text.matchAll(/https:\/\/[^\s<>()"'`]+/g)) {
    let host: string;
    try { host = new URL(raw).host.toLowerCase(); } catch { continue; }
    if (hosts.some((h) => hostMatches(host, h))) return true;
  }
  return config.rules.some((pattern) => {
    // Go and JS regex syntax overlap; an untranslatable pattern asks the server.
    try { return new RegExp(pattern).test(text); } catch { return true; }
  });
}

/** Checks a personal token with the provider and saves it for this machine. */
export async function savePreviewToken(provider: string, token: string): Promise<void> {
  await postJSON('/api/previews/token', { provider, token });
  previewAuthChanged();
}

/** Starts OAuth consent in this tab; the provider redirects back here. */
export async function connectPreviewProvider(provider: string): Promise<void> {
  const returnTo = window.location.pathname + window.location.search;
  const { authorizeUrl } = await postJSON<{ authorizeUrl: string }>('/api/previews/connect', { provider, returnTo });
  window.location.assign(authorizeUrl);
}

/** Removes a saved token or grant (all workspaces when none is given). */
export async function disconnectPreviewProvider(provider: string, workspaceId?: string): Promise<void> {
  await postJSON('/api/previews/disconnect', { provider, workspaceId });
  previewAuthChanged();
}

/** Sign-in apps are configured on this ocman (the hub), never per remote. */
export function loadPreviewApps(): Promise<PreviewApps> {
  return fetchJSON<PreviewApps>('/api/previews/apps');
}

export async function savePreviewApp(app: PreviewAppInput): Promise<PreviewApps> {
  const r = await postJSON<PreviewApps>('/api/previews/apps/save', app);
  previewAuthChanged();
  return r;
}

export async function removePreviewApp(id: string): Promise<PreviewApps> {
  const r = await postJSON<PreviewApps>('/api/previews/apps/remove', { id });
  previewAuthChanged();
  return r;
}

/** Discovers and resolves previews in `text`; the server owns discovery and credentials. */
export async function resolvePreviews(text: string, remoteId = 'local', signal?: AbortSignal): Promise<PreviewResult[]> {
  const { previews } = await postJSON<{ previews: PreviewResult[] }>(`/api/previews/resolve${q(remoteId)}`, { text }, { signal });
  return previews;
}

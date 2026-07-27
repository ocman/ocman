// Wire types and helpers for the PR/Issue sidebar feature.
// Backend contract: see spec/pr-issue-sidebar/architecture.md (API design).

import { fetchJSON, postJSON, raiseAuthError } from './api';

export type RemoteType = 'github' | 'forgejo';

export interface Upstream {
  remote: string;
  host: string;
  type: RemoteType;
  repo: string;
  url?: string;
}

export interface Label {
  name: string;
  color?: string;
}

export interface ForgeUser {
  login: string;
  avatarUrl?: string;
}

export interface PR {
  number: number;
  title: string;
  body: string;
  author: string;
  status: 'open' | 'draft' | 'merged' | 'closed';
  updatedAt: string; // ISO timestamp
  labels: Label[] | null;
  assignees: ForgeUser[] | null;
  requestedReviewers: ForgeUser[] | null;
  branch: string;
  url: string;
  host: string;
  repo: string;
  headSha?: string;
  crossFork: boolean;
}

export type CIState = 'unknown' | 'pending' | 'success' | 'failure';

export interface Check {
  name: string;
  state: CIState;
  url?: string;
}

export interface PRChecks {
  state: CIState;
  checks: Check[];
  rateLimit?: RateLimit;
}

export interface Issue {
  number: number;
  title: string;
  body: string;
  author: string;
  status: 'open' | 'closed';
  updatedAt: string;
  labels: Label[] | null;
  assignees: ForgeUser[] | null;
  url: string;
  host: string;
  repo: string;
}

export interface Pagination {
  page: number;
  hasMore: boolean;
}

export interface RateLimit {
  limited: boolean;
  resetAt?: string;
}

export type StateFilter = 'open' | 'closed' | 'all';

export interface ListPRsResponse {
  prs: PR[];
  pagination: Pagination;
  rateLimit: RateLimit;
}

export interface ListIssuesResponse {
  issues: Issue[];
  pagination: Pagination;
  rateLimit: RateLimit;
}

export interface ErrorEnvelope {
  error: {
    code: string;
    message: string;
    status?: number;
    retryAfter?: string;
    fetchTarget?: string;
  };
}

// HandleRequest is the body of POST /api/project/handle.
export interface HandleRequest {
  dir: string;
  remote: string;
  type: 'pr' | 'issue';
  number: number;
  mode: 'session' | 'worktree';
  action?: 'handle' | 'review';
  fetchHead?: boolean;
  intent?: string;
}

export interface HandleResponse {
  childSessionId: string;
  mode: 'session' | 'worktree';
  worktreePath?: string;
  branch?: string;
  tmuxTarget?: string;
}

// Settings (prompt templates).
export interface PromptTemplates {
  pr: string;
  issue: string;
  review: string;
}

// fetchUpstreams returns the supported forge remotes for the project
// containing dir. Returns [] when the directory has no recognised
// upstream — the caller hides the pane in that case.
export async function fetchUpstreams(dir: string, signal?: AbortSignal): Promise<Upstream[]> {
  const url = `/api/project/upstreams?dir=${encodeURIComponent(dir)}`;
  const resp = await fetch(url, { signal });
  if (!resp.ok) {
    // 404 = not a git repo; treat as "no upstreams" rather than an error.
    if (resp.status === 404) return [];
    if (resp.status === 401) throw raiseAuthError();
    throw new Error(`upstreams: ${resp.status}`);
  }
  const json = (await resp.json()) as { upstreams: Upstream[] };
  return json.upstreams ?? [];
}

export async function fetchPRs(opts: {
  dir: string;
  remote: string;
  state: StateFilter;
  mine: string | undefined;
  page: number;
  signal?: AbortSignal;
}): Promise<ListPRsResponse> {
  const q = new URLSearchParams({
    dir: opts.dir,
    remote: opts.remote,
    state: opts.state,
    page: String(opts.page),
  });
  if (opts.mine) q.set('mine', opts.mine);
  const resp = await fetch(`/api/project/prs?${q.toString()}`, { signal: opts.signal });
  if (!resp.ok) {
    const env = await safeError(resp);
    if (sessionExpired(resp, env)) throw raiseAuthError();
    throw new UpstreamApiError(env, resp.status);
  }
  return (await resp.json()) as ListPRsResponse;
}

export async function fetchIssues(opts: {
  dir: string;
  remote: string;
  state: StateFilter;
  mine: string | undefined;
  page: number;
  signal?: AbortSignal;
}): Promise<ListIssuesResponse> {
  const q = new URLSearchParams({
    dir: opts.dir,
    remote: opts.remote,
    state: opts.state,
    page: String(opts.page),
  });
  if (opts.mine) q.set('mine', opts.mine);
  const resp = await fetch(`/api/project/issues?${q.toString()}`, { signal: opts.signal });
  if (!resp.ok) {
    const env = await safeError(resp);
    if (sessionExpired(resp, env)) throw raiseAuthError();
    throw new UpstreamApiError(env, resp.status);
  }
  return (await resp.json()) as ListIssuesResponse;
}

// fetchPRChecks returns the combined CI/build status for a PR's head
// commit. Fetched lazily (on expand/hover) so the list stays cheap.
export async function fetchPRChecks(opts: {
  dir: string;
  remote: string;
  sha: string;
  signal?: AbortSignal;
}): Promise<PRChecks> {
  const q = new URLSearchParams({
    dir: opts.dir,
    remote: opts.remote,
    sha: opts.sha,
  });
  const resp = await fetch(`/api/project/pr-checks?${q.toString()}`, { signal: opts.signal });
  if (!resp.ok) {
    const env = await safeError(resp);
    if (sessionExpired(resp, env)) throw raiseAuthError();
    throw new UpstreamApiError(env, resp.status);
  }
  return (await resp.json()) as PRChecks;
}

export async function fetchForgeUser(opts: {
  dir: string;
  remote: string;
  signal?: AbortSignal;
}): Promise<{ login: string; host: string } | null> {
  const q = new URLSearchParams({ dir: opts.dir, remote: opts.remote });
  const resp = await fetch(`/api/project/forge-user?${q.toString()}`, { signal: opts.signal });
  if (resp.status === 401) {
    // A forge-level 401 always carries an error envelope; a bare 401 is
    // ocman's own auth middleware telling us the cookie expired.
    if (sessionExpired(resp, await safeError(resp))) throw raiseAuthError();
    return null; // unauthenticated — disable "mine" for this remote
  }
  if (!resp.ok) throw new Error(`forge-user: ${resp.status}`);
  return (await resp.json()) as { login: string; host: string };
}

export async function postHandle(req: HandleRequest): Promise<HandleResponse> {
  const resp = await fetch('/api/project/handle', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(req),
  });
  if (!resp.ok) {
    const env = await safeError(resp);
    if (sessionExpired(resp, env)) throw raiseAuthError();
    throw new UpstreamApiError(env, resp.status);
  }
  return (await resp.json()) as HandleResponse;
}

export async function fetchPromptTemplates(): Promise<PromptTemplates> {
  return fetchJSON<PromptTemplates>('/api/settings/prompt-templates');
}

export async function savePromptTemplates(t: Partial<PromptTemplates>): Promise<PromptTemplates> {
  return postJSON<PromptTemplates, Partial<PromptTemplates>>('/api/settings/prompt-templates', t);
}

// UpstreamApiError carries the structured error envelope returned by
// the backend on 4xx/5xx. Consumers use `.envelope.error.code` to
// decide whether to render a rate-limit banner vs an auth banner vs
// a generic error.
export class UpstreamApiError extends Error {
  envelope: ErrorEnvelope | null;
  status: number;
  constructor(envelope: ErrorEnvelope | null, status: number) {
    super(envelope?.error?.message ?? `request failed: ${status}`);
    this.name = 'UpstreamApiError';
    this.envelope = envelope;
    this.status = status;
  }
}

// sessionExpired distinguishes "your ocman cookie expired" from "you
// have no GITHUB_TOKEN". Both are 401, but every forge-level 401 from
// these handlers carries a JSON error envelope, while ocman's auth
// middleware answers with a bare "unauthorized" body. Only the latter
// should flip the whole app to the lockscreen.
function sessionExpired(resp: Response, envelope: ErrorEnvelope | null): boolean {
  return resp.status === 401 && envelope === null;
}

async function safeError(resp: Response): Promise<ErrorEnvelope | null> {
  try {
    const body = await resp.json();
    if (body && typeof body === 'object' && 'error' in body) {
      return body as ErrorEnvelope;
    }
  } catch {
    // body not JSON — return null so the caller falls back to the status
  }
  return null;
}

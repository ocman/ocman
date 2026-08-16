/**
 * Forge URL preview utilities (GitHub + Forgejo/Gitea).
 *
 * Fetches metadata via the ocman backend:
 *   - /api/integrations/github/preview  (GitHub, fixed host)
 *   - /api/integrations/forgejo/preview (Forgejo/Gitea, dynamic hosts)
 * The backend discovers and injects the appropriate token server-side, so
 * previews work for both public and private repos.
 *
 * GitHub's host is fixed (github.com); Forgejo hosts are self-hosted and
 * therefore discovered at runtime from /api/integrations/status.
 */

import { raiseForUnauthorized } from './api';

export type GitHubPreviewKind = 'pr' | 'issue' | 'commit';

export interface GitHubPreviewRef {
  kind: GitHubPreviewKind;
  owner: string;
  repo: string;
  number?: number; // PR or issue
  sha?: string;    // commit
}

export interface GitHubPreviewData {
  kind: GitHubPreviewKind;
  title: string;
  /** Human-readable state label, e.g. "Open", "Closed", "Merged" */
  state: string;
  /** Icon class from Bootstrap Icons */
  stateIcon: string;
  /** CSS class suffix for colour: "open" | "merged" | "closed" | "commit" */
  stateClass: string;
  author: string;
  authorAvatar: string;
  repo: string;
  url: string;
  /** Short SHA (7 chars) for commits */
  shortSha?: string;
  /** ISO date string */
  updatedAt: string;
}

// ---------------------------------------------------------------------------
// URL parsing (frontend — used to decide whether to show a preview strip)
// ---------------------------------------------------------------------------

const GITHUB_PR_RE = /^https?:\/\/github\.com\/([^/]+)\/([^/]+)\/pull\/(\d+)/;
const GITHUB_ISSUE_RE = /^https?:\/\/github\.com\/([^/]+)\/([^/]+)\/issues\/(\d+)/;
const GITHUB_COMMIT_RE = /^https?:\/\/github\.com\/([^/]+)\/([^/]+)\/commit\/([0-9a-f]{5,40})/;

export function parseGitHubUrl(url: string): GitHubPreviewRef | null {
  let m: RegExpMatchArray | null;

  m = url.match(GITHUB_PR_RE);
  if (m) return { kind: 'pr', owner: m[1], repo: m[2], number: parseInt(m[3], 10) };

  m = url.match(GITHUB_ISSUE_RE);
  if (m) return { kind: 'issue', owner: m[1], repo: m[2], number: parseInt(m[3], 10) };

  m = url.match(GITHUB_COMMIT_RE);
  if (m) return { kind: 'commit', owner: m[1], repo: m[2], sha: m[3] };

  return null;
}

// ---------------------------------------------------------------------------
// Extract all previewable GitHub URLs from a text string (deduplicated,
// preserving first-occurrence order).
// ---------------------------------------------------------------------------

const GITHUB_URL_SCAN_RE = /https?:\/\/github\.com\/[^\s<>"')]+/g;

export function extractGitHubUrls(text: string): string[] {
  const seen = new Set<string>();
  const out: string[] = [];
  for (const raw of text.matchAll(GITHUB_URL_SCAN_RE)) {
    const url = raw[0].replace(/[.,;:!?]+$/, ''); // strip trailing punctuation
    if (!seen.has(url) && parseGitHubUrl(url)) {
      seen.add(url);
      out.push(url);
    }
  }
  return out;
}

// ---------------------------------------------------------------------------
// Backend proxy fetch + shape mapping
// ---------------------------------------------------------------------------

async function fetchFromBackend(url: string): Promise<GitHubPreviewData> {
  const res = await fetch(
    `/api/integrations/github/preview?url=${encodeURIComponent(url)}`,
  );
  // Every caller below swallows the throw (a dead preview is not worth a
  // crash), but the guard's real job is the AuthError fan-out: without it
  // an expired cookie fails silently instead of showing the lockscreen.
  await raiseForUnauthorized(res);
  if (!res.ok) throw new Error(`preview proxy ${res.status}`);

  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  const raw: any = await res.json();
  return mapRawToPreviewData(url, raw);
}

// mapRawToPreviewData converts a forge API JSON payload into the preview
// shape. `ref` may be supplied by the caller (Forgejo URLs don't match the
// GitHub parser); when omitted it is derived from the GitHub URL.
// eslint-disable-next-line @typescript-eslint/no-explicit-any
function mapRawToPreviewData(url: string, raw: any, ref?: GitHubPreviewRef): GitHubPreviewData {
  ref = ref ?? parseGitHubUrl(url)!;
  const repoFull = `${ref.owner}/${ref.repo}`;

  if (ref.kind === 'pr' || raw.pull_request !== undefined) {
    const merged = raw.merged === true || raw.merged_at != null || raw.pull_request?.merged_at != null;
    const stateClass = merged ? 'merged' : raw.state === 'closed' ? 'closed' : 'open';
    const stateLabel = merged ? 'Merged' : raw.state === 'closed' ? 'Closed' : 'Open';
    const stateIcon = merged ? 'bi-git' : raw.state === 'closed' ? 'bi-x-circle' : 'bi-circle';
    return {
      kind: 'pr',
      title: raw.title ? `#${ref.number} ${raw.title}` : `${repoFull} #${ref.number}`,
      state: stateLabel,
      stateIcon,
      stateClass,
      author: raw.user?.login ?? '',
      authorAvatar: raw.user?.avatar_url ?? '',
      repo: repoFull,
      url: raw.html_url ?? url,
      updatedAt: raw.updated_at ?? '',
    };
  }

  if (ref.kind === 'issue') {
    const stateClass = raw.state === 'closed' ? 'closed' : 'open';
    const stateLabel = raw.state === 'closed' ? 'Closed' : 'Open';
    const stateIcon = raw.state === 'closed' ? 'bi-check-circle' : 'bi-circle';
    return {
      kind: 'issue',
      title: raw.title ?? `${repoFull} #${ref.number}`,
      state: stateLabel,
      stateIcon,
      stateClass,
      author: raw.user?.login ?? '',
      authorAvatar: raw.user?.avatar_url ?? '',
      repo: repoFull,
      url: raw.html_url ?? url,
      updatedAt: raw.updated_at ?? '',
    };
  }

  // commit
  const msg = raw.commit?.message?.split('\n')[0] ?? ref.sha?.slice(0, 7) ?? '';
  return {
    kind: 'commit',
    title: msg,
    state: 'Commit',
    stateIcon: 'bi-braces',
    stateClass: 'commit',
    author: raw.author?.login ?? raw.commit?.author?.name ?? '',
    authorAvatar: raw.author?.avatar_url ?? '',
    repo: repoFull,
    url: raw.html_url ?? url,
    shortSha: ref.sha?.slice(0, 7),
    updatedAt: raw.commit?.author?.date ?? '',
  };
}

// ---------------------------------------------------------------------------
// In-memory cache (FR-11)
//
// One entry per normalized resource key (origin + kind + repo + number/sha),
// so distinct forge hosts and resources never collide while `/pull/1` and
// `/pull/1/files` share one entry. Rules:
//
//   - concurrent loads/refreshes join the entry's single in-flight promise,
//     so N visible cards for one URL cause one backend request;
//   - a completed attempt (success *or* failure) is "fresh" for
//     PREVIEW_FRESH_MS, below the cards' 5 s cadence, so a revalidation cycle
//     issues one request and an error backs off instead of hammering;
//   - a failure never replaces a previous success: the stale success keeps
//     rendering and the next cycle retries;
//   - the map is capped at PREVIEW_MAX_ENTRIES with oldest-success eviction.
// ---------------------------------------------------------------------------

/** Window during which a completed attempt is reused instead of refetched. */
export const PREVIEW_FRESH_MS = 4_000;
/** Hard cap on cache entries; the oldest success is evicted past it. */
export const PREVIEW_MAX_ENTRIES = 100;

interface PreviewEntry {
  /** Last successful preview, kept across later failures. */
  data?: GitHubPreviewData;
  /** Completion time of the last attempt; 0 while none has completed. */
  ts: number;
  inflight?: Promise<GitHubPreviewData | null>;
}

const cache = new Map<string, PreviewEntry>();

function previewKey(url: string, ref: GitHubPreviewRef): string {
  let origin = url;
  try {
    origin = new URL(url).origin;
  } catch {
    // Not parseable as an absolute URL; the raw string still isolates hosts.
  }
  return `${origin}|${ref.kind}|${ref.owner}/${ref.repo}|${ref.number ?? ref.sha}`;
}

function isFresh(e: PreviewEntry, now: number): boolean {
  return e.ts !== 0 && now - e.ts < PREVIEW_FRESH_MS;
}

function evictOldest(): void {
  while (cache.size > PREVIEW_MAX_ENTRIES) {
    // Map iterates in insertion order and a success re-inserts, so the first
    // evictable key is the oldest success.
    const victim = [...cache].find(([, e]) => !e.inflight)?.[0];
    if (victim === undefined) return;
    cache.delete(victim);
  }
}

// request starts (or joins) the single in-flight fetch for `key`.
function request(
  key: string,
  fetcher: () => Promise<GitHubPreviewData>,
): Promise<GitHubPreviewData | null> {
  const existing = cache.get(key);
  if (existing?.inflight) return existing.inflight;

  const entry: PreviewEntry = existing ?? { ts: 0 };
  // Self-referenced inside the callbacks below; they only run after
  // initialization, so the binding is always set by then.
  const inflight: Promise<GitHubPreviewData | null> = fetcher()
    .then((data) => {
      // Re-insert so insertion order tracks success recency.
      cache.delete(key);
      cache.set(key, { ...entry, data, ts: Date.now(), inflight });
      return data;
    })
    .catch(() => {
      // Keep the previous success; only mark the attempt time so the next
      // cycle retries instead of retrying immediately.
      entry.ts = Date.now();
      return entry.data ?? null;
    })
    .finally(() => {
      const cur = cache.get(key);
      if (cur?.inflight === inflight) delete cur.inflight;
      evictOldest();
    });

  entry.inflight = inflight;
  cache.set(key, entry);
  return inflight;
}

// loadPreview serves any cached success immediately (page-lifetime behaviour),
// otherwise joins/starts one request.
function loadPreview(
  key: string,
  fetcher: () => Promise<GitHubPreviewData>,
): Promise<GitHubPreviewData | null> {
  const e = cache.get(key);
  if (e?.data) return Promise.resolve(e.data);
  if (e?.inflight) return e.inflight;
  if (e && isFresh(e, Date.now())) return Promise.resolve(null); // recent failure
  return request(key, fetcher);
}

// refreshPreview revalidates, unless the last attempt is still inside the
// freshness window or a request is already in flight.
function refreshPreview(
  key: string,
  fetcher: () => Promise<GitHubPreviewData>,
): Promise<GitHubPreviewData | null> {
  const e = cache.get(key);
  if (e?.inflight) return e.inflight;
  if (e && isFresh(e, Date.now())) return Promise.resolve(e.data ?? null);
  return request(key, fetcher);
}

export function cachedGitHubPreview(url: string): Promise<GitHubPreviewData | null> {
  const ref = parseGitHubUrl(url);
  if (!ref) return Promise.resolve(null);
  return loadPreview(previewKey(url, ref), () => fetchFromBackend(url));
}

/**
 * Revalidates the preview for `url`. Concurrent callers share one request and
 * a failure leaves the last success in place, so a card keeps rendering.
 */
export function refreshGitHubPreview(url: string): Promise<GitHubPreviewData | null> {
  const ref = parseGitHubUrl(url);
  if (!ref) return Promise.resolve(null);
  return refreshPreview(previewKey(url, ref), () => fetchFromBackend(url));
}

// ===========================================================================
// Forgejo / Gitea
//
// Same preview shape as GitHub, but the host is dynamic. The set of
// previewable hosts is provided by the caller (loaded from
// /api/integrations/status). Forgejo's web UI uses the plural "/pulls/" path
// for pull requests (the API uses the same), unlike GitHub's "/pull/".
// ===========================================================================

// escapeRegExp escapes a hostname for safe interpolation into a RegExp.
function escapeRegExp(s: string): string {
  return s.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
}

// ---------------------------------------------------------------------------
// Forgejo host discovery
//
// The previewable Forgejo hosts come from /api/integrations/status. The
// result is cached for the page lifetime (hosts are fixed at server startup);
// concurrent callers share one in-flight request.
// ---------------------------------------------------------------------------

let forgejoHostsCache: string[] | null = null;
let forgejoHostsInflight: Promise<string[]> | null = null;

export async function loadForgejoHosts(): Promise<string[]> {
  if (forgejoHostsCache) return forgejoHostsCache;
  if (forgejoHostsInflight) return forgejoHostsInflight;
  forgejoHostsInflight = (async () => {
    try {
      const res = await fetch('/api/integrations/status');
      await raiseForUnauthorized(res);
      if (!res.ok) return [];
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
      const raw: any = await res.json();
      const hosts: string[] = Array.isArray(raw?.forgejo?.hosts) ? raw.forgejo.hosts : [];
      forgejoHostsCache = hosts;
      return hosts;
    } catch {
      return [];
    } finally {
      forgejoHostsInflight = null;
    }
  })();
  return forgejoHostsInflight;
}

/**
 * Parses a Forgejo PR / issue / commit URL for one of the given hosts.
 * Returns null when the URL host is not in `hosts` or the path is not a
 * previewable resource.
 */
export function parseForgejoUrl(url: string, hosts: string[]): GitHubPreviewRef | null {
  if (hosts.length === 0) return null;
  const hostAlt = hosts.map(escapeRegExp).join('|');
  const prefix = `^https?://(?:${hostAlt})/([^/]+)/([^/]+)`;

  let m = url.match(new RegExp(`${prefix}/pulls/(\\d+)`));
  if (m) return { kind: 'pr', owner: m[1], repo: m[2], number: parseInt(m[3], 10) };

  m = url.match(new RegExp(`${prefix}/issues/(\\d+)`));
  if (m) return { kind: 'issue', owner: m[1], repo: m[2], number: parseInt(m[3], 10) };

  m = url.match(new RegExp(`${prefix}/commit/([0-9a-f]{5,40})`));
  if (m) return { kind: 'commit', owner: m[1], repo: m[2], sha: m[3] };

  return null;
}

/**
 * Extracts all previewable Forgejo URLs from `text` for the given hosts,
 * deduplicated and in first-occurrence order.
 */
export function extractForgejoUrls(text: string, hosts: string[]): string[] {
  if (hosts.length === 0) return [];
  const hostAlt = hosts.map(escapeRegExp).join('|');
  const scan = new RegExp(`https?://(?:${hostAlt})/[^\\s<>"')]+`, 'g');
  const seen = new Set<string>();
  const out: string[] = [];
  for (const raw of text.matchAll(scan)) {
    const url = raw[0].replace(/[.,;:!?]+$/, '');
    if (!seen.has(url) && parseForgejoUrl(url, hosts)) {
      seen.add(url);
      out.push(url);
    }
  }
  return out;
}

async function fetchForgejoFromBackend(url: string, ref: GitHubPreviewRef): Promise<GitHubPreviewData> {
  const res = await fetch(
    `/api/integrations/forgejo/preview?url=${encodeURIComponent(url)}`,
  );
  await raiseForUnauthorized(res);
  if (!res.ok) throw new Error(`forgejo preview proxy ${res.status}`);
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  const raw: any = await res.json();
  // Forgejo/Gitea returns GitHub-like JSON, so reuse the same mapper.
  return mapRawToPreviewData(url, raw, ref);
}

export function cachedForgejoPreview(
  url: string,
  hosts: string[],
): Promise<GitHubPreviewData | null> {
  const ref = parseForgejoUrl(url, hosts);
  if (!ref) return Promise.resolve(null);
  return loadPreview(previewKey(url, ref), () => fetchForgejoFromBackend(url, ref));
}

export function refreshForgejoPreview(
  url: string,
  hosts: string[],
): Promise<GitHubPreviewData | null> {
  const ref = parseForgejoUrl(url, hosts);
  if (!ref) return Promise.resolve(null);
  return refreshPreview(previewKey(url, ref), () => fetchForgejoFromBackend(url, ref));
}

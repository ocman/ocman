/**
 * GitHub URL preview utilities.
 *
 * Fetches metadata via the ocman backend (/api/integrations/github/preview)
 * which discovers and injects a GitHub token server-side (GITHUB_TOKEN env
 * var or gh CLI config). Works for both public and private repos.
 */

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

// eslint-disable-next-line @typescript-eslint/no-explicit-any
async function fetchFromBackend(url: string): Promise<GitHubPreviewData> {
  const res = await fetch(
    `/api/integrations/github/preview?url=${encodeURIComponent(url)}`,
  );
  if (!res.ok) throw new Error(`preview proxy ${res.status}`);

  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  const raw: any = await res.json();
  return mapRawToPreviewData(url, raw);
}

// eslint-disable-next-line @typescript-eslint/no-explicit-any
function mapRawToPreviewData(url: string, raw: any): GitHubPreviewData {
  const ref = parseGitHubUrl(url)!;
  const repoFull = `${ref.owner}/${ref.repo}`;

  if (ref.kind === 'pr' || raw.pull_request !== undefined) {
    const merged = raw.merged === true || raw.merged_at != null || raw.pull_request?.merged_at != null;
    const stateClass = merged ? 'merged' : raw.state === 'closed' ? 'closed' : 'open';
    const stateLabel = merged ? 'Merged' : raw.state === 'closed' ? 'Closed' : 'Open';
    const stateIcon = merged ? 'bi-git' : raw.state === 'closed' ? 'bi-x-circle' : 'bi-circle';
    return {
      kind: 'pr',
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
// In-memory cache — avoids re-fetching on re-renders
// ---------------------------------------------------------------------------

const cache = new Map<string, GitHubPreviewData | 'error'>();

export async function cachedGitHubPreview(url: string): Promise<GitHubPreviewData | null> {
  if (cache.has(url)) {
    const hit = cache.get(url)!;
    return hit === 'error' ? null : hit;
  }
  if (!parseGitHubUrl(url)) return null;
  try {
    const data = await fetchFromBackend(url);
    cache.set(url, data);
    return data;
  } catch {
    cache.set(url, 'error');
    return null;
  }
}

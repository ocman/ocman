import { useEffect, useState } from 'react';

// How often a mounted consumer waiting on an absent host re-asks the
// backend, so a remote that connects later appears without a reload.
const HOST_MISS_RETRY_MS = 10_000;
import { api, type CapabilitiesResponse, type PlatformCapabilities, type PlatformCapabilityEntry } from './api';

/**
 * Empty capabilities used as a safe default before /api/capabilities
 * resolves and as a fallback when no platform claims a session. All
 * flags are false so capability-gated UI stays hidden until we have
 * authoritative data.
 */
const EMPTY_CAPS: PlatformCapabilities = {
  composer: false,
  respondPermission: false,
  respondQuestion: false,
  abort: false,
  compact: false,
  fork: false,
  move: false,
  events: false,
  agentCatalog: false,
  modelCatalog: false,
  slashCommands: false,
  shellExec: false,
  fileChanges: false,
  sessionInfo: false,
  liveConnectionHint: '',
  autoApprove: false,
  permissionRules: false,
};

let cached: CapabilitiesResponse | null = null;
let inflight: Promise<CapabilitiesResponse> | null = null;
const subscribers = new Set<(c: CapabilitiesResponse) => void>();

// Platform ids we've already tried to (re)resolve via a cache-miss
// refetch, so an unknown id (e.g. a genuinely absent platform) can't
// loop the network. Reset whenever a fresh response arrives.
const refetchedFor = new Set<string>();

/**
 * Refetch /api/capabilities and push the result to every mounted
 * consumer. Used when a platform or host id isn't present in the cached
 * response — a remote that connected after the initial fetch (or whose
 * capabilities weren't resolvable yet) would otherwise stay hidden until
 * a full reload. The stale response stays readable while it refetches.
 */
function invalidateAndReload() {
  loadCapabilities(true).catch(() => {});
}

/**
 * Loads /api/capabilities once per page load, caching the response in
 * module scope. Subsequent callers get the cached value immediately;
 * the result is shared across hook instances so the network call happens
 * exactly once.
 */
function loadCapabilities(force = false): Promise<CapabilitiesResponse> {
  if (cached && !force) return Promise.resolve(cached);
  if (inflight) return inflight;
  inflight = api.capabilities()
    .then((resp) => {
      cached = resp;
      inflight = null;
      // Un-suppress any ids that are now present, so a later
      // disconnect/reconnect can retry them. Ids still absent stay
      // suppressed to avoid a refetch loop on a genuinely-unknown id.
      for (const p of resp.platforms) refetchedFor.delete(p.id);
      for (const fn of subscribers) fn(resp);
      return resp;
    })
    .catch((err) => {
      inflight = null;
      throw err;
    });
  return inflight;
}

/**
 * useCapabilities returns the full /api/capabilities payload (every
 * registered platform with its capability flags). Components looking
 * up flags for a specific platform should usually call
 * usePlatformCapabilities(platformID) instead — it handles the lookup
 * and falls back to disabled flags when the platform isn't known.
 */
export function useCapabilities(): CapabilitiesResponse | null {
  // Lazy initializer reads the module-level cache so first render of
  // every consumer after the initial fetch already has the data —
  // there's no flash of all-false flags.
  const [state, setState] = useState<CapabilitiesResponse | null>(() => cached);

  useEffect(() => {
    // Subscribe even when warm, so a later refetch (a remote connecting)
    // reaches consumers that mounted after the initial load.
    let cancelled = false;
    const handler = (c: CapabilitiesResponse) => {
      if (!cancelled) setState(c);
    };
    subscribers.add(handler);
    if (!cached) loadCapabilities().catch(() => {
      // Network errors leave state at null. Components defaulting
      // through usePlatformCapabilities will treat that as "no
      // capabilities available", matching the conservative default.
    });
    return () => {
      cancelled = true;
      subscribers.delete(handler);
    };
  }, []);

  return state;
}

/**
 * Returns the capability flags for a specific platform ID. Falls back
 * to all-false when the platform isn't known yet (loading) or isn't
 * registered. Use this to gate UI affordances:
 *
 *   const caps = usePlatformCapabilities(session.platform);
 *   if (caps.composer) { renderComposer(); }
 *
 * Deliberately never branches on the platform identifier itself — that
 * pattern undoes the agent-agnostic design.
 */
export function usePlatformCapabilities(platformID: string | undefined): PlatformCapabilities {
  const all = useCapabilities();
  if (!platformID || !all) return EMPTY_CAPS;
  const entry = all.platforms.find((p): p is PlatformCapabilityEntry => p.id === platformID);
  // Cache miss: the id may belong to a remote that connected after the
  // initial fetch. Refetch once (per id, per response) so its composer
  // and other capability-gated UI appear without a page reload.
  if (!entry && !refetchedFor.has(platformID)) {
    refetchedFor.add(platformID);
    invalidateAndReload();
  }
  return entry?.capabilities ?? EMPTY_CAPS;
}

/**
 * Returns true when more than one platform is registered. Components
 * that display platform badges can use this to hide themselves when
 * there's only a single platform — the badge adds no information in
 * that case.
 */
export function useMultiPlatform(): boolean {
  const all = useCapabilities();
  // Count distinct *base* platforms, not registry entries: multi-remote
  // registers a compound "r-<remoteId>:<base>" id per remote, so several
  // entries can share one base (all OpenCode). Strip the remote prefix
  // before de-duping so the platform badge stays hidden when every
  // session is really the same platform.
  const bases = new Set(
    (all?.platforms ?? []).map((p) => {
      const id = p.id;
      if (id.startsWith('r-')) {
        const sep = id.indexOf(':');
        if (sep >= 0) return id.slice(sep + 1);
      }
      return id;
    }),
  );
  return bases.size > 1;
}

/**
 * Returns true when the host can run the /wt (worktree-sessions) flow:
 * git + tmux + opencode all available, AND an OpenCode adapter is
 * registered. Used to gate the `/wt` palette command, the project-
 * page Worktrees link, and the per-project Worktrees view (AD-7).
 *
 * Returns false during the initial /api/capabilities load — same
 * conservative default as every other capability flag — so gated UI
 * stays hidden until we have authoritative data.
 */
export function useWorktreeSessions(): boolean {
  const all = useCapabilities();
  return all?.worktreeSessions === true;
}

/**
 * Returns true when the local host can launch a managed OpenCode
 * instance (opencode + a usable runtime). Distinct from the tmux
 * capability so a future tmux-less (container) host still offers
 * launch. Launch affordances (/wt, worktree launch, new-session-into-
 * project) gate on this; the tmux-session UI keeps gating on tmux
 * (AD-8, #393).
 *
 * Reads the local host entry ("local"); falls back to the first host
 * when that id isn't present (defensive). Pass `remoteId` to ask about a
 * specific owner instead: a remote that isn't connected reports false
 * (fail closed) rather than borrowing the local host's answer. False
 * during the initial load — same conservative default as every other
 * capability flag.
 */
export function useOpencodeLaunch(remoteId?: string): boolean {
  const all = useCapabilities();
  const hosts = all?.hosts ?? [];
  const named = !!remoteId && remoteId !== 'local';
  const host = named
    ? hosts.find((h) => h.remoteId === remoteId)
    : hosts.find((h) => h.remoteId === 'local') ?? hosts[0];
  // A named owner missing from the snapshot may simply have connected
  // after it was taken: re-ask now and then every HOST_MISS_RETRY_MS
  // while this consumer is mounted and the owner is still absent.
  const missing = named && !!all && !host;
  useEffect(() => {
    if (!missing) return;
    invalidateAndReload();
    const timer = setInterval(invalidateAndReload, HOST_MISS_RETRY_MS);
    return () => clearInterval(timer);
  }, [missing, remoteId]);
  return host?.capabilities.opencodeLaunch === true;
}

/**
 * Returns true when more than one host (machine) is present — i.e. at
 * least one remote is connected in addition to the local machine. Host
 * badges use this to hide themselves on a single-host install where the
 * badge adds no information. Mirrors useMultiPlatform (multi-remote
 * support).
 */
export function useMultiHost(): boolean {
  const all = useCapabilities();
  return (all?.hosts?.length ?? 0) > 1;
}

import { useEffect } from 'react';
import { useQuery } from '@tanstack/react-query';
import { queryClient } from './queryClient';
import { onSseConnect } from './useGlobalEvents';

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
  forkAtMessage: false,
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
  revert: false,
  runtimeRestart: false,
};

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
  void queryClient.invalidateQueries({ queryKey: ['capabilities'] }, { cancelRefetch: false });
}

function useCapabilitiesQuery(remoteId?: string) {
  const query = useQuery({
    queryKey: ['capabilities'],
    queryFn: async () => {
      const response = await api.capabilities();
      for (const platform of response.platforms) refetchedFor.delete(platform.id);
      return response;
    },
    staleTime: Infinity,
    gcTime: Infinity,
    retry: false,
    refetchInterval: query => remoteId && remoteId !== 'local' && query.state.data &&
      !query.state.data.hosts?.some(host => host.remoteId === remoteId) ? HOST_MISS_RETRY_MS : false,
  }, queryClient);
  useEffect(() => onSseConnect(() => invalidateAndReload()), []);
  return query;
}

/**
 * useCapabilities returns the full /api/capabilities payload (every
 * registered platform with its capability flags). Components looking
 * up flags for a specific platform should usually call
 * usePlatformCapabilities(platformID) instead — it handles the lookup
 * and falls back to disabled flags when the platform isn't known.
 */
export function useCapabilities(): CapabilitiesResponse | null {
  return useCapabilitiesQuery().data ?? null;
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
  const entry = all?.platforms.find((p): p is PlatformCapabilityEntry => p.id === platformID);
  // Cache miss: the id may belong to a remote that connected after the
  // initial fetch. Refetch once (per id, per response) so its composer
  // and other capability-gated UI appear without a page reload.
  useEffect(() => {
    if (platformID && all && !entry && !refetchedFor.has(platformID)) {
      refetchedFor.add(platformID);
      invalidateAndReload();
    }
  }, [platformID, all, entry]);
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
  const all = useCapabilitiesQuery(remoteId).data;
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
  }, [missing, remoteId]);
  return host?.capabilities.opencodeLaunch === true;
}

/** Whether this machine runs OpenCode; gates OpenCode-only settings (FR-1). */
export function useLocalOpenCode(): boolean {
  const hosts = useCapabilities()?.hosts ?? [];
  return (hosts.find((h) => h.remoteId === 'local') ?? hosts[0])?.capabilities.opencode === true;
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

/** Platforms registered for one machine; remote entries carry their remoteId. */
export function useMachinePlatforms(remoteId = 'local'): PlatformCapabilityEntry[] {
  const all = useCapabilities();
  return (all?.platforms ?? []).filter((p) => (p.remoteId ?? 'local') === remoteId);
}

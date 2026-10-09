import { useEffect } from 'react';
import type { MutableRefObject } from 'react';
import { useApiStore } from '../../lib/apiStore';
import { compareSidebarActivity, computeSidebarHash, filterInactiveChildren, mergeSidebarSessions } from '../../lib/sidebarHelpers';
import { filterVisibleSessions, hasPendingPrompt } from '../../lib/sessionVisibility';
import { onSessionActivity, onSessionChanged, onSseConnect } from '../../lib/useGlobalEvents';
import { remoteLog } from '../../lib/remoteLog';
import { eventRefresh } from '../../lib/eventRefresh';

interface Options {
  enabled: boolean;
  enabledRef: MutableRefObject<boolean>;
  id?: string;
  loadRecentSessions: (signal?: AbortSignal) => Promise<void>;
  abortSignalRef: MutableRefObject<AbortController | null>;
  recentRequest: MutableRefObject<{ promise: Promise<void> } | null>;
  showArchivedRecentRef: MutableRefObject<boolean>;
  sidebarRecentHoursRef: MutableRefObject<number>;
}

// SSE is the primary invalidation path. Reconnects reconcile missed events.
export function useSidebarEvents({ enabled, enabledRef, id, loadRecentSessions,
  abortSignalRef, recentRequest, showArchivedRecentRef, sidebarRecentHoursRef }: Options) {
  const peekSession = useApiStore((s) => s.peekSession);
  const patchRecentSession = useApiStore((s) => s.patchRecentSession);
  const storeSetRecentSessions = useApiStore((s) => s.setRecentSessions);
  useEffect(() => {
    if (!enabled) return;
    const refresh = () => loadRecentSessions(abortSignalRef.current?.signal)
      .catch((err) => remoteLog.error('Failed to refresh recent sessions', err));
    let subscribed = true;
    const refreshAfterCurrent = async () => {
      await recentRequest.current?.promise.catch(() => {});
      if (subscribed) await refresh();
    };
    const changedRefresh = eventRefresh(refreshAfterCurrent);
    const unsubscribeChanged = onSessionChanged((sessionID, _session, patch, platform) => {
      if (!enabledRef.current || document.hidden) return;
      const matches = useApiStore.getState().recentSessions.filter(s => s.id === sessionID && (!platform || s.platform === platform));
      // Older unqualified events are safe only when the owner is unambiguous.
      if (patch && matches.length === 1) {
        const owner = matches[0].platform;
        patchRecentSession(sessionID, { ...patch,
          ...(patch.lastUserPromptAt !== undefined ? { lastUserPromptAt: Math.max(patch.lastUserPromptAt, matches[0].lastUserPromptAt ?? 0) } : {}),
          ...(patch.lastHaltAt !== undefined ? { lastHaltAt: Math.max(patch.lastHaltAt, matches[0].lastHaltAt ?? 0) } : {}),
          ...(patch.status === 'interrupted' && matches[0].status !== 'interrupted' ? { seen: false } : {}),
        }, owner);
        // Refresh the durable row before promoting it; never rank by arrival time.
        if ((!patch.status || patch.status === 'busy') && !patch.pendingPermission && !patch.pendingQuestion) return;
        const statusRow = useApiStore.getState().recentSessions.find(s => s.id === sessionID && s.platform === owner);
        const pendingReads = useApiStore.getState().pendingInterruptionReads;
        peekSession(sessionID, abortSignalRef.current?.signal, owner).then(({ session: row }) => {
          if (!subscribed || row.id !== sessionID || row.platform !== owner) return;
          const current = useApiStore.getState().recentSessions.find(s => s.id === sessionID && s.platform === owner);
          const readState = mergeSidebarSessions([row], current ? [current] : [], undefined, statusRow ? [statusRow] : [],
            { ...pendingReads, ...useApiStore.getState().pendingInterruptionReads })[0];
          const sameInterruption = row.status === 'interrupted' && current?.status === 'interrupted' && current.timeUpdated <= row.timeUpdated;
          patchRecentSession(sessionID, { ...(sameInterruption ? { seen: readState.seen, seenTimeUpdated: readState.seenTimeUpdated } : {}), lastTurnCompletedAt: Math.max(
            row.lastTurnCompletedAt ?? 0, current?.lastTurnCompletedAt ?? 0,
          ), lastUserPromptAt: readState.lastUserPromptAt, lastHaltAt: readState.lastHaltAt }, owner);
        }).catch((err) => remoteLog.error('Failed to refresh completed session', err));
        return;
      }
      changedRefresh.schedule();
    });
    const unsubscribeConnect = onSseConnect(() => { void refreshAfterCurrent(); });
    const pendingActivity = new Map<string, number>();
    const hiddenSessions = new Set<string>();
    const unsubscribeActivity = onSessionActivity((sessionID, timeUpdated) => {
      if (!enabledRef.current || document.hidden) return;
      const session = useApiStore.getState().recentSessions.find((s) => s.id === sessionID);
      if (session) {
        // Per-token activity updates the label, never the ordering signal.
        if (compareSidebarActivity(session, { timeUpdated }) > 0) patchRecentSession(sessionID, { timeUpdated });
      } else if (!hiddenSessions.has(sessionID)) {
        const pending = pendingActivity.get(sessionID);
        pendingActivity.set(sessionID, Math.max(pending ?? 0, timeUpdated));
        if (pending !== undefined) return;
        peekSession(sessionID, abortSignalRef.current?.signal).then(({ session: row }) => {
          if (!subscribed) return;
          const candidates = filterInactiveChildren([row], id);
          if (!candidates.length || (!row.pinned && !hasPendingPrompt(row) && row.id !== id && !showArchivedRecentRef.current && !filterVisibleSessions(candidates).length)) {
            hiddenSessions.add(sessionID);
            return;
          }
          // Replayed events get receipt times. Trust activity only on live rows.
          const live = row.status === 'busy';
          const since = Date.now() - sidebarRecentHoursRef.current * 60 * 60 * 1000;
          if (!row.pinned && !hasPendingPrompt(row) && row.id !== id && !live && row.timeUpdated < since) {
            hiddenSessions.add(sessionID);
            return;
          }
          const current = useApiStore.getState().recentSessions;
          const updated = live
            ? { ...row, timeUpdated: Math.max(row.timeUpdated, pendingActivity.get(sessionID) ?? 0) }
            : row;
          const next = mergeSidebarSessions([updated, ...current.filter((s) => s.id !== sessionID)], current, id);
          storeSetRecentSessions(next, computeSidebarHash(next));
        }).catch((err) => remoteLog.error('Failed to refresh active session', err))
          .finally(() => { pendingActivity.delete(sessionID); });
      }
    });
    return () => {
      unsubscribeChanged();
      unsubscribeConnect();
      unsubscribeActivity();
      subscribed = false;
      changedRefresh.dispose();
    };
  }, [loadRecentSessions, abortSignalRef, patchRecentSession, peekSession, id, storeSetRecentSessions, enabled,
    enabledRef, recentRequest, showArchivedRecentRef, sidebarRecentHoursRef]);
}

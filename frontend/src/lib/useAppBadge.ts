import { useEffect } from 'react';
import type { NotifyEntry } from './api';
import { useNotifySnapshot } from './useNotifyData';
import { dismissIrrelevantSessionNotifications } from './pushNotifications';
import { useInbox } from './queries';
import { useBadgeSettings } from './badgeSettings';
import { attentionSessionEntries } from './sessionAttention';
import { useBetaFeatures } from './betaFeatures';

export function attentionSessionCount(entries: NotifyEntry[]): number {
  return attentionSessionEntries(entries).length;
}

export async function updateAppBadge(count: number): Promise<void> {
  if (typeof navigator === 'undefined' || !navigator.setAppBadge) return;
  try {
    if (count > 0) await navigator.setAppBadge(count);
    else await navigator.clearAppBadge();
  } catch {
    // Permission and OS support vary; the in-app attention indicators remain.
  }
}

export function useAppBadge() {
  const factory = useBetaFeatures((s) => s.factory);
  const snapshot = useNotifySnapshot();
  const entries = snapshot?.entries ?? null;
  const snapshotAt = snapshot?.fetchedAt ?? 0;
  const inbox = useInbox();
  const { data: settings } = useBadgeSettings();
  useEffect(() => {
    if (!settings || (settings.sessions && entries === null) ||
      ((settings.primary || settings.factory || settings.routines) && !inbox.data)) return;
    const categories: Record<string, boolean> = { general: settings.primary, factory: factory && settings.factory, routine: settings.routines };
    const inboxCount = (inbox.data?.items ?? []).filter((item) => !item.readAt && !item.archivedAt &&
      !item.permission && categories[item.category ?? 'general']).length;
    void updateAppBadge((settings.sessions ? attentionSessionCount(entries ?? []) : 0) + inboxCount);
  }, [entries, inbox.data, settings, factory]);
  useEffect(() => {
    const controller = new AbortController();
    if (entries !== null) void dismissIrrelevantSessionNotifications(entries, snapshotAt, controller.signal).catch(() => {});
    return () => controller.abort();
  }, [entries, snapshotAt]);
}

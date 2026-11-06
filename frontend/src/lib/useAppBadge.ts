import { useEffect } from 'react';
import type { NotifyEntry } from './api';
import { useNotifySnapshot } from './useNotifyData';
import { dismissIrrelevantSessionNotifications } from './pushNotifications';
import { useInbox } from './queries';
import { useBadgeSettings } from './badgeSettings';
import { needsSessionAttention } from './sessionAttention';
import { useBetaFeatures } from './betaFeatures';

export function hasSessionAttention(entries: NotifyEntry[]): boolean {
  return entries.some(needsSessionAttention);
}

export async function updateAppBadge(attention: boolean): Promise<void> {
  if (typeof navigator === 'undefined' || !navigator.setAppBadge) return;
  try {
    if (attention) await navigator.setAppBadge();
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
    const inboxAttention = (inbox.data?.items ?? []).some((item) => !item.readAt && !item.archivedAt &&
      !item.permission && categories[item.category ?? 'general']);
    void updateAppBadge((settings.sessions && hasSessionAttention(entries ?? [])) || inboxAttention);
  }, [entries, inbox.data, settings, factory]);
  useEffect(() => {
    const controller = new AbortController();
    if (entries !== null) void dismissIrrelevantSessionNotifications(entries, snapshotAt, controller.signal).catch(() => {});
    return () => controller.abort();
  }, [entries, snapshotAt]);
}

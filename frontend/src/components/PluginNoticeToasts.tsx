import { useEffect, useState } from 'react';
import { useQueryClient } from '@tanstack/react-query';
import { formatDuration } from '../lib/format';
import { pluginNoticeKey, usePluginNotices, type ListedPluginNotice, type PluginNoticeAction } from '../lib/pluginNotices';
import type { PluginActionRequest } from '../lib/plugins';
import { usePluginUIs } from '../lib/pluginUI';
import { Button, RouteButton } from './Control';
import { PluginActionDialog } from './PluginActionDialog';
import { PromptToast, PromptToastActions, PromptToastBody, PromptToastClose, PromptToastHeading } from './PromptToast';
import styles from './PluginNoticeToasts.module.css';

function Elapsed({ since }: { since: number }) {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    const timer = window.setInterval(() => setNow(Date.now()), 1000);
    return () => window.clearInterval(timer);
  }, []);
  // The host bounds startedAt; still never let a bad value throw during render.
  const start = new Date(since);
  if (!Number.isFinite(start.getTime())) return null;
  return <time className={styles.elapsed} dateTime={start.toISOString()}>{formatDuration(Math.max(0, now - since))}</time>;
}

/**
 * Persistent toasts for notices that plugin backends publish (notice.v1).
 * Mounted once inside the app-wide toast provider so they show on every page.
 * A dismissal is local to this tab and lasts while the notice ID stays active.
 */
export function PluginNoticeToasts() {
  const client = useQueryClient();
  const { notices } = usePluginNotices();
  const { uis, error: uiError } = usePluginUIs();
  const [dismissed, setDismissed] = useState<ReadonlySet<string>>(new Set());
  const [action, setAction] = useState<{ label: string; request: PluginActionRequest } | null>(null);

  // Forget a dismissal once its owner reports the notice gone, so the same ID
  // shows again if the plugin publishes it later. An unreachable owner keeps
  // the dismissal; its notices are hidden anyway.
  useEffect(() => client.getQueryCache().subscribe((event) => {
    const [kind, owner] = event.query.queryKey as [unknown, unknown];
    if (event.type !== 'updated' || kind !== 'plugin-notices' || typeof owner !== 'string' || event.query.state.status !== 'success') return;
    const data = event.query.state.data as Record<string, { notices?: { id: string }[] }> | undefined;
    const active = new Set(Object.entries(data && typeof data === 'object' ? data : {}).flatMap(([pluginId, entry]) =>
      (Array.isArray(entry?.notices) ? entry.notices : []).map((n) => pluginNoticeKey(owner, pluginId, n.id))));
    setDismissed((previous) => {
      const next = new Set([...previous].filter((key) => (JSON.parse(key) as string[])[0] !== owner || active.has(key)));
      return next.size === previous.size ? previous : next;
    });
  }), [client]);

  const dismiss = (key: string) => setDismissed((previous) => new Set([...previous, key]));
  const run = (item: ListedPluginNotice, declared: PluginNoticeAction) => setAction({
    label: declared.label,
    request: { ownerId: item.ownerId, pluginId: item.pluginId, actionId: declared.actionId, operationId: crypto.randomUUID(),
      placement: declared.placement, surface: 'command-palette', context: { ownerId: item.ownerId } },
  });

  return <>
    {notices.filter((item) => !dismissed.has(item.key)).map((item) => {
      const { notice } = item;
      // In-app plugin UIs are hub-local. Never link a remote notice to a local plugin with the same ID.
      const canView = !uiError && item.ownerId === 'local' && uis.some((ui) =>
        ui.ownerId === item.ownerId && ui.pluginId === item.pluginId && ui.compatible && ui.index);
      return <PromptToast key={item.key} data-tone={notice.tone} open duration={Infinity}
        type={notice.tone === 'critical' ? 'foreground' : 'background'}
        onOpenChange={(open) => { if (!open) dismiss(item.key); }}>
        <PromptToastClose label={`Dismiss ${notice.title}`} />
        <PromptToastHeading>{item.pluginLabel}</PromptToastHeading>
        <PromptToastBody>
          <span className={styles.title}>{notice.title}</span>
          {notice.body && <span className={styles.body}>{notice.body}</span>}
          {notice.startedAt ? <span className={styles.meta}>Elapsed <Elapsed since={notice.startedAt} /></span> : null}
        </PromptToastBody>
        {(canView || notice.actions.length > 0) && <PromptToastActions label={`${notice.title} actions`}>
          {canView && <RouteButton to={`/plugins/${encodeURIComponent(item.pluginId)}`} size="small" variant="accent">View</RouteButton>}
          {notice.actions.map((declared, index) => <Button key={declared.actionId} type="button" size="small" variant={!canView && index === 0 ? 'accent' : 'default'}
            onClick={() => run(item, declared)}>{declared.label}</Button>)}
        </PromptToastActions>}
      </PromptToast>;
    })}
    {action && <PluginActionDialog key={action.request.operationId} label={action.label} request={action.request} onClose={() => {
      setAction(null);
      void client.invalidateQueries({ queryKey: ['plugin-notices'] });
    }} />}
  </>;
}

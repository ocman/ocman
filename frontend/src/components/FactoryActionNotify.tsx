import { useEffect, useState } from 'react';
import { useQueryClient } from '@tanstack/react-query';
import { useNavigate } from 'react-router-dom';
import * as Toast from '@radix-ui/react-toast';
import type { InboxItem, InboxResponse } from '../lib/api';
import { useInbox } from '../lib/queries';
import { useUiStore } from '../lib/uiStore';
import { Button } from './Control';
import { PromptToast, PromptToastHeading, PromptToastBody, PromptToastActions, PromptToastClose } from './PromptToast';

const key = (item: InboxItem) => `${item.remoteId}:${item.id}`;
const target = (item: InboxItem) => item.remoteId === 'local' ? '/factory/overview' : '/inbox?category=factory';

export function FactoryActionNotify() {
  const inbox = useInbox(false, true);
  const client = useQueryClient();
  const navigate = useNavigate();
  const [toasts, setToasts] = useState<InboxItem[]>([]);
  const [dismissed, setDismissed] = useState<Set<string>>(new Set());
  useEffect(() => {
    let seen: Set<string> | null = null;
    const consume = (data: InboxResponse | undefined) => {
      if (!data) return;
      const actions = data.items.filter((item) => item.category === 'factory' && item.id.startsWith('factory-action-'));
      if (seen === null) { seen = new Set(actions.map(key)); return; }
      const fresh = actions.filter((item) => !seen!.has(key(item)) && !item.readAt);
      for (const item of actions) seen.add(key(item));
      if (!fresh.length) return;
      setToasts((previous) => [...previous, ...fresh]);
      if (!useUiStore.getState().notificationsEnabled || typeof Notification === 'undefined' || Notification.permission !== 'granted') return;
      for (const item of fresh) {
        try {
          const notification = new Notification('ocman — Factory action required', {
            body: item.title, tag: `ocman:${key(item)}`, requireInteraction: true,
            icon: '/apple-touch-icon.png', data: { url: target(item) },
          });
          notification.onclick = () => { window.focus(); navigate(target(item)); notification.close(); };
        } catch { /* In-app toast remains available if the OS rejects notifications. */ }
      }
    };
    consume(client.getQueryData<InboxResponse>(['inbox']));
    return client.getQueryCache().subscribe((event) => {
      if (event.type === 'updated' && event.query.queryKey.length === 1 && event.query.queryKey[0] === 'inbox') {
        consume(event.query.state.data as InboxResponse | undefined);
      }
    });
  }, [client, navigate]);
  const active = new Set(inbox.data?.items.filter((item) => !item.readAt).map(key));
  const dismiss = (item: InboxItem) => setDismissed((previous) => new Set([...previous, key(item)]));
  return toasts.filter((item) => active.has(key(item)) && !dismissed.has(key(item))).map((item) => (
    <PromptToast key={key(item)} open duration={Infinity} onOpenChange={(open) => { if (!open) dismiss(item); }}>
      <PromptToastClose />
      <PromptToastHeading>Factory action required</PromptToastHeading>
      <PromptToastBody truncate>{item.title}</PromptToastBody>
      <PromptToastActions label="Factory notification actions">
        <Toast.Action asChild altText="Open Factory actions"><Button size="small" variant="accent" onClick={() => { dismiss(item); navigate(target(item)); }}>Open actions</Button></Toast.Action>
      </PromptToastActions>
    </PromptToast>
  ));
}

import { useState } from 'react';
import { Button } from '../components/Control';
import { MarkdownContent } from '../components/assistant/MarkdownText';
import { RelativeTime } from '../components/RelativeTime';
import { useArchiveAllReadInboxItems, useArchiveInboxItems, useInbox, useMarkInboxItemRead } from '../lib/queries';
import type { InboxItem } from '../lib/api';
import './Inbox.css';

function sourceLabel(remoteId: string) {
  return remoteId === 'local' ? 'This machine' : remoteId;
}

function InboxCard({ item, selected, onSelect }: { item: InboxItem; selected: boolean; onSelect: () => void }) {
  const [expanded, setExpanded] = useState(false);
  const markRead = useMarkInboxItemRead();
  const open = () => {
    setExpanded((value) => !value);
    if (!item.readAt) markRead.mutate({ id: item.id, remoteId: item.remoteId });
  };
  return (
    <article className={`inbox-card${item.readAt ? '' : ' unread'}`}>
      <label className="inbox-select"><input type="checkbox" checked={selected} onChange={onSelect} aria-label={`Select ${item.title}`} /></label>
      <button type="button" className="inbox-title" onClick={open} aria-expanded={expanded}>
        <span>{expanded ? <MarkdownContent text={item.title} /> : item.title}</span>
        <i className={`bi bi-chevron-${expanded ? 'up' : 'down'}`} aria-hidden="true" />
      </button>
      <div className="inbox-meta"><span>{sourceLabel(item.remoteId)}</span><span>Arrived <RelativeTime iso={new Date(item.createdAt).toISOString()} /></span></div>
      {expanded && <div className="inbox-body"><MarkdownContent text={item.body} /></div>}
    </article>
  );
}

export function Inbox() {
  const inbox = useInbox();
  const archive = useArchiveInboxItems();
  const archiveRead = useArchiveAllReadInboxItems();
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const items = inbox.data?.items ?? [];
  const selectedItems = items.filter((item) => selected.has(`${item.remoteId}:${item.id}`));
  const toggle = (item: InboxItem) => setSelected((current) => {
    const next = new Set(current);
    const key = `${item.remoteId}:${item.id}`;
    if (next.has(key)) next.delete(key); else next.add(key);
    return next;
  });
  const archiveSelected = () => archive.mutate(selectedItems.map(({ id, remoteId }) => ({ id, remoteId })), { onSuccess: () => setSelected(new Set()) });
  const remoteIds = [...new Set(items.map((item) => item.remoteId))];
  const archiveAllRead = () => remoteIds.forEach((remoteId) => archiveRead.mutate(remoteId));

  return <main className="inbox-page">
    <div className="inbox-header"><div><h2>Inbox</h2><p>Messages delivered to this ocman instance.</p></div><div className="inbox-actions"><Button type="button" disabled={!selectedItems.length || archive.isPending} onClick={archiveSelected}>Archive selected</Button><Button type="button" disabled={!items.some((item) => item.readAt) || archiveRead.isPending} onClick={archiveAllRead}>Archive all read</Button></div></div>
    {inbox.isLoading && <p role="status">Loading inbox…</p>}
    {inbox.isError && <p role="alert">Could not load inbox.</p>}
    {!inbox.isLoading && !items.length && <p className="oc-empty">Your inbox is empty.</p>}
    <section className="inbox-list" aria-label="Inbox messages">{items.map((item) => <InboxCard key={`${item.remoteId}:${item.id}`} item={item} selected={selected.has(`${item.remoteId}:${item.id}`)} onSelect={() => toggle(item)} />)}</section>
  </main>;
}

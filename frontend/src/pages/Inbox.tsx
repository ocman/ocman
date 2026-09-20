import { useState } from 'react';
import { Link, useSearchParams } from 'react-router-dom';
import { Button } from '../components/Control';
import { PermissionPrompt } from '../components/session/PermissionPrompt';
import { MarkdownContent } from '../components/assistant/MarkdownText';
import { RelativeTime } from '../components/RelativeTime';
import { useArchiveAllReadInboxItems, useArchiveInboxItems, useInbox, useMarkInboxItemRead, useMarkInboxItemUnread, useRespondInboxPermission } from '../lib/queries';
import type { InboxItem } from '../lib/api';
import './Inbox.css';

function sourceLabel(remoteId: string) {
  return remoteId === 'local' ? 'This machine' : remoteId;
}

const filters = [
  { id: 'all', label: 'All', icon: 'inbox' },
  { id: 'unread', label: 'Unread', icon: 'envelope' },
  { id: 'read', label: 'Read', icon: 'envelope-open' },
] as const;

function itemKey(item: InboxItem) {
  return `${item.remoteId}:${item.id}`;
}

function itemCategory(item: InboxItem) {
  return item.category ?? 'general';
}

const categories = [
  { id: 'all', label: 'All', icon: 'inbox' },
  { id: 'general', label: 'Primary', icon: 'chat-left-text' },
  { id: 'factory', label: 'Factory', icon: 'buildings' },
  { id: 'routine', label: 'Routines', icon: 'clock-history' },
  { id: 'permission', label: 'Permissions', icon: 'shield-lock' },
] as const;

function InboxPermissionActions({ permission }: { permission: NonNullable<InboxItem['permission']> }) {
  const respond = useRespondInboxPermission();
  return <section className="inbox-permission-actions" aria-label="Permission actions">
    <Link to={`/session/${encodeURIComponent(permission.sessionId)}?platform=${encodeURIComponent(permission.platform)}`}>Open session</Link>
    <PermissionPrompt permission={permission} disabled={respond.isPending || respond.isSuccess}
      error={respond.isError ? 'Could not send your response. Please try again.' : null}
      onReply={(reply) => respond.mutate({ permission, reply })} />
    {respond.isSuccess && <p role="status">Response sent. Waiting for the request to close.</p>}
  </section>;
}

export function Inbox() {
  const inbox = useInbox();
  const archive = useArchiveInboxItems();
  const archiveRead = useArchiveAllReadInboxItems();
  const markRead = useMarkInboxItemRead();
  const markUnread = useMarkInboxItemUnread();
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [activeKey, setActiveKey] = useState<string | null>(null);
  const [filter, setFilter] = useState<typeof filters[number]['id']>('all');
  const [searchParams, setSearchParams] = useSearchParams();
  const activeCategory = searchParams.get('category') ?? 'all';
  const items = inbox.data ? inbox.data.items : [];
  const activeItem = items.find((item) => itemKey(item) === activeKey);
  const categoryItems = items.filter((item) => activeCategory === 'all' || itemCategory(item) === activeCategory);
  const unreadCount = categoryItems.filter((item) => !item.readAt).length;
  const counts = { all: categoryItems.length, unread: unreadCount, read: categoryItems.length - unreadCount };
  const visibleItems = categoryItems.filter((item) => filter === 'all' || (filter === 'read' ? !!item.readAt : !item.readAt));
  const selectedItems = items.filter((item) => selected.has(itemKey(item)));
  const open = (item: InboxItem) => {
    setActiveKey(itemKey(item));
    if (!item.readAt) markRead.mutate({ id: item.id, remoteId: item.remoteId });
  };
  const toggle = (item: InboxItem) => setSelected((current) => {
    const next = new Set(current);
    const key = itemKey(item);
    if (next.has(key)) next.delete(key); else next.add(key);
    return next;
  });
  const archiveSelected = () => archive.mutate(selectedItems.map(({ id, remoteId }) => ({ id, remoteId })), { onSuccess: () => setSelected(new Set()) });
  const remoteIds = [...new Set(items.map((item) => item.remoteId))];
  const archiveAllRead = () => remoteIds.forEach((remoteId) => archiveRead.mutate(remoteId));
  const selectCategory = (id: string) => {
    setSearchParams((params) => { params.set('category', id); return params; }, { replace: true });
    setActiveKey(null);
  };

  return <main className="inbox-page">
    {(archive.isError || archiveRead.isError) && <p role="alert">Could not archive messages. Please try again.</p>}
    {markRead.isError && <p role="alert">Could not mark the message as read. Open it again to retry.</p>}
    {markUnread.isError && <p role="alert">Could not mark the message as unread. Please try again.</p>}
    <div className={`inbox-workspace${activeItem ? ' has-active-message' : ''}`}>
      <section className="inbox-mailbox" aria-label="Inbox messages">
        <div className="inbox-filters">
          <div className="inbox-categories" role="group" aria-label="Message category">
            {filters.map(({ id, label, icon }) => <Button key={id} type="button" size="small" variant={filter === id ? 'accent' : 'default'} aria-pressed={filter === id} onClick={() => { setFilter(id); setActiveKey(null); }}>
              <i className={`bi bi-${icon}`} aria-hidden="true" /><span>{label}</span>{' '}<span className="inbox-count">{counts[id]}</span>
            </Button>)}
          </div>
          <div className="inbox-types" role="group" aria-label="Message type">
            {categories.map(({ id, label, icon }) => <Button key={id} type="button" size="small" title={label} aria-label={label} aria-pressed={activeCategory === id} variant={activeCategory === id ? 'accent' : 'default'} onClick={() => selectCategory(id)}>
              <i className={`bi bi-${icon}`} aria-hidden="true" />
              {activeCategory === id && <span>{label}</span>}
            </Button>)}
          </div>
          <div className="inbox-actions">
            <Button type="button" size="small" disabled={!selectedItems.length || archive.isPending} onClick={archiveSelected}><i className="bi bi-archive" aria-hidden="true" />Archive selected{selectedItems.length > 0 && ` (${selectedItems.length})`}</Button>
            <Button type="button" size="small" disabled={!items.some((item) => item.readAt) || archiveRead.isPending} onClick={archiveAllRead}>Archive all read</Button>
          </div>
        </div>
        <div className="inbox-list">
          {inbox.isLoading && <p className="oc-empty" role="status">Loading inbox…</p>}
          {inbox.isError && <p className="oc-empty" role="alert">Could not load inbox.</p>}
          {inbox.isSuccess && !visibleItems.length && <p className="oc-empty">{items.length ? 'No messages match these filters.' : 'Your inbox is empty.'}</p>}
          {visibleItems.map((item) => <article key={itemKey(item)} className={`inbox-message${item.readAt ? '' : ' unread'}${activeKey === itemKey(item) ? ' active' : ''}`}>
            <input className="inbox-select" type="checkbox" checked={selected.has(itemKey(item))} onChange={() => toggle(item)} aria-label={`Select ${item.title}`} />
            <button type="button" className="inbox-message-open" onClick={() => open(item)} aria-current={activeKey === itemKey(item) ? 'true' : undefined}>
              <span className="inbox-meta"><span>{categories.find(({ id }) => id === itemCategory(item))?.label}</span><span><RelativeTime iso={new Date(item.createdAt).toISOString()} /></span></span>
              <span className="inbox-subject">{!item.readAt && <span className="inbox-unread-dot" aria-label="Unread" />}{item.title}</span>
              <span className="inbox-preview">{item.body}</span>
            </button>
          </article>)}
        </div>
      </section>
      <section className="inbox-reader" aria-label="Message body">
        {activeItem ? <>
          <div className="inbox-reader-toolbar">
            <Button type="button" size="small" className="inbox-back" onClick={() => setActiveKey(null)}><i className="bi bi-arrow-left" aria-hidden="true" />Back to messages</Button>
            <span className="inbox-meta">From {sourceLabel(activeItem.remoteId)}</span>
            <div className="inbox-actions">
            <Button type="button" size="small" disabled={!activeItem.readAt || markRead.isPending || markUnread.isPending} onClick={() => markUnread.mutate({ id: activeItem.id, remoteId: activeItem.remoteId }, { onSuccess: () => setActiveKey(null) })}><i className="bi bi-envelope" aria-hidden="true" />Mark unread</Button>
            <Button type="button" size="small" disabled={archive.isPending} onClick={() => archive.mutate([{ id: activeItem.id, remoteId: activeItem.remoteId }])}><i className="bi bi-archive" aria-hidden="true" />Archive message</Button>
            </div>
          </div>
          <article className="inbox-letter" key={itemKey(activeItem)}>
            <div className="inbox-letter-header" data-testid="inbox-message-header">
              <div className="inbox-letter-subject" role="heading" aria-level={3}><MarkdownContent text={activeItem.title} /></div>
              <div className="inbox-meta"><span>{sourceLabel(activeItem.remoteId)}</span><time dateTime={new Date(activeItem.createdAt).toISOString()}>{new Date(activeItem.createdAt).toLocaleString()}</time></div>
            </div>
            <div className="inbox-body"><MarkdownContent text={activeItem.body} preserveLineBreaks /></div>
            {activeItem.permission && <InboxPermissionActions permission={activeItem.permission} />}
          </article>
        </> : <div className="inbox-reader-empty"><i className="bi bi-envelope-open" aria-hidden="true" /><h3>Select a message</h3><p>Choose a message from your inbox to read it here.</p></div>}
      </section>
    </div>
  </main>;
}

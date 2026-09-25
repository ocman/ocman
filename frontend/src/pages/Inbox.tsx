import { useDeferredValue, useEffect, useRef, useState } from 'react';
import { Link, useSearchParams } from 'react-router-dom';
import { Button, SearchField } from '../components/Control';
import { PermissionPrompt } from '../components/session/PermissionPrompt';
import { MarkdownContent } from '../components/assistant/MarkdownText';
import { RelativeTime } from '../components/RelativeTime';
import { SegmentedControl } from '../components/SegmentedControl';
import { EmptyState } from '../components/EmptyState';
import { useArchiveAllReadInboxItems, useArchiveInboxItems, useInbox, useMarkInboxItemRead, useMarkInboxItemUnread, useRespondInboxPermission } from '../lib/queries';
import type { InboxItem } from '../lib/api';
import { fuzzyMatch } from '../lib/format';
import { useShortcut } from '../lib/shortcutRegistry';
import './Inbox.css';

function sourceLabel(remoteId: string) {
  return remoteId === 'local' ? 'This machine' : remoteId;
}

const filters = [
  { id: 'all', label: 'All', icon: 'inbox' },
  { id: 'unread', label: 'Unread', icon: 'envelope' },
  { id: 'archived', label: 'Archived', icon: 'archive' },
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
    <PermissionPrompt permission={permission} disabled={respond.isPending || respond.isSuccess}
      error={respond.isError ? 'Could not send your response. Please try again.' : null}
      onReply={(reply) => respond.mutate({ permission, reply })} />
    {respond.isSuccess && <p role="status">Response sent. Waiting for the request to close.</p>}
  </section>;
}

export function Inbox() {
  const [filter, setFilter] = useState<typeof filters[number]['id']>('all');
  const archived = filter === 'archived';
  const inbox = useInbox(archived);
  const archive = useArchiveInboxItems();
  const archiveRead = useArchiveAllReadInboxItems();
  const markRead = useMarkInboxItemRead();
  const markUnread = useMarkInboxItemUnread();
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [activeKey, setActiveKey] = useState<string | null>(null);
  const [search, setSearch] = useState('');
  const query = useDeferredValue(search.trim());
  const actions = useRef<HTMLDetailsElement>(null);
  const [searchParams, setSearchParams] = useSearchParams();
  const activeCategory = searchParams.get('category') ?? 'all';
  const items = inbox.data ? inbox.data.items : [];
  const activeItem = items.find((item) => itemKey(item) === activeKey);
  const activeSession = activeItem?.session ?? (activeItem?.permission ? { ...activeItem.permission, title: '' } : undefined);
  const categoryItems = items.filter((item) => activeCategory === 'all' || itemCategory(item) === activeCategory);
  const visibleItems = categoryItems.filter((item) => (filter !== 'unread' || !item.readAt)
    && fuzzyMatch(query, `${item.title} ${item.body} ${sourceLabel(item.remoteId)} ${JSON.stringify(item.session ?? {})} ${JSON.stringify(item.permission ?? {})}`));
  const selectedItems = items.filter((item) => !item.archivedAt && selected.has(itemKey(item)));
  const open = (item: InboxItem) => {
    setActiveKey(itemKey(item));
    if (!item.readAt && !item.archivedAt) markRead.mutate({ id: item.id, remoteId: item.remoteId });
  };
  const toggle = (item: InboxItem) => setSelected((current) => {
    const next = new Set(current);
    const key = itemKey(item);
    if (next.has(key)) next.delete(key); else next.add(key);
    return next;
  });
  const archiveSelected = () => {
    actions.current?.removeAttribute('open');
    archive.mutate(selectedItems.map(({ id, remoteId }) => ({ id, remoteId })), { onSuccess: () => setSelected(new Set()) });
  };
  const remoteIds = [...new Set(items.map((item) => item.remoteId))];
  const archiveAllRead = () => {
    actions.current?.removeAttribute('open');
    remoteIds.forEach((remoteId) => archiveRead.mutate(remoteId));
  };
  const selectCategory = (id: string) => {
    setSearchParams((params) => { params.set('category', id); return params; }, { replace: true });
    setActiveKey(null);
  };

  // Open the first message once, when the inbox first loads, so the reader
  // isn't empty on arrival. Only then: closing or filtering a message is a
  // deliberate act and must not be undone. Skipped on narrow screens, where
  // the reader replaces the list and would hide the inbox.
  const autoSelected = useRef(false);
  const firstItem = visibleItems[0];
  useEffect(() => {
    if (autoSelected.current || !firstItem || window.innerWidth <= 700) return;
    autoSelected.current = true;
    setActiveKey(itemKey(firstItem));
    if (!firstItem.readAt && !firstItem.archivedAt) markRead.mutate({ id: firstItem.id, remoteId: firstItem.remoteId });
    // markRead is a stable mutation handle; re-running on it would loop.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [firstItem && itemKey(firstItem)]);

  useShortcut({
    id: 'inbox.archive',
    scope: 'site',
    keys: [{ code: 'Delete' }, { code: 'Backspace' }],
    description: 'Archive the open message',
    enabled: () => !!activeItem && !archived,
    handler: () => {
      if (!activeItem || archived) return;
      archive.mutate([{ id: activeItem.id, remoteId: activeItem.remoteId }]);
      setActiveKey(null);
    },
  });

  return <main className="inbox-page">
    {(archive.isError || archiveRead.isError) && <p role="alert">Could not archive messages. Please try again.</p>}
    {markRead.isError && <p role="alert">Could not mark the message as read. Open it again to retry.</p>}
    {markUnread.isError && <p role="alert">Could not mark the message as unread. Please try again.</p>}
    <div className={`inbox-workspace${activeItem ? ' has-active-message' : ''}`}>
      <section className="inbox-mailbox" aria-label="Inbox messages">
        <div className="inbox-filters">
          <div className="inbox-filter-row">
            <SearchField aria-label="Search inbox" placeholder="Search inbox…" value={search} onChange={(event) => { setSearch(event.target.value); setActiveKey(null); }} />
            <SegmentedControl label="Message status" compact options={filters.map(({ id, label, icon }) => ({ value: id, label, icon: `bi-${icon}` }))} value={filter} onChange={(id) => { setFilter(id); setActiveKey(null); setSelected(new Set()); }} />
          </div>
          <div className="inbox-filter-row">
            <SegmentedControl label="Message type" compact options={categories.map(({ id, label, icon }) => ({ value: id, label, icon: `bi-${icon}` }))} value={activeCategory} onChange={selectCategory} />
            <details className="inbox-action-menu" ref={actions} onKeyDown={(event) => { if (event.key === 'Escape') event.currentTarget.open = false; }} onBlur={(event) => { if (!event.currentTarget.contains(event.relatedTarget)) event.currentTarget.open = false; }}>
              <summary className="oc-button oc-button--default oc-button--small" aria-label="Inbox actions" title="Inbox actions"><i className="bi bi-three-dots" aria-hidden="true" /></summary>
              <div className="inbox-action-menu-items">
                <Button type="button" size="small" disabled={!selectedItems.length || archive.isPending} onClick={archiveSelected}><i className="bi bi-archive" aria-hidden="true" />Archive selected{selectedItems.length > 0 && ` (${selectedItems.length})`}</Button>
                <Button type="button" size="small" disabled={archived || !items.some((item) => item.readAt) || archiveRead.isPending} onClick={archiveAllRead}><i className="bi bi-check2-all" aria-hidden="true" />Archive all read</Button>
              </div>
            </details>
          </div>
        </div>
        <div className="inbox-list">
          {inbox.isLoading && <p className="oc-empty" role="status">Loading inbox…</p>}
          {inbox.isError && <p className="oc-empty" role="alert">Could not load inbox.</p>}
          {inbox.isSuccess && !visibleItems.length && <EmptyState>{items.length ? 'No messages match these filters.' : archived ? 'No archived messages.' : 'Your inbox is empty.'}</EmptyState>}
          {visibleItems.map((item) => <article key={itemKey(item)} className={`inbox-message${item.readAt ? '' : ' unread'}${activeKey === itemKey(item) ? ' active' : ''}`}>
            {/* The category icon doubles as the selection checkbox: clicking it
                swaps in a check mark, so no separate checkbox column is needed. */}
            <button type="button" role="checkbox" className="inbox-select" disabled={archived} aria-checked={selected.has(itemKey(item))} onClick={() => toggle(item)} aria-label={`Select ${item.title}`}>
              <i className={`bi bi-${selected.has(itemKey(item)) ? 'check-square-fill' : categories.find(({ id }) => id === itemCategory(item))?.icon ?? 'chat-left-text'}`} aria-hidden="true" />
            </button>
            <button type="button" className="inbox-message-open" onClick={() => open(item)} aria-current={activeKey === itemKey(item) ? 'true' : undefined}>
              <span className="inbox-headline">
                <span className="inbox-subject">{!item.readAt && <span className="inbox-unread-dot" aria-label="Unread" />}{item.title}</span>
                <span className="inbox-meta"><RelativeTime iso={new Date(item.createdAt).toISOString()} /></span>
              </span>
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
             <Button type="button" size="small" disabled={archived || !activeItem.readAt || markRead.isPending || markUnread.isPending} onClick={() => markUnread.mutate({ id: activeItem.id, remoteId: activeItem.remoteId }, { onSuccess: () => setActiveKey(null) })}><i className="bi bi-envelope" aria-hidden="true" />Mark unread</Button>
             <Button type="button" size="small" disabled={archived || archive.isPending} onClick={() => archive.mutate([{ id: activeItem.id, remoteId: activeItem.remoteId }])}><i className="bi bi-archive" aria-hidden="true" />Archive message</Button>
            </div>
          </div>
          <article className="inbox-letter" key={itemKey(activeItem)}>
            <div className="inbox-letter-header" data-testid="inbox-message-header">
              <div className="inbox-letter-subject" role="heading" aria-level={3}><MarkdownContent text={activeItem.title} /></div>
              <div className="inbox-meta">{activeSession
                ? <span>Originating session: <Link to={`/session/${encodeURIComponent(activeSession.sessionId)}?platform=${encodeURIComponent(activeSession.platform)}`}>{activeSession.title || activeSession.sessionId}</Link></span>
                : <span>Originating session unavailable</span>}</div>
              <div className="inbox-meta"><span>{sourceLabel(activeItem.remoteId)}</span><time dateTime={new Date(activeItem.createdAt).toISOString()}>{new Date(activeItem.createdAt).toLocaleString()}</time></div>
            </div>
            <div className="inbox-body"><MarkdownContent text={activeItem.body} preserveLineBreaks /></div>
            {activeItem.permission && !activeItem.archivedAt && <InboxPermissionActions permission={activeItem.permission} />}
          </article>
        </> : <div className="inbox-reader-empty"><i className="bi bi-envelope-open" aria-hidden="true" /><h3>Select a message</h3><p>Choose a message from your inbox to read it here.</p></div>}
      </section>
    </div>
  </main>;
}

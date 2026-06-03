import { useState } from 'react';
import { messageBookmarkKey, type MessageBookmark, type MessageBookmarkGroup } from '../lib/messageBookmarks';

export function MessageBookmarksPane({
  groups,
  selectedKey,
  onRemove,
  onScrollToMessage,
}: {
  groups: MessageBookmarkGroup[];
  selectedKey: string | null;
  onRemove: (bookmark: MessageBookmark) => void;
  onScrollToMessage: (bookmark: MessageBookmark) => void;
}) {
  if (groups.length === 0) {
    return <div className="oc-bookmarks-empty">No bookmarked messages yet.</div>;
  }

  return (
    <div className="oc-bookmarks-pane">
      <div className="oc-bookmarks-groups">
        {groups.map((group) => (
          <section key={group.projectDirectory} className="oc-bookmarks-group">
            <div className="oc-bookmarks-group-header">
              <span>{group.label}</span>
              {group.current && <span className="oc-bookmarks-current">Current</span>}
            </div>
            {group.bookmarks.map((bookmark) => (
              <BookmarkRow
                key={messageBookmarkKey(bookmark)}
                bookmark={bookmark}
                selectedKey={selectedKey}
                onRemove={onRemove}
                onScrollToMessage={onScrollToMessage}
              />
            ))}
          </section>
        ))}
      </div>
    </div>
  );
}

interface BookmarkRowProps {
  bookmark: MessageBookmark;
  selectedKey: string | null;
  onRemove: (bookmark: MessageBookmark) => void;
  onScrollToMessage: (bookmark: MessageBookmark) => void;
}

// Row layout:
//   - Header (meta + inline action buttons)
//   - Preview body. Collapsed by default to a short clamp; clicking
//     the row toggles expansion to show the full preview.
//   - The "go to" and "remove" buttons live inline in the header so
//     the user never needs to expand the row to act on it. Stopping
//     propagation on those buttons keeps clicks from also toggling
//     the expanded state.
function BookmarkRow({ bookmark, selectedKey, onRemove, onScrollToMessage }: BookmarkRowProps) {
  const [expanded, setExpanded] = useState(false);
  const key = messageBookmarkKey(bookmark);
  const active = key === selectedKey;

  const toggleExpanded = () => setExpanded((v) => !v);

  return (
    <div
      className={`oc-bookmark-row${active ? ' active' : ''}${expanded ? ' expanded' : ''}`}
      role="button"
      tabIndex={0}
      aria-expanded={expanded}
      onClick={toggleExpanded}
      onKeyDown={(e) => {
        if (e.key === 'Enter' || e.key === ' ') {
          e.preventDefault();
          toggleExpanded();
        }
      }}
    >
      <div className="oc-bookmark-row-header">
        <span className="oc-bookmark-row-meta">
          <span>{bookmark.role}</span>
          {bookmark.sessionTitle && <span>{bookmark.sessionTitle}</span>}
        </span>
        <span className="oc-bookmark-row-actions">
          <button
            type="button"
            className="oc-bookmark-row-action"
            onClick={(e) => {
              e.stopPropagation();
              onScrollToMessage(bookmark);
            }}
            aria-label="Go to bookmark"
            title="Go to bookmark"
          >
            <i className="bi bi-arrow-right-circle" aria-hidden="true" />
          </button>
          <button
            type="button"
            className="oc-bookmark-row-action oc-bookmark-row-remove"
            onClick={(e) => {
              e.stopPropagation();
              onRemove(bookmark);
            }}
            aria-label="Remove bookmark"
            title="Remove bookmark"
          >
            <i className="bi bi-trash" aria-hidden="true" />
          </button>
        </span>
      </div>
      <span className="oc-bookmark-row-preview">{bookmark.preview || 'Empty message'}</span>
    </div>
  );
}

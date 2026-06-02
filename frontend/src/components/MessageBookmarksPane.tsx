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
            {group.bookmarks.map((bookmark) => {
              const key = messageBookmarkKey(bookmark);
              return (
                <div
                  key={key}
                  className={`oc-bookmark-row${key === selectedKey ? ' active' : ''}`}
                  role="button"
                  tabIndex={0}
                  onClick={() => onScrollToMessage(bookmark)}
                  onKeyDown={(e) => {
                    if (e.key === 'Enter' || e.key === ' ') {
                      e.preventDefault();
                      onScrollToMessage(bookmark);
                    }
                  }}
                >
                  <span className="oc-bookmark-row-meta">
                    <span>{bookmark.role}</span>
                    {bookmark.sessionTitle && <span>{bookmark.sessionTitle}</span>}
                  </span>
                  <span className="oc-bookmark-row-preview">{bookmark.preview || 'Empty message'}</span>
                  <button
                    type="button"
                    className="oc-bookmark-row-remove"
                    onClick={(e) => {
                      e.stopPropagation();
                      onRemove(bookmark);
                    }}
                    aria-label="Remove bookmark"
                    title="Remove bookmark"
                  >
                    <i className="bi bi-trash" aria-hidden="true" />
                  </button>
                </div>
              );
            })}
          </section>
        ))}
      </div>
    </div>
  );
}

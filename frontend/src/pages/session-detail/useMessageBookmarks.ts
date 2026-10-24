// useMessageBookmarks owns the per-session message-bookmark state:
// localStorage-backed bookmark list, selection, toggle/remove
// handlers, and the cross-project grouping shown in the sidebar.
// Scroll-to-bookmark state stays in SessionDetail because it must
// exist before useSession runs (it feeds protectedMessageId).
import { useState, useEffect } from 'react';
import type { Message, Part, Session } from '../../lib/api';
import { onSessionBrowserStateAdopted } from '../../lib/sessionIdentityState';
import {
  buildMessageBookmarks,
  groupMessageBookmarks,
  loadAllMessageBookmarks,
  loadMessageBookmarks,
  messageBookmarkKey,
  saveMessageBookmarks,
  type MessageBookmark,
} from '../../lib/messageBookmarks';

interface MessageBookmarkState {
  sessionId: string | undefined;
  bookmarks: MessageBookmark[];
  selectedKey: string | null;
}

export function useMessageBookmarks({
  id,
  session,
  messages,
  parts,
  recentSessions,
}: {
  id: string | undefined;
  session: Session | null | undefined;
  messages: Message[];
  parts: Part[];
  recentSessions: Session[];
}) {
  const currentMessageBookmarks = buildMessageBookmarks(messages, parts, {
    sessionId: session?.id ?? id,
    directory: session?.directory,
    sessionTitle: session?.title,
  });

  const loadedMessageBookmarks = loadMessageBookmarks(id);
  const [messageBookmarkState, setMessageBookmarkState] = useState<MessageBookmarkState>(() => ({
    sessionId: id,
    bookmarks: loadedMessageBookmarks,
    selectedKey: loadedMessageBookmarks[0] ? messageBookmarkKey(loadedMessageBookmarks[0]) : null,
  }));
  const [messageBookmarkStorageTick, setMessageBookmarkStorageTick] = useState(0);

  useEffect(() => onSessionBrowserStateAdopted((adoptedId) => {
    if (adoptedId !== id) return;
    const bookmarks = loadMessageBookmarks(id);
    setMessageBookmarkState((current) => ({
      sessionId: id, bookmarks,
      selectedKey: current.selectedKey || (bookmarks[0] ? messageBookmarkKey(bookmarks[0]) : null),
    }));
    setMessageBookmarkStorageTick((tick) => tick + 1);
  }), [id]);

  const activeMessageBookmarkState = messageBookmarkState.sessionId === id
    ? messageBookmarkState
    : {
      sessionId: id,
      bookmarks: loadedMessageBookmarks,
      selectedKey: loadedMessageBookmarks[0] ? messageBookmarkKey(loadedMessageBookmarks[0]) : null,
    };
  const messageBookmarks = activeMessageBookmarkState.bookmarks;
  const selectedMessageBookmarkKey = activeMessageBookmarkState.selectedKey;

  const stateForActiveSession = (state: MessageBookmarkState): MessageBookmarkState => {
    if (state.sessionId === id) return state;
    return {
      sessionId: id,
      bookmarks: loadedMessageBookmarks,
      selectedKey: loadedMessageBookmarks[0] ? messageBookmarkKey(loadedMessageBookmarks[0]) : null,
    };
  };

  useEffect(() => {
    saveMessageBookmarks(id, messageBookmarks);
  }, [id, messageBookmarks]);

  const bookmarkedMessageIds = new Set(messageBookmarks.map((bookmark) => bookmark.id));

  const handleToggleMessageBookmark = (messageId: string) => {
    setMessageBookmarkState((current) => {
      const state = stateForActiveSession(current);
      if (state.bookmarks.some((bookmark) => bookmark.id === messageId)) {
        const bookmarks = state.bookmarks.filter((bookmark) => bookmark.id !== messageId);
        return {
          ...state,
          bookmarks,
          selectedKey: state.selectedKey === messageBookmarkKey({ sessionId: state.sessionId || '', id: messageId })
            ? (bookmarks[0] ? messageBookmarkKey(bookmarks[0]) : null)
            : state.selectedKey,
        };
      }
      const bookmark = currentMessageBookmarks.get(messageId);
      if (!bookmark) return state;
      return {
        ...state,
        bookmarks: [...state.bookmarks, bookmark],
        selectedKey: messageBookmarkKey(bookmark),
      };
    });
  };

  const handleRemoveMessageBookmark = (bookmark: MessageBookmark) => {
    if (bookmark.sessionId !== id) {
      const key = messageBookmarkKey(bookmark);
      const bookmarks = loadMessageBookmarks(bookmark.sessionId)
        .filter((item) => messageBookmarkKey(item) !== key);
      saveMessageBookmarks(bookmark.sessionId, bookmarks);
      setMessageBookmarkStorageTick((current) => current + 1);
      setMessageBookmarkState((current) => (
        current.selectedKey === key ? { ...current, selectedKey: null } : current
      ));
      return;
    }
    setMessageBookmarkState((current) => {
      const state = stateForActiveSession(current);
      const key = messageBookmarkKey(bookmark);
      const bookmarks = state.bookmarks.filter((item) => messageBookmarkKey(item) !== key);
      return {
        ...state,
        bookmarks,
        selectedKey: state.selectedKey === key ? (bookmarks[0] ? messageBookmarkKey(bookmarks[0]) : null) : state.selectedKey,
      };
    });
  };

  const allMessageBookmarks = loadAllMessageBookmarks(id, messageBookmarks, messageBookmarkStorageTick);
  const messageBookmarkGroups = groupMessageBookmarks(
    allMessageBookmarks,
    session?.directory,
    [session, ...recentSessions].filter((s): s is Session => !!s),
  );

  return {
    bookmarkedMessageIds,
    selectedMessageBookmarkKey,
    messageBookmarkGroups,
    handleToggleMessageBookmark,
    handleRemoveMessageBookmark,
  };
}

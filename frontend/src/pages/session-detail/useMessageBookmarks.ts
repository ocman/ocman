// useMessageBookmarks owns the per-session message-bookmark state:
// localStorage-backed bookmark list, selection, toggle/remove
// handlers, and the cross-project grouping shown in the sidebar.
// Scroll-to-bookmark state stays in SessionDetail because it must
// exist before useSession runs (it feeds protectedMessageId).
import { useState, useEffect, useCallback, useMemo } from 'react';
import type { Message, Part, Session } from '../../lib/api';
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
  const currentMessageBookmarks = useMemo(() => buildMessageBookmarks(messages, parts, {
    sessionId: session?.id ?? id,
    directory: session?.directory,
    sessionTitle: session?.title,
  }), [messages, parts, session?.id, session?.directory, session?.title, id]);

  const loadedMessageBookmarks = useMemo(() => loadMessageBookmarks(id), [id]);
  const [messageBookmarkState, setMessageBookmarkState] = useState<MessageBookmarkState>(() => ({
    sessionId: id,
    bookmarks: loadedMessageBookmarks,
    selectedKey: loadedMessageBookmarks[0] ? messageBookmarkKey(loadedMessageBookmarks[0]) : null,
  }));
  const [messageBookmarkStorageTick, setMessageBookmarkStorageTick] = useState(0);

  const activeMessageBookmarkState = messageBookmarkState.sessionId === id
    ? messageBookmarkState
    : {
      sessionId: id,
      bookmarks: loadedMessageBookmarks,
      selectedKey: loadedMessageBookmarks[0] ? messageBookmarkKey(loadedMessageBookmarks[0]) : null,
    };
  const messageBookmarks = activeMessageBookmarkState.bookmarks;
  const selectedMessageBookmarkKey = activeMessageBookmarkState.selectedKey;

  const stateForActiveSession = useCallback((state: MessageBookmarkState): MessageBookmarkState => {
    if (state.sessionId === id) return state;
    return {
      sessionId: id,
      bookmarks: loadedMessageBookmarks,
      selectedKey: loadedMessageBookmarks[0] ? messageBookmarkKey(loadedMessageBookmarks[0]) : null,
    };
  }, [id, loadedMessageBookmarks]);

  useEffect(() => {
    saveMessageBookmarks(id, messageBookmarks);
  }, [id, messageBookmarks]);

  const bookmarkedMessageIds = useMemo(
    () => new Set(messageBookmarks.map((bookmark) => bookmark.id)),
    [messageBookmarks],
  );

  const handleToggleMessageBookmark = useCallback((messageId: string) => {
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
  }, [currentMessageBookmarks, stateForActiveSession]);

  const handleRemoveMessageBookmark = useCallback((bookmark: MessageBookmark) => {
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
  }, [id, stateForActiveSession]);

  const allMessageBookmarks = useMemo(
    () => loadAllMessageBookmarks(id, messageBookmarks, messageBookmarkStorageTick),
    [id, messageBookmarks, messageBookmarkStorageTick],
  );
  const messageBookmarkGroups = useMemo(
    () => groupMessageBookmarks(
      allMessageBookmarks,
      session?.directory,
      [session, ...recentSessions].filter((s): s is Session => !!s),
    ),
    [allMessageBookmarks, session, recentSessions],
  );

  return {
    bookmarkedMessageIds,
    selectedMessageBookmarkKey,
    messageBookmarkGroups,
    handleToggleMessageBookmark,
    handleRemoveMessageBookmark,
  };
}

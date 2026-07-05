// Message bookmarks: types, localStorage persistence, preview
// building, and project grouping. The stateful side lives in
// pages/session-detail/useMessageBookmarks.ts.
import type { Message, Part, PartData, Session } from './api';
import { cleanTitle, shortPath } from './format';
import { projectRootForDirectory } from './worktrees';

export interface MessageBookmark {
  id: string;
  sessionId: string;
  role: string;
  preview: string;
  timeCreated: number;
  directory?: string;
  projectDirectory?: string;
  sessionTitle?: string;
}

export interface MessageBookmarkGroup {
  projectDirectory: string;
  label: string;
  current: boolean;
  bookmarks: MessageBookmark[];
}

export function messageBookmarkKey(bookmark: Pick<MessageBookmark, 'sessionId' | 'id'>) {
  return `${bookmark.sessionId}:${bookmark.id}`;
}

const MESSAGE_BOOKMARKS_STORAGE_PREFIX = 'ocman:message-bookmarks:';

function messageBookmarkStorage(): Storage | undefined {
  if (typeof window === 'undefined') return undefined;
  try {
    return window.localStorage ?? undefined;
  } catch {
    return undefined;
  }
}

function normalizeBookmarkRole(role: string | undefined) {
  if (!role) return 'Message';
  return role.charAt(0).toUpperCase() + role.slice(1);
}

function partData(part: Part): PartData | null {
  if (typeof part.data === 'string') {
    try {
      return JSON.parse(part.data) as PartData;
    } catch {
      return null;
    }
  }
  return part.data;
}

function summarizePart(part: Part) {
  const data = partData(part);
  if (!data) return '';
  if (typeof data.text === 'string' && data.text.trim()) return data.text.trim();
  if (data.type === 'tool' && data.tool) return `[tool: ${data.tool}]`;
  if (data.type === 'file' && data.filename) return `[file: ${data.filename}]`;
  return '';
}

export function buildMessageBookmarks(
  messages: Message[],
  parts: Part[],
  opts: { sessionId: string | undefined; directory: string | undefined; sessionTitle: string | undefined },
) {
  const partsByMessage = new Map<string, Part[]>();
  for (const part of parts) {
    const grouped = partsByMessage.get(part.messageId);
    if (grouped) grouped.push(part);
    else partsByMessage.set(part.messageId, [part]);
  }

  return new Map(messages.map((message) => {
    const preview = (partsByMessage.get(message.id) ?? [])
      .map(summarizePart)
      .filter(Boolean)
      .join(' ')
      .replace(/\s+/g, ' ')
      .slice(0, 220);
    const projectDirectory = projectRootForDirectory(opts.directory || '');
    return [message.id, {
      id: message.id,
      sessionId: opts.sessionId || message.sessionId,
      role: normalizeBookmarkRole(message.data.role),
      preview,
      timeCreated: message.timeCreated,
      directory: opts.directory,
      projectDirectory,
      sessionTitle: cleanTitle(opts.sessionTitle) || 'Untitled',
    } satisfies MessageBookmark];
  }));
}

export function loadMessageBookmarks(sessionId: string | undefined): MessageBookmark[] {
  if (!sessionId) return [];
  try {
    const storage = messageBookmarkStorage();
    if (!storage) return [];
    if (typeof storage.getItem !== 'function') return [];
    const raw = storage.getItem(`${MESSAGE_BOOKMARKS_STORAGE_PREFIX}${sessionId}`);
    if (!raw) return [];
    const parsed = JSON.parse(raw) as MessageBookmark[];
    if (!Array.isArray(parsed)) return [];
    return parsed
      .filter((bookmark) => bookmark && typeof bookmark.id === 'string')
      .map((bookmark) => ({ ...bookmark, sessionId: bookmark.sessionId || sessionId }));
  } catch {
    return [];
  }
}

export function saveMessageBookmarks(sessionId: string | undefined, bookmarks: MessageBookmark[]) {
  if (!sessionId) return;
  try {
    const storage = messageBookmarkStorage();
    if (!storage) return;
    const key = `${MESSAGE_BOOKMARKS_STORAGE_PREFIX}${sessionId}`;
    if (bookmarks.length === 0) {
      if (typeof storage.removeItem === 'function') storage.removeItem(key);
      return;
    }
    if (typeof storage.setItem === 'function') storage.setItem(key, JSON.stringify(bookmarks));
  } catch {
    return;
  }
}

export function loadAllMessageBookmarks(activeSessionId: string | undefined, activeBookmarks: MessageBookmark[], storageTick: number) {
  void storageTick;
  const storage = messageBookmarkStorage();
  if (!storage) return activeBookmarks;
  const bookmarks: MessageBookmark[] = [];
  try {
    if (typeof storage.length !== 'number' || typeof storage.key !== 'function') return activeBookmarks;
    for (let i = 0; i < storage.length; i++) {
      const key = storage.key(i);
      if (!key?.startsWith(MESSAGE_BOOKMARKS_STORAGE_PREFIX)) continue;
      const sessionId = key.slice(MESSAGE_BOOKMARKS_STORAGE_PREFIX.length);
      if (sessionId === activeSessionId) continue;
      bookmarks.push(...loadMessageBookmarks(sessionId));
    }
  } catch {
    return activeBookmarks;
  }
  return [...activeBookmarks, ...bookmarks];
}

export function groupMessageBookmarks(
  bookmarks: MessageBookmark[],
  currentDirectory: string | undefined,
  sessions: Session[],
): MessageBookmarkGroup[] {
  const sessionsById = new Map(sessions.map((session) => [session.id, session]));
  const currentProject = projectRootForDirectory(currentDirectory || '');
  const groups = new Map<string, MessageBookmarkGroup>();

  for (const bookmark of bookmarks) {
    const bookmarkSession = sessionsById.get(bookmark.sessionId);
    const directory = bookmark.directory || bookmarkSession?.directory;
    const projectDirectory = bookmark.projectDirectory || projectRootForDirectory(directory || '') || '(unknown)';
    const label = projectDirectory === '(unknown)' ? 'Unknown project' : shortPath(projectDirectory);
    const group = groups.get(projectDirectory) ?? {
      projectDirectory,
      label,
      current: projectDirectory === currentProject,
      bookmarks: [],
    };
    group.bookmarks.push({
      ...bookmark,
      directory,
      projectDirectory,
      sessionTitle: bookmark.sessionTitle || cleanTitle(bookmarkSession?.title) || 'Untitled',
    });
    groups.set(projectDirectory, group);
  }

  return Array.from(groups.values()).sort((a, b) => {
    if (a.current !== b.current) return a.current ? -1 : 1;
    return a.label.localeCompare(b.label);
  });
}

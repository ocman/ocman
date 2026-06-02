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

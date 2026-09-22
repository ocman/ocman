export interface InboxItem {
  id: string;
  title: string;
  body: string;
  createdAt: number;
  readAt?: number;
  archivedAt?: number;
  remoteId: string;
  category?: 'permission' | 'factory' | 'routine' | 'general';
  session?: {
    platform: string;
    sessionId: string;
    title?: string;
  };
  permission?: {
    platform: string;
    sessionId: string;
    permissionId: string;
    permission: string;
    patterns: string[];
    always?: string[];
    metadata?: Record<string, unknown>;
  };
}

export interface InboxResponse {
  items: InboxItem[];
  unreadTotal: number;
}

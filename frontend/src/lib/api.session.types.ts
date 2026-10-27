import type { SessionStatus } from './api.types';

export interface Session {
  id: string;
  /** Owning coding tool. Gate behavior on capabilities, never on this value. */
  platform: string;
  projectId: string;
  /** Native parent or an ocman child link; empty for top-level sessions. */
  parentId?: string;
  title: string;
  directory: string;
  timeCreated: number;
  timeUpdated: number;
  lastMessageAt?: number;
  branch?: string;
  origin?: 'user' | 'external' | 'routine' | 'factory' | 'plugin';
  originId?: string;
  /** Last terminal assistant turn, including errors; absent on older remotes. */
  lastTurnCompletedAt?: number;
  /** Latest accepted user prompt and permission/question halt. Unix milliseconds. */
  lastUserPromptAt?: number;
  lastHaltAt?: number;
  summaryAdditions: number | null;
  summaryDeletions: number | null;
  summaryFiles: number | null;
  shareUrl: string | null;
  messageCount: number;
  durationMs: number;
  /** Sum of assistant completed-created times, excluding user think time.
   * Populated by session detail; zero when unsupported or omitted by listing. */
  activeDurationMs: number;
  totalInputTokens: number;
  totalOutputTokens: number;
  totalCost: number;
  totalEstCost?: number;
  totalEffectiveCost?: number;
  status: SessionStatus;
  resetsAt?: number;
  liveConnection: boolean;
  pendingPermission: boolean;
  pendingQuestion: boolean;
  backgroundTaskCount?: number;
  backgroundServiceCount?: number;
  archived: boolean;
  snoozedUntil?: number;
  seen: boolean;
  pinned: boolean;
  pinnedAt: number;
  routineId?: string;
  factoryAttemptId?: string;
  /** Session timeUpdated when viewed. Used for first-unread markers and badges. */
  seenTimeUpdated: number;
  /** Messages newer than the read watermark; zero when suppressed or unsupported. */
  unreadCount: number;
  notice?: SessionNotice;
  projectDefaultModel?: string;
  /** Display-only owner attributes. Capabilities control host behavior. */
  remoteId?: string;
  remoteName?: string;
  stale?: boolean;
}

/** Normalized transient condition, without platform-specific behavior. */
export interface SessionNotice {
  kind: 'rate_limit' | 'provider_overloaded' | 'model_switch' | 'models_exhausted' | string;
  message: string;
  /** Unix ms when retry is expected, zero when unknown. */
  retryAt: number;
  attempt: number;
}

/**
 * A pending permission as `/api/session/{id}/permissions` returns it: the
 * platform's `permission.asked` properties. Fields beyond `id` vary by
 * platform; read it through `permissionFromRequest`.
 */
export interface PermissionRequest {
  id: string;
  sessionID?: string;
  permission?: string;
  patterns?: string[];
  always?: string[];
  metadata?: Record<string, unknown>;
}

/** A pending question as `/api/session/{id}/questions` returns it (`question.asked` properties). */
export interface QuestionRequest {
  id: string;
  sessionID?: string;
  questions?: unknown;
}

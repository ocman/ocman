import { fetchJSON, postJSON, queryString } from './api.requests';
import { settingsApi } from './api.settings';
import { sessionApi } from './api.sessions';
import { hostApi } from './api.host';
import type { CachedProjectSettings } from './projectSettingsCache';
import type { UsageDay } from './uiUsage';
import type {
  WebhookDelivery, WebhookInbox, WebhookSubscription, ClientActivity, Stats, MetricsPerformance,
  AnalyticsOverview, DatabaseSizeSample, SubscriptionUsageResponse, MetricsLog, MetricsLogKind,
  PermissionStats, Project, InboxResponse, InboxItem, FactoryEpic, CreateWorkEpicRequest,
  FactoryClaimedPlan, FactoryMaterialization, FactoryIssue, FactoryIssueComment, FactoryGraphMutation,
  FactoryQueueItem, FactoryRecoveryGate, FactoryAuthorityEscalationGate, FactoryProjectRequestGate,
  FactoryProposal, FactoryPlanGateDecisionRequest, FactoryPlanGate, FactoryFormula,
  FactoryFormulaValidationRequest, FactoryFormulaSaveRequest, FactoryCapacityPolicy, DoctorReport,
  McpConfigStatus, McpConfigInstallResult, ActivityDay, ModelUsage, FavoriteEntry, HourlyData,
  HourlyTokensByModel, CapabilitiesResponse, Routine, RoutineInput, RoutineRun, SystemStats, AuthMe,
} from './api.types';

// Keep existing consumers on the same public module.
export type * from './api.types';
export interface SessionConcurrency {
  bucketMs: number;
  series: { timestamp: number; sessions: number }[];
}
export { APIError, AuthError, BackendUnavailableError, fetchJSON, postJSON,
  registerAuthErrorHandler, raiseAuthError, raiseForUnauthorized } from './api.requests';

export function sessionExportMarkdownUrl(id: string): string {
  return `/api/session/${encodeURIComponent(id)}/export.md`;
}

export function sharedExportMarkdownUrl(token: string): string {
  return `/api/share/${encodeURIComponent(token)}/export.md`;
}

export const api = {
  ...settingsApi(fetchJSON, postJSON),
  ...sessionApi,
  ...hostApi,
  clientActivity: (activity: ClientActivity) =>
    postJSON<void, ClientActivity>('/api/client-activity', activity, { parseJSON: false }),
  stats: (signal?: AbortSignal) => fetchJSON<Stats>('/api/stats', signal),
  metrics: (params?: { agent?: string; model?: string; days?: number; dir?: string }, signal?: AbortSignal) =>
    fetchJSON<MetricsPerformance>(`/api/metrics/performance${queryString(params)}`, signal),
  analyticsOverview: (signal?: AbortSignal) => fetchJSON<AnalyticsOverview>('/api/analytics/overview', signal),
  sessionConcurrency: (params?: { days?: number; dir?: string }, signal?: AbortSignal) =>
    fetchJSON<SessionConcurrency>(`/api/analytics/session-concurrency${queryString(params)}`, signal),
  databaseSizes: (params?: { days?: number }, signal?: AbortSignal) =>
    fetchJSON<DatabaseSizeSample[]>(`/api/analytics/database-sizes${queryString(params)}`, signal),
  uiUsage: (days: number, signal?: AbortSignal) =>
    fetchJSON<UsageDay[]>(`/api/analytics/ui-usage${queryString({ days })}`, signal),
  agentRunHours: (params: { days?: number; dir?: string }, signal?: AbortSignal) =>
    fetchJSON<{ timestamp: number; minutes: number }[]>(`/api/analytics/agent-run-hours${queryString(params)}`, signal),
  subscriptionUsage: (signal?: AbortSignal) => fetchJSON<SubscriptionUsageResponse>('/api/subscription-usage', signal),
  metricLogs: (params: { kind: MetricsLogKind; agent?: string; model?: string; days?: number; limit?: number; offset?: number; sessionLimit?: number; sessionOffset?: number; projectLimit?: number; projectOffset?: number; dir?: string }, signal?: AbortSignal) =>
    fetchJSON<MetricsLog>(`/api/metric-logs${queryString(params)}`, signal),
  permissionStats: (params?: { days?: number; dir?: string }, signal?: AbortSignal) =>
    fetchJSON<PermissionStats>(`/api/permission-stats${queryString(params)}`, signal),
  projects: (signal?: AbortSignal) => fetchJSON<Project[]>('/api/projects', signal),
  factoryEpics: (signal?: AbortSignal) => fetchJSON<FactoryEpic[]>('/api/factory/epics', signal),
  inbox: (signal?: AbortSignal, archived = false) => fetchJSON<InboxResponse>(`/api/inbox${archived ? '?archived=true' : ''}`, signal),
  markInboxItemRead: (id: string, remoteId: string) =>
    postJSON<void, { id: string; remoteId: string }>('/api/inbox/open', { id, remoteId }, { parseJSON: false }),
  markInboxItemUnread: (id: string, remoteId: string) =>
    postJSON<void, { id: string; remoteId: string }>('/api/inbox/unread', { id, remoteId }, { parseJSON: false }),
  pinInboxItem: (id: string, remoteId: string, pinned: boolean) =>
    postJSON<void, { id: string; remoteId: string; pinned: boolean }>('/api/inbox/pin', { id, remoteId, pinned }, { parseJSON: false }),
  archiveInboxItems: (items: Pick<InboxItem, 'id' | 'remoteId'>[]) =>
    postJSON<void, { items: Pick<InboxItem, 'id' | 'remoteId'>[] }>('/api/inbox/archive', { items }, { parseJSON: false }),
  factoryEpic: (id: string, signal?: AbortSignal) => fetchJSON<FactoryEpic>(`/api/factory/epics/${encodeURIComponent(id)}`, signal),
  createFactoryEpic: (request: CreateWorkEpicRequest) => postJSON<FactoryEpic, CreateWorkEpicRequest>('/api/factory/epics', request),
  factoryClaimPlan: (id: string, issueID: string) =>
    postJSON<FactoryClaimedPlan, undefined>(`/api/factory/epics/${encodeURIComponent(id)}/plans/${encodeURIComponent(issueID)}`, undefined),
  factoryMaterialize: (id: string, issueID: string) =>
    postJSON<FactoryMaterialization, undefined>(`/api/factory/epics/${encodeURIComponent(id)}/materializations/${encodeURIComponent(issueID)}`, undefined),
  reopenFactoryIssue: (id: string, issueID: string) =>
    postJSON<unknown, undefined>(`/api/factory/epics/${encodeURIComponent(id)}/issues/${encodeURIComponent(issueID)}/reopen`, undefined),
  investigateFactoryUnblock: (id: string, issueID: string) =>
    postJSON<{ platform: string; id: string }, undefined>(`/api/factory/epics/${encodeURIComponent(id)}/issues/${encodeURIComponent(issueID)}/unblock`, undefined),
  factoryIssues: (id: string, signal?: AbortSignal) => fetchJSON<FactoryIssue[]>(`/api/factory/epics/${encodeURIComponent(id)}/issues`, signal),
  factoryIssueComments: (epicID: string, issueID: string, signal?: AbortSignal) =>
    fetchJSON<FactoryIssueComment[]>(`/api/factory/epics/${encodeURIComponent(epicID)}/issues/${encodeURIComponent(issueID)}/comments`, signal),
  addFactoryIssueComment: (epicID: string, issueID: string, body: string) =>
    postJSON<FactoryIssueComment, { body: string }>(`/api/factory/epics/${encodeURIComponent(epicID)}/issues/${encodeURIComponent(issueID)}/comments`, { body }),
  factoryRemovedIssues: (id: string, signal?: AbortSignal) => fetchJSON<FactoryIssue[]>(`/api/factory/epics/${encodeURIComponent(id)}/removed-issues`, signal),
  mutateFactoryGraph: (id: string, mutation: FactoryGraphMutation) =>
    postJSON<void, FactoryGraphMutation>(`/api/factory/epics/${encodeURIComponent(id)}/mutations`, mutation, { parseJSON: false }),
  factoryQueue: (signal?: AbortSignal) => fetchJSON<FactoryQueueItem[]>('/api/factory/queue', signal),
  resolveFactoryRecoveryGate: (id: string, action: 'resume' | 'retry' | 'cancel', response: string) =>
    postJSON<FactoryRecoveryGate, { response: string }>(`/api/factory/recovery-gates/${encodeURIComponent(id)}/${action}`, { response }),
  resolveFactoryAuthorityGate: (id: string, action: 'approve' | 'reject') =>
    postJSON<FactoryAuthorityEscalationGate, Record<string, never>>(`/api/factory/authority-gates/${encodeURIComponent(id)}/${action}`, {}),
  resolveFactoryProjectGate: (id: string, action: 'approve' | 'reject', response: string, acknowledgeLocalExecution: boolean) =>
    postJSON<FactoryProjectRequestGate, { response: string; acknowledgeLocalExecution: boolean }>(`/api/factory/project-gates/${encodeURIComponent(id)}/${action}`, { response, acknowledgeLocalExecution }),
  factoryProposals: (id: string, signal?: AbortSignal) => fetchJSON<FactoryProposal[]>(`/api/factory/epics/${encodeURIComponent(id)}/proposals`, signal),
  factoryPlanGate: (id: string, action: 'approve' | 'revise' | 'reject', request: FactoryPlanGateDecisionRequest) =>
    postJSON<FactoryPlanGate, FactoryPlanGateDecisionRequest>(`/api/factory/epics/${encodeURIComponent(id)}/plan-gate/${action}`, request),
  factoryCloseMol: (id: string, molID: string) =>
    postJSON<void, undefined>(`/api/factory/epics/${encodeURIComponent(id)}/mols/${encodeURIComponent(molID)}/close`, undefined, { parseJSON: false }),
  factoryCloseEpic: (id: string, force: boolean) =>
    postJSON<void, undefined>(`/api/factory/epics/${encodeURIComponent(id)}/close${force ? '?force=true' : ''}`, undefined, { parseJSON: false }),
  factorySetEpicPaused: (id: string, paused: boolean) =>
    postJSON<void, undefined>(`/api/factory/epics/${encodeURIComponent(id)}/${paused ? 'pause' : 'resume'}`, undefined, { parseJSON: false }),
  factoryFormula: (id: string, version: number, signal?: AbortSignal) => fetchJSON<FactoryFormula>(`/api/factory/formulas/${encodeURIComponent(id)}/${version}`, signal),
  factoryFormulas: (signal?: AbortSignal) => fetchJSON<FactoryFormula[]>('/api/factory/formulas', signal),
  validateFactoryFormula: (request: FactoryFormulaValidationRequest) => postJSON<FactoryFormula, FactoryFormulaValidationRequest>('/api/factory/formulas/validate', request),
  previewFactoryFormula: (request: FactoryFormulaValidationRequest) => postJSON<FactoryFormula, FactoryFormulaValidationRequest>('/api/factory/formulas/preview', request),
  saveFactoryFormula: (request: FactoryFormulaSaveRequest) => postJSON<FactoryFormula, FactoryFormulaSaveRequest>('/api/factory/formulas', request),
  factoryCapacityPolicy: (signal?: AbortSignal) => fetchJSON<FactoryCapacityPolicy>('/api/factory/configuration', signal),
  setFactoryCapacityPolicy: (policy: FactoryCapacityPolicy) => postJSON<FactoryCapacityPolicy, FactoryCapacityPolicy>('/api/factory/configuration', policy),
  getDoctor: (signal?: AbortSignal) => fetchJSON<DoctorReport>('/api/doctor', signal),
  getMcpConfig: (signal?: AbortSignal) => fetchJSON<McpConfigStatus>('/api/mcp/config', signal),
  installMcpConfig: () => postJSON<McpConfigInstallResult>('/api/mcp/config/install', undefined),
  webhookInboxes: {
    list: (signal?: AbortSignal) => fetchJSON<WebhookInbox[]>('/api/webhook-inboxes', signal),
    create: (input: { name: string; enrollmentToken?: string; secret?: string; secretHeader?: string }) => postJSON<WebhookInbox>('/api/webhook-inboxes', input),
    update: (id: string, input: { name?: string; secret?: string; secretHeader?: string }) => postJSON<WebhookInbox>(`/api/webhook-inboxes/${encodeURIComponent(id)}`, input, { method: 'PATCH' }),
    rotate: (id: string, input: { reset: boolean }) => postJSON<{ keyVersion: number }>(`/api/webhook-inboxes/${encodeURIComponent(id)}`, input, { method: 'PUT' }),
    revoke: (id: string) => postJSON<void>(`/api/webhook-inboxes/${encodeURIComponent(id)}`, undefined, { method: 'DELETE', parseJSON: false }),
    deliveries: (id: string, signal?: AbortSignal) => fetchJSON<WebhookDelivery[]>(`/api/webhook-inboxes/${encodeURIComponent(id)}/deliveries`, signal),
    subscribe: (id: string, input: { routineId: string; headerPredicates: string; jsonPredicates: string }) => postJSON<WebhookSubscription>(`/api/webhook-inboxes/${encodeURIComponent(id)}/subscriptions`, input, { method: 'PUT' }),
    redeliver: (id: string, deliveryId: string) => postJSON<{ deliveryId: string }>(`/api/webhook-inboxes/${encodeURIComponent(id)}/redeliver`, { deliveryId }),
    unsubscribe: (id: string, routineId: string) => postJSON<void>(`/api/webhook-inboxes/${encodeURIComponent(id)}/subscriptions`, { routineId }, { method: 'DELETE', parseJSON: false }),
  },
  // Project ownership accompanies archives to avoid mutating the hub's copy.
  archiveProject: (directory: string, archived = true, remoteId?: string) => postJSON<{ ok: boolean }>('/api/project/archive', { directory, archived, remoteId }),
  calcCost: (req: { modelID: string; input: number; output: number; cacheRead: number; cacheWrite: number }) => postJSON<{ cost: number; known: boolean }>('/api/cost/calc', req),
  activity: (params?: { days?: number; model?: string; dir?: string }, signal?: AbortSignal) => fetchJSON<ActivityDay[]>(`/api/activity${queryString(params)}`, signal),
  models: (params?: { days?: number; dir?: string }, signal?: AbortSignal) => fetchJSON<ModelUsage[]>(`/api/models${queryString(params)}`, signal),
  projectSettings: (dir: string, signal?: AbortSignal, remoteId = 'local') => fetchJSON<CachedProjectSettings>(`/api/project/settings${queryString({ dir, remoteId })}`, signal),
  setProjectSettings: (directory: string, models: string[], off: boolean, remoteId = 'local') => postJSON<{ ok: boolean }>('/api/project/settings', { directory, models, off, remoteId }),
  listFavorites: (platform: string) => fetchJSON<FavoriteEntry[]>(`/api/favorites?platform=${encodeURIComponent(platform)}`),
  addFavorite: (platform: string, provider: string, model: string) => postJSON<void>('/api/favorites', { platform, provider, model }, { parseJSON: false }),
  removeFavorite: (platform: string, provider: string, model: string) => postJSON<void>('/api/favorites', { platform, provider, model }, { method: 'DELETE', parseJSON: false }),
  hourly: (params?: { days?: number; dir?: string }, signal?: AbortSignal) => fetchJSON<HourlyData[]>(`/api/hourly${queryString(params)}`, signal),
  hourlyTokens: (params?: { days?: number; model?: string; dir?: string }, signal?: AbortSignal) => fetchJSON<HourlyTokensByModel[]>(`/api/hourly-tokens${queryString(params)}`, signal),
  capabilities: (signal?: AbortSignal) => fetchJSON<CapabilitiesResponse>('/api/capabilities', signal),
  routines: {
    stats: (id: string, signal?: AbortSignal) => fetchJSON<RoutineStatsData>(`/api/routines/${encodeURIComponent(id)}/stats`, signal),
    list: (signal?: AbortSignal) => fetchJSON<Routine[]>('/api/routines', signal),
    create: (input: RoutineInput) => postJSON<Routine, RoutineInput>('/api/routines', input),
    update: (id: string, input: RoutineInput) => postJSON<Routine, RoutineInput>(`/api/routines/${encodeURIComponent(id)}`, input, { method: 'PUT' }),
    remove: (id: string) => postJSON<void, undefined>(`/api/routines/${encodeURIComponent(id)}`, undefined, { method: 'DELETE', parseJSON: false }),
    run: (id: string) => postJSON<RoutineRun, undefined>(`/api/routines/${encodeURIComponent(id)}/run`, undefined),
    history: (id: string, page: { limit: number; before?: Pick<RoutineRun, 'createdAt' | 'id'> }, signal?: AbortSignal) => {
      const query = new URLSearchParams({ limit: String(page.limit) });
      if (page.before) { query.set('beforeCreatedAt', String(page.before.createdAt)); query.set('beforeId', page.before.id); }
      return fetchJSON<RoutineRun[]>(`/api/routines/${encodeURIComponent(id)}/history?${query}`, signal);
    },
  },
  // Best-effort logging must never interrupt the caller.
  debugLog: async (level: 'debug' | 'info' | 'warn' | 'error', message: string, data?: unknown): Promise<void> => {
    try {
      await fetch('/api/debug/log', { method: 'POST', headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ level, message, data }), keepalive: true });
    } catch { /* Deliberately ignored. */ }
  },
  async systemStats(signal?: AbortSignal): Promise<SystemStats> { return fetchJSON<SystemStats>('/api/system/stats', signal); },
  authMe: () => fetchJSON<AuthMe>('/api/auth/me'),
  authLogin: (password: string) => postJSON<{ ok: boolean }, { password: string }>('/api/auth/login', { password }),
  authLogout: () => postJSON<void, Record<string, never>>('/api/auth/logout', {}, { parseJSON: false }),
  getPromptSections: () => fetchJSON<Array<{ title: string; content: string; enabled?: boolean }>>('/api/settings/prompt-sections'),
  setPromptSections: (sections: Array<{ title: string; content: string; enabled?: boolean }>): Promise<void> => postJSON<void>('/api/settings/prompt-sections', sections, { parseJSON: false }),
  getJudgeDelay: () => fetchJSON<{ delayMs: number }>('/api/settings/judge-delay').then((r) => r.delayMs),
  setJudgeDelay: (delayMs: number): Promise<void> => postJSON<void>('/api/settings/judge-delay', { delayMs }, { parseJSON: false }),
  getJudgeModel: () => fetchJSON<{ model: string }>('/api/settings/judge-model').then((r) => r.model),
  setJudgeModel: (model: string): Promise<void> => postJSON<void>('/api/settings/judge-model', { model }, { parseJSON: false }),
  getJudgeModelOptions: (signal?: AbortSignal) => fetchJSON<{ models: string[]; default: string }>('/api/settings/judge-model/options', signal),
};

export type RoutineStatsData = {
  totalRuns: number; states: Record<string, number>; averageDurationMs: number | null;
  totalCost: number; totalEstCost: number; costSessions: number; missingSessions: number;
};

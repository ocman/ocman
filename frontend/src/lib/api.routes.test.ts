import { afterEach, describe, expect, it, vi } from 'vitest';
import { api } from './api';

// Route contract for the thin API wrappers: each call must hit the
// backend route, verb and JSON body the Go handlers register. A typo
// here silently 404s in production, so every row pins all three.
type Call = { url: string; method: string; body: unknown };

function capture(): Call[] {
  const calls: Call[] = [];
  vi.stubGlobal('fetch', vi.fn((url: string, init?: RequestInit) => {
    calls.push({
      url,
      method: init?.method ?? 'GET',
      body: init?.body === undefined ? undefined : JSON.parse(String(init.body)),
    });
    return Promise.resolve(new Response('{"delayMs":7,"model":"m"}', { status: 200 }));
  }));
  return calls;
}

afterEach(() => vi.unstubAllGlobals());

const rows: Array<[string, () => Promise<unknown>, string, string, unknown?]> = [
  ['clientActivity', () => api.clientActivity({ active: true } as never), 'POST', '/api/client-activity', { active: true }],
  ['stats', () => api.stats(), 'GET', '/api/stats'],
  ['metrics', () => api.metrics({ days: 7, dir: '' }), 'GET', '/api/metrics/performance?days=7'],
  ['analyticsOverview', () => api.analyticsOverview(), 'GET', '/api/analytics/overview'],
  ['databaseSizes', () => api.databaseSizes(), 'GET', '/api/analytics/database-sizes'],
  ['subscriptionUsage', () => api.subscriptionUsage(), 'GET', '/api/subscription-usage'],
  ['providerAccounts', () => api.providerAccounts(), 'GET', '/api/provider-accounts'],
  ['metricLogs', () => api.metricLogs({ kind: 'tool' as never, limit: 5 }), 'GET', '/api/metric-logs?kind=tool&limit=5'],
  ['permissionStats', () => api.permissionStats({ days: 1 }), 'GET', '/api/permission-stats?days=1'],
  ['projects', () => api.projects(), 'GET', '/api/projects'],
  ['inbox archived', () => api.inbox(undefined, true), 'GET', '/api/inbox?archived=true'],
  ['markInboxItemRead', () => api.markInboxItemRead('i', 'local'), 'POST', '/api/inbox/open', { id: 'i', remoteId: 'local' }],
  ['markInboxItemUnread', () => api.markInboxItemUnread('i', 'local'), 'POST', '/api/inbox/unread', { id: 'i', remoteId: 'local' }],
  ['pinInboxItem', () => api.pinInboxItem('i', 'local', true), 'POST', '/api/inbox/pin', { id: 'i', remoteId: 'local', pinned: true }],
  ['archiveInboxItems', () => api.archiveInboxItems([{ id: 'i', remoteId: 'local' }]), 'POST', '/api/inbox/archive', { items: [{ id: 'i', remoteId: 'local' }] }],
  ['reopenFactoryIssue', () => api.reopenFactoryIssue('e/1', 'e.2'), 'POST', '/api/factory/epics/e%2F1/issues/e.2/reopen'],
  ['discussFactoryIssue', () => api.discussFactoryIssue('e', 'e.2'), 'POST', '/api/factory/epics/e/issues/e.2/discuss'],
  ['factoryRemovedIssues', () => api.factoryRemovedIssues('e'), 'GET', '/api/factory/epics/e/removed-issues'],
  ['mutateFactoryGraph', () => api.mutateFactoryGraph('e', { action: 'delete' } as never), 'POST', '/api/factory/epics/e/mutations', { action: 'delete' }],
  ['factoryQueue', () => api.factoryQueue(), 'GET', '/api/factory/queue'],
  ['factoryPlanGate', () => api.factoryPlanGate('e', 'revise', { revision: 2 } as never), 'POST', '/api/factory/epics/e/plan-gate/revise', { revision: 2 }],
  ['factoryCloseMol', () => api.factoryCloseMol('e', 'm'), 'POST', '/api/factory/epics/e/mols/m/close'],
  ['factoryCloseEpic force', () => api.factoryCloseEpic('e', true), 'POST', '/api/factory/epics/e/close?force=true'],
  ['factoryCloseEpic', () => api.factoryCloseEpic('e', false), 'POST', '/api/factory/epics/e/close'],
  ['factorySetEpicPaused pause', () => api.factorySetEpicPaused('e', true), 'POST', '/api/factory/epics/e/pause'],
  ['factorySetEpicPaused resume', () => api.factorySetEpicPaused('e', false), 'POST', '/api/factory/epics/e/resume'],
  ['factoryFormula', () => api.factoryFormula('ocman/tracer', 3), 'GET', '/api/factory/formulas/ocman%2Ftracer/3'],
  ['factoryFormulas', () => api.factoryFormulas(), 'GET', '/api/factory/formulas'],
  ['validateFactoryFormula', () => api.validateFactoryFormula({ source: 's' } as never), 'POST', '/api/factory/formulas/validate', { source: 's' }],
  ['previewFactoryFormula', () => api.previewFactoryFormula({ source: 's' } as never), 'POST', '/api/factory/formulas/preview', { source: 's' }],
  ['saveFactoryFormula', () => api.saveFactoryFormula({ id: 'x' } as never), 'POST', '/api/factory/formulas', { id: 'x' }],
  ['factoryCapacityPolicy', () => api.factoryCapacityPolicy(), 'GET', '/api/factory/configuration'],
  ['setFactoryCapacityPolicy', () => api.setFactoryCapacityPolicy({ globalCapacity: 2 } as never), 'POST', '/api/factory/configuration', { globalCapacity: 2 }],
  ['browseDirectories', () => api.browseDirectories('/r'), 'GET', '/api/filesystem/directories?dir=%2Fr'],
  ['searchDirectories', () => api.searchDirectories('/r', 'oc', 5), 'GET', '/api/filesystem/directory-search?q=oc&root=%2Fr&limit=5'],
  ['searchDirectories bare', () => api.searchDirectories(undefined, 'oc'), 'GET', '/api/filesystem/directory-search?q=oc'],
  ['sessionsNotify', () => api.sessionsNotify({ limit: 3 }), 'GET', '/api/sessions/notify?limit=3'],
  ['session peek', () => api.session('s', 10, 20, undefined, 'claudecode', true), 'GET', '/api/session/s?limit=10&offset=20&platform=claudecode&peek=1'],
  ['listShareLinks', () => api.listShareLinks('s'), 'GET', '/api/session/s/shares'],
  ['createShareLink', () => api.createShareLink('s'), 'POST', '/api/session/s/share'],
  ['revokeShareLink', () => api.revokeShareLink('s', 't'), 'DELETE', '/api/session/s/share/t'],
  ['getDoctor', () => api.getDoctor(), 'GET', '/api/doctor'],
  ['getMcpConfig', () => api.getMcpConfig(), 'GET', '/api/mcp/config'],
  ['installMcpConfig', () => api.installMcpConfig(), 'POST', '/api/mcp/config/install'],
  ['hookPlugin', () => api.hookPlugin(), 'GET', '/api/hook-plugin'],
  ['setHookPlugin install', () => api.setHookPlugin(true), 'POST', '/api/hook-plugin/install'],
  ['setHookPlugin uninstall', () => api.setHookPlugin(false), 'POST', '/api/hook-plugin/uninstall'],
  ['webhookInboxes.list', () => api.webhookInboxes.list(), 'GET', '/api/webhook-inboxes'],
  ['webhookInboxes.create', () => api.webhookInboxes.create({ name: 'n' }), 'POST', '/api/webhook-inboxes', { name: 'n' }],
  ['webhookInboxes.update', () => api.webhookInboxes.update('w', { name: 'n' }), 'PATCH', '/api/webhook-inboxes/w', { name: 'n' }],
  ['webhookInboxes.rotate', () => api.webhookInboxes.rotate('w', { reset: true }), 'PUT', '/api/webhook-inboxes/w', { reset: true }],
  ['webhookInboxes.revoke', () => api.webhookInboxes.revoke('w'), 'DELETE', '/api/webhook-inboxes/w'],
  ['webhookInboxes.deliveries', () => api.webhookInboxes.deliveries('w'), 'GET', '/api/webhook-inboxes/w/deliveries'],
  ['webhookInboxes.subscribe', () => api.webhookInboxes.subscribe('w', { routineId: 'r', headerPredicates: '', jsonPredicates: '' }), 'PUT', '/api/webhook-inboxes/w/subscriptions', { routineId: 'r', headerPredicates: '', jsonPredicates: '' }],
  ['webhookInboxes.redeliver', () => api.webhookInboxes.redeliver('w', 'd'), 'POST', '/api/webhook-inboxes/w/redeliver', { deliveryId: 'd' }],
  ['webhookInboxes.unsubscribe', () => api.webhookInboxes.unsubscribe('w', 'r'), 'DELETE', '/api/webhook-inboxes/w/subscriptions', { routineId: 'r' }],
  ['listAllShares', () => api.listAllShares(), 'GET', '/api/shares'],
  ['sharedConversation', () => api.sharedConversation('t'), 'GET', '/api/share/t'],
  ['sessionChanges', () => api.sessionChanges('s'), 'GET', '/api/session/s/changes'],
  ['sessionTasks', () => api.sessionTasks('s', ['a', 'b/c']), 'GET', '/api/session/s/tasks?ids=a,b%2Fc'],
  ['gitDiff fresh', () => api.gitDiff('/r', { fresh: true }), 'GET', '/api/git/diff?dir=%2Fr&fresh=1'],
  ['repoFiles ignored', () => api.repoFiles('/r', 'local', undefined, true), 'GET', '/api/git/files?dir=%2Fr&remoteId=local&ignored=1'],
  ['repoFile', () => api.repoFile('/r', 'a b', 'local'), 'GET', '/api/git/file?dir=%2Fr&path=a%20b&remoteId=local'],
  ['archiveSession', () => api.archiveSession('claudecode', 's', 9), 'POST', '/api/session/archive', { platform: 'claudecode', sessionId: 's', timeUpdated: 9, archived: true }],
  ['archiveProject', () => api.archiveProject('/r', false, 'local'), 'POST', '/api/project/archive', { directory: '/r', archived: false, remoteId: 'local' }],
  ['markSessionSeen', () => api.markSessionSeen('p', 's', 9), 'POST', '/api/session/seen', { platform: 'p', sessionId: 's', timeUpdated: 9 }],
  ['pinSession', () => api.pinSession('p', 's', true), 'POST', '/api/session/pin', { platform: 'p', sessionId: 's', pinned: true }],
  ['calcCost', () => api.calcCost({ modelID: 'm', input: 1, output: 2, cacheRead: 3, cacheWrite: 4 }), 'POST', '/api/cost/calc', { modelID: 'm', input: 1, output: 2, cacheRead: 3, cacheWrite: 4 }],
  ['activity', () => api.activity({ days: 2 }), 'GET', '/api/activity?days=2'],
  ['models', () => api.models({ dir: '/r' }), 'GET', '/api/models?dir=%2Fr'],
  ['projectSettings', () => api.projectSettings('/r'), 'GET', '/api/project/settings?dir=%2Fr&remoteId=local'],
  ['setProjectSettings', () => api.setProjectSettings('/r', ['m'], true), 'POST', '/api/project/settings', { directory: '/r', models: ['m'], off: true, remoteId: 'local' }],
  ['sessionModels', () => api.sessionModels('s', 'claudecode'), 'GET', '/api/session/s/models?platform=claudecode'],
  ['listFavorites', () => api.listFavorites('claudecode'), 'GET', '/api/favorites?platform=claudecode'],
  ['addFavorite', () => api.addFavorite('p', 'a', 'm'), 'POST', '/api/favorites', { platform: 'p', provider: 'a', model: 'm' }],
  ['removeFavorite', () => api.removeFavorite('p', 'a', 'm'), 'DELETE', '/api/favorites', { platform: 'p', provider: 'a', model: 'm' }],
  ['hourly', () => api.hourly({ days: 1 }), 'GET', '/api/hourly?days=1'],
  ['hourlyTokens', () => api.hourlyTokens({ model: 'm' }), 'GET', '/api/hourly-tokens?model=m'],
  ['remoteAccess', () => api.remoteAccess(), 'GET', '/api/settings/remote-access'],
  ['revealRemoteToken', () => api.revealRemoteToken(), 'POST', '/api/settings/remote-access/reveal-token'],
  ['addRemote', () => api.addRemote({ address: 'a', token: 't' }), 'POST', '/api/remotes', { address: 'a', token: 't' }],
  ['removeRemote', () => api.removeRemote(3), 'DELETE', '/api/remotes/3'],
  ['reconnectRemote', () => api.reconnectRemote(3), 'POST', '/api/remotes/3/reconnect'],
  ['resolveTargets', () => api.resolveTargets('/r', 'r-1'), 'POST', '/api/sessions/resolve-targets', { dir: '/r', remoteId: 'r-1' }],
  ['resolveTargets inferred', () => api.resolveTargets('/r'), 'POST', '/api/sessions/resolve-targets', { dir: '/r' }],
  ['prepareSession', () => api.prepareSession({ directory: '/r' } as never), 'POST', '/api/sessions/prepare', { directory: '/r' }],
  ['startSession', () => api.startSession({ directory: '/r', platform: 'claudecode' } as never), 'POST', '/api/sessions/start', { directory: '/r', platform: 'claudecode' }],
  ['listPermissions', () => api.listPermissions('s'), 'GET', '/api/session/s/permissions'],
  ['respondPermission', () => api.respondPermission('s', 'p', 'once', 'claudecode'), 'POST', '/api/session/s/permissions/p?platform=claudecode', { reply: 'once' }],
  ['setPermissionRules', () => api.setPermissionRules('s', []), 'PUT', '/api/session/s/permission-rules', { rules: [] }],
  ['listQuestions', () => api.listQuestions('s'), 'GET', '/api/session/s/questions'],
  ['respondQuestion', () => api.respondQuestion('s', 'q', [['a']]), 'POST', '/api/session/s/questions/q', { answers: [['a']] }],
  ['rejectQuestion', () => api.rejectQuestion('s', 'q'), 'POST', '/api/session/s/questions/q/reject'],
  ['abortSession', () => api.abortSession('s'), 'POST', '/api/session/s/abort'],
  ['revertSession', () => api.revertSession('s', 'm'), 'POST', '/api/session/s/revert', { messageID: 'm' }],
  ['unrevertSession', () => api.unrevertSession('s'), 'POST', '/api/session/s/unrevert'],
  ['compactSession', () => api.compactSession('s', 'p', 'm'), 'POST', '/api/session/s/compact', { providerID: 'p', modelID: 'm' }],
  ['forkSession', () => api.forkSession('s'), 'POST', '/api/session/s/fork', { messageID: '' }],
  ['moveSession', () => api.moveSession('s', '/r'), 'POST', '/api/session/s/move', { directory: '/r' }],
  ['agents', () => api.agents('s', undefined, 'claudecode'), 'GET', '/api/session/s/agents?platform=claudecode'],
  ['restartOpencode', () => api.restartOpencode('s', { all: true, force: true, confirmed: true }), 'POST', '/api/session/s/restart-opencode?all=true&force=true&confirmed=true'],
  ['runShell', () => api.runShell('s', 'ls', 'build'), 'POST', '/api/session/s/shell', { command: 'ls', agent: 'build' }],
  ['renameSession', () => api.renameSession('s', 't'), 'PATCH', '/api/session/s', { title: 't' }],
  ['setAutoApprove', () => api.setAutoApprove('s', true), 'POST', '/api/session/s/auto-approve', { enabled: true }],
  ['setJudgeDelay', () => api.setJudgeDelay(5), 'POST', '/api/settings/judge-delay', { delayMs: 5 }],
  ['setJudgeModel', () => api.setJudgeModel('m'), 'POST', '/api/settings/judge-model', { model: 'm' }],
  ['tmuxLaunchOpencode', () => api.tmuxLaunchOpencode('/r', 'r-1'), 'POST', '/api/tmux/launch-opencode', { directory: '/r', remoteId: 'r-1' }],
  ['term.listWindows', () => api.term.listWindows('/r', 'r-1'), 'GET', '/api/term/windows?dir=%2Fr&remoteId=r-1'],
  ['term.createWindow', () => api.term.createWindow('/r', 'local'), 'POST', '/api/term/windows', { dir: '/r' }],
  ['term.killWindow', () => api.term.killWindow('/r', 'w', 'r-1'), 'DELETE', '/api/term/windows', { dir: '/r', window: 'w', remoteId: 'r-1' }],
  ['routines.list', () => api.routines.list(), 'GET', '/api/routines'],
  ['routines.create', () => api.routines.create({ name: 'n' } as never), 'POST', '/api/routines', { name: 'n' }],
  ['routines.update', () => api.routines.update('r', { name: 'n' } as never), 'PUT', '/api/routines/r', { name: 'n' }],
  ['routines.remove', () => api.routines.remove('r'), 'DELETE', '/api/routines/r'],
  ['routines.run', () => api.routines.run('r'), 'POST', '/api/routines/r/run'],
  ['routines.history', () => api.routines.history('r', { limit: 5, before: { createdAt: 9, id: 'x' } }), 'GET', '/api/routines/r/history?limit=5&beforeCreatedAt=9&beforeId=x'],
  ['approvedPermissions', () => api.approvedPermissions('s'), 'GET', '/api/session/s/approved-permissions'],
  ['getPromptSections', () => api.getPromptSections(), 'GET', '/api/settings/prompt-sections'],
  ['setPromptSections', () => api.setPromptSections([{ title: 't', content: 'c' }]), 'POST', '/api/settings/prompt-sections', [{ title: 't', content: 'c' }]],
  ['getSharingEnabled', () => api.getSharingEnabled(), 'GET', '/api/settings/sharing'],
  ['setSharingEnabled', () => api.setSharingEnabled(true), 'POST', '/api/settings/sharing', { enabled: true }],
  ['getWebhookRelay', () => api.getWebhookRelay(), 'GET', '/api/settings/webhook-relay'],
  ['setWebhookRelay', () => api.setWebhookRelay({ relayUrl: 'u' }), 'POST', '/api/settings/webhook-relay', { relayUrl: 'u' }],
  ['getModelFallthroughSettings', () => api.getModelFallthroughSettings(), 'GET', '/api/settings/model-fallthrough'],
  ['setModelFallthroughSettings', () => api.setModelFallthroughSettings({ enabled: true } as never), 'POST', '/api/settings/model-fallthrough', { enabled: true }],
  ['getArchiveResurface', () => api.getArchiveResurface(), 'GET', '/api/settings/archive-resurface'],
  ['setArchiveResurface', () => api.setArchiveResurface('never'), 'POST', '/api/settings/archive-resurface', { mode: 'never' }],
];

describe('api route contract', () => {
  it.each(rows)('%s', async (_name, call, method, url, body) => {
    const calls = capture();
    await call();
    expect(calls).toEqual([{ url, method, body }]);
  });

  it('unwraps judge settings', async () => {
    capture();
    await expect(api.getJudgeDelay()).resolves.toBe(7);
    await expect(api.getJudgeModel()).resolves.toBe('m');
  });
});

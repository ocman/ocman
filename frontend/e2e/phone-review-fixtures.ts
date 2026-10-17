import type { Page } from '@playwright/test';
import { MOCK_PROJECT, MOCK_SESSION, makeCapabilitiesWithPort } from './fixtures.ts';

export const phoneReviewPages = [
  ['sessions', '/sessions'], ['projects', '/projects'], ['inbox', '/inbox'],
  ['conversation', `/session/${MOCK_SESSION.id}`], ['new-conversation', `/session/new?dir=${encodeURIComponent(MOCK_PROJECT.directory)}`],
  ['project-sessions', `/project/${encodeURIComponent(MOCK_PROJECT.directory)}`],
  ['worktrees', `/project/${encodeURIComponent(MOCK_PROJECT.directory)}/worktrees`],
  ['project-settings', `/project/${encodeURIComponent(MOCK_PROJECT.directory)}/settings`],
  ['routines', '/routines'], ['webhook-inboxes', '/routines?tab=inboxes'], ['artifacts', '/artifacts'], ['artifact-detail', '/artifacts/mobile-review'],
  ['usage', '/subscription-usage'], ['import-share', '/import-share'],
  ...['overview', 'epics', 'epics/mobile-review', 'issues', 'queue', 'configuration', 'how-to'].map(s => [`factory-${s.replaceAll('/', '-')}`, `/factory/${s}`]),
  ...['overview', 'activity', 'models', 'performance', 'permissions', 'logs'].map(s => [`analytics-${s}`, `/analytics/${s}`]),
  ...['notifications', 'sessions', 'conversation', 'remotes', 'plugins', 'platforms', 'idle-projects', 'auto-approve', 'sharing', 'webhooks', 'templates', 'link-previews', 'maintenance', 'provider-accounts'].map(s => [`settings-${s}`, `/settings?tab=${s}`]),
] as const;

/** Populated read-only fixtures; no review request reaches a real backend. */
export async function installPhoneReviewRoutes(page: Page) {
  const now = Date.now();
  const session = { ...MOCK_SESSION, liveConnection: true };
  const artifact = { id: 'mobile-review', title: 'Mobile layout review', description: 'Implementation notes and verification results.', directory: MOCK_PROJECT.directory, createdAt: new Date(now).toISOString(), items: [{ kind: 'file', name: 'review.txt', mime: 'text/plain', size: 42, url: '/api/artifacts/mobile-review/files/0' }] };
  const epic = { id: 'mobile-review', status: 'open', goal: 'Make mobile controls comfortable to use', brief: 'Review every page at phone sizes while keeping the desktop layout unchanged.', initialProject: MOCK_PROJECT.directory, progress: { requiredTotal: 4, requiredSucceeded: 2, optionalOpen: 0 }, attempts: [], projects: [{ path: MOCK_PROJECT.directory }] };
  const issue = { id: 'mobile-review.1', epicId: epic.id, title: 'Review the controls and spacing on every page', kind: 'implementation', status: 'open', project: MOCK_PROJECT.directory, description: 'Keep controls square and leave enough space between independent actions.', dependsOn: [], createdAt: now };

  await page.route('/api/capabilities', r => r.fulfill({ json: makeCapabilitiesWithPort() }));
  await page.route(new RegExp(`/api/session/${session.id}(\\?|$)`), r => r.fulfill({ json: {
    session, messages: [
      { id: 'u1', sessionId: session.id, timeCreated: now - 1000, data: { role: 'user' } },
      { id: 'a1', sessionId: session.id, timeCreated: now, data: { role: 'assistant' } },
    ], parts: [
      { id: 'p1', messageId: 'u1', data: { type: 'text', text: 'Keep the mobile layout square and comfortable to use.' } },
      { id: 'p2', messageId: 'a1', data: { type: 'text', text: 'I am checking the controls on each page.\n\nDesktop tables keep their current layout.' } },
    ], totalMessages: 2,
  } }));
  await page.route('/api/sessions/prepare', r => r.fulfill({ json: { platform: 'opencode', agents: [{ name: 'build' }], models: { hasProviders: false, models: [] }, commands: [] } }));
  await page.route('/api/sessions/resolve-targets', r => r.fulfill({ json: { candidates: [{ remoteId: 'local', remoteName: 'This machine', platform: 'opencode', dir: MOCK_PROJECT.directory }], remotes: [] } }));
  await page.route('/api/worktree/list*', r => r.fulfill({ json: { worktrees: [{ path: MOCK_PROJECT.directory, branch: 'main', isMain: true }] } }));
  await page.route('/api/routines', r => r.fulfill({ json: [{ id: 'mobile-review', name: 'Release check', prompt: 'Check the release health', directory: MOCK_PROJECT.directory, platform: 'opencode', remoteId: 'local', enabled: true, scheduleKind: 'cron', scheduleConfigJSON: '{"expression":"0 9 * * 1-5"}', nextDueAt: now + 86400000, createdAt: now, updatedAt: now }] }));
  await page.route('/api/webhook-inboxes', r => r.fulfill({ json: [] }));
  await page.route('/api/artifacts/stats', r => r.fulfill({ json: { count: 1, totalBytes: 42 } }));
  await page.route(/\/api\/artifacts(\?.*)?$/, r => r.fulfill({ json: { artifacts: [artifact], nextCursor: '' } }));
  await page.route('/api/artifacts/mobile-review', r => r.fulfill({ json: artifact }));
  await page.route('/api/artifacts/mobile-review/files/0', r => r.fulfill({ contentType: 'text/plain', body: 'Use square controls with room for fingers.' }));
  await page.route('/api/subscription-usage', r => r.fulfill({ json: { providers: [{ id: 'anthropic', name: 'Anthropic', status: 'ok', plan: 'max', windows: [{ name: 'Current session', usedPercent: 35, resetsAt: new Date(now + 7200000).toISOString() }] }] } }));
  await page.route('/api/inbox*', r => r.fulfill({ json: { items: [{ id: 'mobile-review', remoteId: 'local', title: 'Review the mobile layout', body: 'The latest changes are ready for review.', createdAt: now, readAt: 0 }], unreadTotal: 1 } }));
  await page.route(/\/api\/factory\/epics(\?.*)?$/, r => r.fulfill({ json: [epic] }));
  await page.route('/api/factory/epics/mobile-review', r => r.fulfill({ json: epic }));
  await page.route('/api/factory/epics/mobile-review/issues', r => r.fulfill({ json: [issue] }));
  await page.route('/api/factory/epics/mobile-review/removed-issues', r => r.fulfill({ json: [] }));
  await page.route('/api/factory/epics/mobile-review/proposals', r => r.fulfill({ json: [] }));
  await page.route('/api/factory/queue', r => r.fulfill({ json: [{ id: issue.id, epicId: epic.id, title: issue.title, project: MOCK_PROJECT.directory, state: 'running', attemptId: 'a1', session: { platform: session.platform, id: session.id } }] }));
  await page.route('/api/factory/configuration', r => r.fulfill({ json: { globalCapacity: 4, projectCapacity: 2, projectOverrides: {} } }));
  await page.route('/api/factory/formulas', r => r.fulfill({ json: [] }));
  await page.route('/api/factory/formulas/ocman%2Ftracer/3', r => r.fulfill({ json: {
    id: 'ocman/tracer', version: 3, name: 'Tracer', source: 'version: 2\nname: Tracer\nsteps: {}\n', hash: 'review-formula', inputs: ['goal', 'initial_project'], nodes: [{ key: 'plan', kind: 'planning' }], edges: [], valid: true,
  } }));
  await page.route('/api/settings/platforms', r => r.fulfill({ json: { enabled: ['opencode', 'claudecode'] } }));
  await page.route('/api/settings/idle-project-timeout', r => r.fulfill({ json: { minutes: 30 } }));
  await page.route('/api/maintenance/opencode-db', r => r.fulfill({ json: { available: false, job: { running: false } } }));
  await page.route('/api/provider-accounts', r => r.fulfill({ json: { accounts: [] } }));
}

import type { Page } from './fixtures';

// Hand-written demo code, forge records and metrics. No exports from real data.
export async function installChangesDemo(page: Page, sessionId: string, directory: string) {
  const path = 'src/components/ProjectSwitcher.tsx';
  const before = '  return <Menu items={projects} onSelect={onSelect} />;';
  const after = [
    '  return <Menu',
    '    items={projects}',
    '    onSelect={onSelect}',
    '    keyboardNavigation',
    '    restoreFocusOnClose',
    '  />;',
  ].join('\n');
  const diff = `diff --git a/${path} b/${path}\n--- a/${path}\n+++ b/${path}\n@@ -1,3 +1,8 @@\n export function ProjectSwitcher({ projects, onSelect }) {\n-${before}\n${after.split('\n').map(line => `+${line}`).join('\n')}\n }\n`;
  await page.route('/api/git/diff?*', route => route.fulfill({ json: {
    repo: directory, branch: 'feat/keyboard-navigation', ahead: 2, behind: 0,
    files: [
      { path, status: 'modified', additions: 6, deletions: 1, isBinary: false, diff },
      { path: 'src/components/ProjectSwitcher.test.tsx', status: 'added', additions: 42, deletions: 0, isBinary: false, diff: '' },
    ],
  } }));
  const remote = { remote: 'origin', host: 'code.example.com', type: 'forgejo', repo: 'demo/atlas-web' };
  const pr = {
    number: 42, title: 'Add keyboard navigation', status: 'open', body: 'Keyboard selection and focus restoration.',
    author: 'demo-developer', url: 'https://code.example.com/demo/atlas-web/pulls/42',
    branch: 'feat/keyboard-navigation', updatedAt: '2026-10-20T14:26:00Z',
    host: remote.host, repo: remote.repo, headSha: 'abc123demo', crossFork: false,
    labels: [], assignees: [], requestedReviewers: [],
  };
  const checks = { state: 'success', checks: [{ name: 'Tests and lint', state: 'success' }] };
  await page.route('/api/project/upstreams*', route => route.fulfill({ json: { upstreams: [remote] } }));
  await page.route('/api/project/prs*', route => route.fulfill({ json: { prs: [pr], pagination: { page: 1, perPage: 30, hasMore: false } } }));
  await page.route('/api/project/issues*', route => route.fulfill({ json: { issues: [], pagination: { page: 1, perPage: 30, hasMore: false } } }));
  await page.route('/api/project/forge-user*', route => route.fulfill({ json: { login: 'demo-developer', host: remote.host } }));
  await page.route('/api/project/pr-checks*', route => route.fulfill({ json: checks }));
  await page.route(`/api/session/${sessionId}/pull-request-checks*`, route => route.fulfill({ json: checks }));
  await page.route(`/api/session/${sessionId}/pull-requests*`, route => route.fulfill({ json: {
    pullRequests: [{ platform: 'opencode', sessionId, remoteId: 'local', directory, remote, number: 42, pr, watching: true }],
  } }));
  return { id: 'demo-edit', messageId: 'demo-answer', sessionId, data: {
    type: 'tool', tool: 'apply_patch', callID: 'demo-edit-call', state: {
      status: 'completed', input: { patchText: `*** Begin Patch\n*** Update File: ${path}\n@@\n-${before}\n${after.split('\n').map(line => `+${line}`).join('\n')}\n*** End Patch` },
      output: 'Edit applied successfully.', metadata: { diff }, time: { start: 1792506360000, end: 1792506361000 },
    },
  } };
}

export async function installAnalyticsDemo(page: Page) {
  const series = Array.from({ length: 14 }, (_, index) => {
    const volume = [3, 5, 4, 7, 6, 2, 1, 5, 8, 6, 9, 7, 4, 6][index];
    return { label: `10-${String(index + 7).padStart(2, '0')}`, inputTokens: volume * 12_000,
      outputTokens: volume * 4_000, cacheReadTokens: volume * 18_000 };
  });
  const sum = (key: 'inputTokens' | 'outputTokens' | 'cacheReadTokens') => series.reduce((total, point) => total + point[key], 0);
  await page.route(/\/api\/metrics(?:\/performance)?(?:\?.*)?$/, route => route.fulfill({ json: {
    summary: {
      requests: 384, completedRequests: 384, successfulRequests: 380, errorRequests: 4, errorRate: 4 / 384,
      inputTokens: sum('inputTokens'), outputTokens: sum('outputTokens'), cacheReadTokens: sum('cacheReadTokens'),
      totalTokens: sum('inputTokens') + sum('outputTokens') + sum('cacheReadTokens'),
      cacheWriteTokens: 48_000, avgTokensPerSec: 86.4, avgDurationMs: 18_500,
      p50DurationMs: 14_000, p95DurationMs: 42_000, totalDurationMs: 7_104_000,
      cacheHitRate: 0.6, totalCost: 12.84, totalCalcCost: 16.32, totalEffectiveCost: 16.32,
      costPerSuccessfulRequest: 16.32 / 380,
    }, series, agents: [], costByModel: { models: [], series: [] },
    dailyEstimatedCostByModel: { models: [], series: [] }, dailyEffectiveCostByModel: { models: [], series: [] },
    stopReasons: [], requests: [], totalRequests: 384, sessions: [], totalSessions: 48,
    projects: [], totalProjects: 3, availableAgents: [], availableModels: [],
  } }));
  await page.route('/api/analytics/overview', route => route.fulfill({ json: {
    inventoryScope: 'local', totalSessions: 48, subagentSessions: 12, totalProjects: 3, totalRoutines: 4,
    routineRunsByStatus: { done: 28 }, factoryEpicsByStatus: {}, factoryIssuesByStatus: {},
    factoryAttemptsByPhase: {}, factoryAttemptsByTerminalOutcome: {},
  } }));
  await page.route('/api/analytics/database-sizes*', route => route.fulfill({ json: Array.from({ length: 14 }, (_, index) => [
    { database: 'ocman', sampledAt: Date.UTC(2026, 9, index + 7), sizeBytes: (8 + index * 0.3) * 1024 * 1024 },
    { database: 'statev2', sampledAt: Date.UTC(2026, 9, index + 7), sizeBytes: (24 + index * 2) * 1024 * 1024 },
  ]).flat() }));
}

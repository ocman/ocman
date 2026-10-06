import { test, expect } from './fixtures';

test('graph approval shows frozen parent groups and external dependencies', async ({ mockedPage: page }) => {
  const issues = [
    { id: 'old', epicId: 'ship', project: '/repo', kind: 'mol', title: 'Old parent', status: 'open' },
    { id: 'new', epicId: 'ship', project: '/repo', kind: 'mol', title: 'New parent', status: 'open' },
    { id: 'work', epicId: 'ship', project: '/repo', kind: 'task', title: 'Frozen task', status: 'open', parentId: 'new', dependsOn: [{ id: 'other.1', type: 'blocks' }] },
  ];
  const proposal = { revision: 2, contentHash: 'frozen', manifest: {
    epicId: 'ship', molId: 'old', project: '/repo', nodes: [], issues,
    externalIssues: [{ id: 'other.1', epicId: 'other', project: '/other', kind: 'task', title: 'External blocker', status: 'open' }],
  } };
  const epic = { id: 'ship', status: 'open', goal: 'Review graph changes', initialProject: '/repo',
    formulaId: 'ocman/tracer', formulaVersion: 4, formulaRevision: 4, formulaHash: 'formula', formulaOrigin: 'built_in',
    progress: { requiredTotal: 1, requiredSucceeded: 0, optionalOpen: 0 },
    planGate: { issueId: 'gate', resolution: 'open', proposalRevision: 2, proposalHash: 'frozen' }, proposal,
  };
  await page.route('/api/factory/epics', (route) => route.fulfill({ json: [epic] }));
  await page.route('/api/factory/epics/ship', (route) => route.fulfill({ json: epic }));
  // The current work has a different parent; the preview must use the snapshot.
  await page.route('/api/factory/epics/ship/issues', (route) => route.fulfill({ json: issues.map((issue) => issue.id === 'work' ? { ...issue, parentId: 'old', title: 'Live task' } : issue) }));
  await page.route('/api/factory/epics/ship/proposals', (route) => route.fulfill({ json: [proposal] }));
  await page.goto('/factory/epics/ship');
  const graph = page.getByTestId('epic-graph');
  await expect(graph.getByText('Old parent', { exact: true })).toBeVisible();
  await expect(graph.getByText('New parent', { exact: true })).toBeVisible();
  await expect(graph.getByText('Frozen task', { exact: true })).toBeVisible();
  await expect(graph.getByText('other: External blocker', { exact: true })).toBeVisible();
  await expect(graph.getByText('Live task', { exact: true })).toHaveCount(0);
  await expect(page.getByRole('button', { name: 'Approve plan', exact: true })).toBeEnabled();
  const screenshot = test.info().outputPath('frozen-graph-preview.png');
  await page.screenshot({ path: screenshot });
  await test.info().attach('frozen-graph-preview', { path: screenshot, contentType: 'image/png' });
});

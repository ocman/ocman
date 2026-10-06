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

test('the pending proposal can be approved with its additions visible', async ({ mockedPage: page }) => {
  const original = { revision: 1, contentHash: 'original', manifest: { epicId: 'ship', molId: 'mol', project: '/repo', nodes: [{ key: 'existing', type: 'implementation', requirement: 'required', title: 'Existing work' }] } };
  const proposal = { revision: 3, contentHash: 'amendment', manifest: { epicId: 'ship', molId: 'mol', project: '/repo', nodes: [], baseRevision: 1, issues: [
    { id: 'ship.1', epicId: 'ship', project: '/repo', kind: 'implementation', title: 'Existing work', status: 'closed', outcome: 'succeeded', manifestKey: 'existing' },
    { id: 'ship.2', epicId: 'ship', project: '/repo', kind: 'task', title: 'New contract test', status: 'open' },
    { id: 'ship.3', epicId: 'ship', project: '/repo', kind: 'task', title: 'New recovery check', status: 'open', dependsOn: [{ id: 'ship.2', type: 'blocks' }] },
  ] } };
  const intermediate = { ...proposal, revision: 2, contentHash: 'intermediate', manifest: { ...proposal.manifest, issues: proposal.manifest.issues.slice(0, 2) } };
  const epic = { id: 'ship', status: 'open', goal: 'Review newly discovered work', initialProject: '/repo', formulaId: 'ocman/tracer', formulaVersion: 4, formulaRevision: 4, formulaHash: 'formula', formulaOrigin: 'built_in', progress: { requiredTotal: 3, requiredSucceeded: 1, optionalOpen: 0 }, planGate: { issueId: 'gate', resolution: 'open', proposalRevision: 3, proposalHash: 'amendment' }, proposal };
  await page.route('/api/factory/epics', (route) => route.fulfill({ json: [epic] }));
  await page.route('/api/factory/epics/ship', (route) => route.fulfill({ json: epic }));
  await page.route('/api/factory/epics/ship/issues', (route) => route.fulfill({ json: proposal.manifest.issues }));
  await page.route('/api/factory/epics/ship/proposals', (route) => route.fulfill({ json: [original, intermediate, proposal] }));
  await page.route('/api/factory/epics/ship/plan-gate/approve', async (route) => {
    expect(route.request().postDataJSON()).toMatchObject({ expectedRevision: 3, expectedHash: 'amendment' });
    epic.planGate.resolution = 'approved';
    await route.fulfill({ json: epic.planGate });
  });
  await page.goto('/factory/epics/ship');
  const preview = page.getByLabel('Proposed plan');
  await expect(preview.getByText('Added', { exact: true })).toHaveCount(2);
  await expect(preview.getByText('Added blocks', { exact: true })).toBeVisible();
  await expect(preview.getByLabel('Proposal additions')).toHaveText('2 added issues · 1 added connections');
  const screenshot = test.info().outputPath('proposal-approval-additions.png');
  await page.screenshot({ path: screenshot, fullPage: true });
  await test.info().attach('proposal-approval-additions', { path: screenshot, contentType: 'image/png' });
  const approval = page.waitForResponse((response) => response.url().endsWith('/plan-gate/approve'));
  await page.getByRole('button', { name: 'Approve revision 3', exact: true }).click();
  await approval;
  await expect(page.getByRole('button', { name: 'Approve revision 3', exact: true })).toHaveCount(0);
  await page.getByRole('tab', { name: 'Plan', exact: true }).click();
  await expect(page.getByText('No new proposal is awaiting approval.', { exact: true })).toBeVisible();
});

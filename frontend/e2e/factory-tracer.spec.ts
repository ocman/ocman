import { test, expect } from './fixtures';

test('Factory tracer approves one plan, materializes one worktree session, and closes its containers', async ({ mockedPage: page }) => {
  const epic = { id: 'ship-a1b2', status: 'open', goal: 'Ship tracer', brief: '', initialProject: '/repo', formulaId: 'ocman/tracer', formulaVersion: 1, formulaRevision: 1, formulaHash: 'formula', formulaOrigin: 'built_in', instantiationId: 'one', progress: { requiredTotal: 1, requiredSucceeded: 0, optionalOpen: 0 }, attempts: [] as object[] };
  let poured = false;
  let claimed = false;
  let approved = false;
  let materialized = false;
  let closed = false;
  const issues = () => poured ? [
    { id: 'ship-a1b2.1', epicId: epic.id, kind: 'mol', title: 'Tracer Mol', status: closed ? 'closed' : 'open', requirement: 'required' },
    { id: 'ship-a1b2.1.1', epicId: epic.id, parentId: 'ship-a1b2.1', kind: 'plan', title: 'Plan', status: claimed ? 'closed' : 'open', requirement: 'required' },
    { id: 'ship-a1b2.1.2', epicId: epic.id, parentId: 'ship-a1b2.1', kind: 'gate', title: 'Approval', status: approved ? 'closed' : 'open', requirement: 'required' },
    { id: 'ship-a1b2.1.3', epicId: epic.id, parentId: 'ship-a1b2.1', kind: 'materialization', title: 'Materialization', status: materialized ? 'closed' : 'open', requirement: 'required' },
    ...(materialized ? [{ id: 'ship-a1b2.1.4', epicId: epic.id, parentId: 'ship-a1b2.1', kind: 'implementation', title: 'Implementation', status: 'closed', outcome: 'succeeded', requirement: 'required', dispatchState: 'completed', session: { platform: 'opencode', id: 'worktree-session' } }] : []),
  ] : [];
  const view = () => ({ ...epic, status: closed ? 'closed' : 'open', progress: { requiredTotal: materialized ? 1 : 0, requiredSucceeded: materialized ? 1 : 0, optionalOpen: 0 }, attempts: claimed ? [{ id: 'plan-attempt', phase: 'active', session: { platform: 'opencode', id: 'plan-session' } }] : [], proposal: claimed ? { epicId: epic.id, molId: 'ship-a1b2.1', project: '/repo', revision: 1, contentHash: 'exact', manifest: { epicId: epic.id, molId: 'ship-a1b2.1', project: '/repo', nodes: [{ key: 'implementation', type: 'implementation', requirement: 'required' }] } } : undefined, planGate: claimed ? { issueId: 'ship-a1b2.1.2', proposalRevision: 1, proposalHash: 'exact', resolution: approved ? 'approved' : 'open' } : undefined });
  await page.route('/api/factory/formulas', (route) => route.fulfill({ json: [] }));
  await page.route('/api/factory/queue', (route) => route.fulfill({ json: [] }));
  await page.route('/api/projects', (route) => route.fulfill({ json: [{ directory: '/repo', archived: false }] }));
  await page.route('/api/factory/epics', async (route) => {
    if (route.request().method() === 'POST') return route.fulfill({ status: 201, json: view() });
    return route.fulfill({ json: [view()] });
  });
  await page.route(`/api/factory/epics/${epic.id}`, (route) => route.fulfill({ json: view() }));
  await page.route(`/api/factory/epics/${epic.id}/issues`, (route) => route.fulfill({ json: issues() }));
  await page.route(`/api/factory/epics/${epic.id}/proposals`, (route) => route.fulfill({ json: view().proposal ? [view().proposal] : [] }));
  await page.route(`/api/factory/epics/${epic.id}/pour`, (route) => { poured = true; claimed = true; return route.fulfill({ status: 201, json: issues() }); });
	await page.route(`/api/factory/epics/${epic.id}/plans/${epic.id}.1.1`, (route) => { claimed = true; return route.fulfill({ status: 201, json: {} }); });
  await page.route(`/api/factory/epics/${epic.id}/plan-gate/approve`, (route) => { approved = true; materialized = true; return route.fulfill({ json: view().planGate }); });
	await page.route(`/api/factory/epics/${epic.id}/materializations/${epic.id}.1.3`, (route) => { materialized = true; return route.fulfill({ status: 201, json: {} }); });
  await page.route(`/api/factory/epics/${epic.id}/mols/${epic.id}.1/close`, (route) => route.fulfill({ status: 204 }));
  await page.route(`/api/factory/epics/${epic.id}/close`, (route) => { closed = true; return route.fulfill({ status: 204 }); });

  await page.goto('/factory/epics');
  await page.getByRole('button', { name: 'New epic' }).click();
  await page.getByLabel('Goal').fill(epic.goal);
  await page.getByRole('combobox', { name: 'Initial Factory project' }).click();
  await page.getByRole('option', { name: '/repo' }).click();
  await page.getByRole('checkbox', { name: 'Allow Factory agents to run commands in this repository' }).check();
  await page.getByRole('button', { name: 'Create epic', exact: true }).click();
  await page.getByRole('link', { name: epic.goal }).click();
  await page.getByRole('button', { name: 'Pour graph' }).click();
  await page.reload();
  await expect(page.getByRole('link', { name: 'Open session' })).toHaveAttribute('href', '/session/plan-session');
  await page.getByRole('button', { name: 'Approve plan' }).click();
  await page.reload();
  await expect(page.getByTestId('issue-title-ship-a1b2.1.4')).toHaveText('Implementation');
  await page.getByRole('button', { name: 'Close Mol' }).click();
  await page.getByRole('button', { name: 'Close epic' }).click();
  await page.reload();
  await expect(page.getByTestId('epic-status')).toHaveText('closed');
});

test('Factory tracer rejects a plan without creating implementation work', async ({ mockedPage: page }) => {
  const epic = { id: 'reject-a1b2', status: 'open', goal: 'Reject tracer', brief: '', initialProject: '/repo', formulaId: 'ocman/tracer', formulaVersion: 1, formulaRevision: 1, formulaHash: 'formula', formulaOrigin: 'built_in', instantiationId: 'one', progress: { requiredTotal: 0, requiredSucceeded: 0, optionalOpen: 0 }, attempts: [] as object[] };
  let poured = false;
  let rejected = false;
  const issues = () => poured ? [
    { id: 'reject-a1b2.1', epicId: epic.id, kind: 'mol', title: 'Tracer Mol', status: rejected ? 'closed' : 'open', requirement: 'required' },
    { id: 'reject-a1b2.1.1', epicId: epic.id, parentId: 'reject-a1b2.1', kind: 'plan', title: 'Plan', status: 'closed', requirement: 'required' },
    { id: 'reject-a1b2.1.2', epicId: epic.id, parentId: 'reject-a1b2.1', kind: 'gate', title: 'Approval', status: rejected ? 'closed' : 'open', requirement: 'required' },
    { id: 'reject-a1b2.1.3', epicId: epic.id, parentId: 'reject-a1b2.1', kind: 'materialization', title: 'Materialization', status: rejected ? 'closed' : 'open', requirement: 'required' },
  ] : [];
  const view = () => ({ ...epic, attempts: poured ? [{ id: 'plan-attempt', phase: 'active', session: { platform: 'opencode', id: 'plan-session' } }] : [], proposal: poured ? { epicId: epic.id, molId: 'reject-a1b2.1', project: '/repo', revision: 1, contentHash: 'exact', manifest: { epicId: epic.id, molId: 'reject-a1b2.1', project: '/repo', nodes: [{ key: 'implementation', type: 'implementation', requirement: 'required' }] } } : undefined, planGate: poured ? { issueId: 'reject-a1b2.1.2', proposalRevision: 1, proposalHash: 'exact', resolution: rejected ? 'rejected' : 'open' } : undefined });
  await page.route('/api/factory/formulas', (route) => route.fulfill({ json: [] }));
  await page.route('/api/factory/queue', (route) => route.fulfill({ json: [] }));
  await page.route('/api/projects', (route) => route.fulfill({ json: [{ directory: '/repo', archived: false }] }));
  await page.route('/api/factory/epics', async (route) => route.request().method() === 'POST' ? route.fulfill({ status: 201, json: view() }) : route.fulfill({ json: [view()] }));
  await page.route(`/api/factory/epics/${epic.id}`, (route) => route.fulfill({ json: view() }));
  await page.route(`/api/factory/epics/${epic.id}/issues`, (route) => route.fulfill({ json: issues() }));
  await page.route(`/api/factory/epics/${epic.id}/proposals`, (route) => route.fulfill({ json: poured ? [view().proposal] : [] }));
  await page.route(`/api/factory/epics/${epic.id}/pour`, (route) => { poured = true; return route.fulfill({ status: 201, json: issues() }); });
  await page.route(`/api/factory/epics/${epic.id}/plan-gate/reject`, (route) => { rejected = true; return route.fulfill({ json: view().planGate }); });

  await page.goto('/factory/epics');
  await page.getByRole('button', { name: 'New epic' }).click();
  await page.getByLabel('Goal').fill(epic.goal);
  await page.getByRole('combobox', { name: 'Initial Factory project' }).click();
  await page.getByRole('option', { name: '/repo' }).click();
  await page.getByRole('checkbox', { name: 'Allow Factory agents to run commands in this repository' }).check();
  await page.getByRole('button', { name: 'Create epic', exact: true }).click();
  await page.getByRole('link', { name: epic.goal }).click();
  await page.getByRole('button', { name: 'Pour graph' }).click();
  await page.reload();
  await page.getByRole('button', { name: 'Reject plan' }).click();
  await expect(page.getByRole('status')).toHaveText('Plan rejected.');
  await page.reload();
  await expect(page.getByText('Implementation', { exact: true })).toHaveCount(0);
});

test.describe('narrow Factory navigation', () => {
  test.use({ viewport: { width: 320, height: 700 } });

  test('keeps every local destination reachable', async ({ mockedPage: page }) => {
    await page.route('/api/factory/queue', (route) => route.fulfill({ json: [] }));
    await page.route('/api/factory/configuration', (route) => route.fulfill({ json: { globalCapacity: 10, projectCapacity: 4, projectOverrides: {} } }));
    await page.route('/api/factory/epics', (route) => route.fulfill({ json: [] }));
    await page.route('/api/factory/formulas**', (route) => route.fulfill({ json: route.request().url().includes('/ocman%2Ftracer/1') ? {
      id: 'ocman/tracer', version: 1, name: 'Tracer', source: 'version = 1\nname = "Tracer"\n', hash: 'compiled-hash', sourceHash: 'source-hash', inputs: ['goal', 'initial_project'], nodes: [{ key: 'plan', kind: 'plan' }], edges: [], valid: true,
    } : [] }));

    await page.goto('/factory/queue');
    await expect(page.getByRole('link', { name: 'Queue' })).toHaveAttribute('aria-current', 'page');
    await page.getByRole('link', { name: 'Epics' }).press('Enter');
    await expect(page).toHaveURL(/\/factory\/epics$/);
    await page.getByRole('link', { name: 'Issues' }).press('Enter');
    await expect(page).toHaveURL(/\/factory\/issues$/);
    await page.getByRole('link', { name: 'Queue' }).press('Enter');
    await expect(page).toHaveURL(/\/factory\/queue$/);
    await page.getByRole('link', { name: 'Configuration' }).press('Enter');
    await expect(page).toHaveURL(/\/factory\/configuration$/);
    await expect(page.getByRole('heading', { name: 'Tracer · ocman/tracer@1' })).toBeVisible();
    await expect(page.getByRole('status')).toContainText('Formula is valid');
  });
});

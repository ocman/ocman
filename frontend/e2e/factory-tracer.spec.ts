import { test, expect } from './fixtures';

test('Factory tracer approves a plan, checkpoints implementation, delivers a PR, and closes its containers', async ({ mockedPage: page }) => {
  const epic = { id: 'ship-a1b2', status: 'open', goal: 'Ship tracer', brief: '', initialProject: '/repo', formulaId: 'ocman/tracer', formulaVersion: 1, formulaRevision: 1, formulaHash: 'formula', formulaOrigin: 'built_in', instantiationId: 'one', progress: { requiredTotal: 1, requiredSucceeded: 0, optionalOpen: 0 }, attempts: [] as object[] };
  let poured = false;
  let claimed = false;
  let approved = false;
  let materialized = false;
  let delivered = false;
  const prURL = 'https://forge.example/acme/repo/pulls/1';
  let molClosed = false;
  let closed = false;
  const issues = () => poured ? [
    { id: 'ship-a1b2.1', epicId: epic.id, kind: 'mol', title: 'Tracer Mol', status: closed ? 'closed' : 'open', requirement: 'required' },
    { id: 'ship-a1b2.1.1', epicId: epic.id, parentId: 'ship-a1b2.1', kind: 'plan', title: 'Plan', status: claimed ? 'closed' : 'open', requirement: 'required' },
    { id: 'ship-a1b2.1.2', epicId: epic.id, parentId: 'ship-a1b2.1', kind: 'gate', title: 'Approval', status: approved ? 'closed' : 'open', requirement: 'required' },
    { id: 'ship-a1b2.1.3', epicId: epic.id, parentId: 'ship-a1b2.1', kind: 'materialization', title: 'Materialization', status: materialized ? 'closed' : 'open', requirement: 'required' },
    ...(materialized ? [{ id: 'ship-a1b2.1.4', epicId: epic.id, parentId: 'ship-a1b2.1', kind: 'implementation', title: 'Implementation', status: 'closed', outcome: 'succeeded', requirement: 'required', dispatchState: 'completed', session: { platform: 'opencode', id: 'worktree-session' } }] : []),
    ...(materialized ? [{ id: 'ship-a1b2.1.5', epicId: epic.id, parentId: 'ship-a1b2.1', kind: 'delivery', title: 'Final delivery', status: delivered ? 'closed' : 'open', outcome: delivered ? 'succeeded' : '', requirement: 'required', dispatchState: delivered ? 'completed' : 'ready', prUrl: delivered ? prURL : undefined }] : []),
  ] : [];
  const view = () => ({ ...epic, status: closed ? 'closed' : 'open', progress: { requiredTotal: materialized ? 2 : 0, requiredSucceeded: delivered ? 2 : materialized ? 1 : 0, optionalOpen: 0, deliveryStatus: delivered ? 'ready_for_review' : materialized ? 'pending' : undefined, closureBlockers: materialized && !delivered ? ['Final delivery'] : [] }, attempts: claimed ? [{ id: 'plan-attempt', phase: 'active', session: { platform: 'opencode', id: 'plan-session' } }] : [], proposal: claimed ? { epicId: epic.id, molId: 'ship-a1b2.1', project: '/repo', revision: 1, contentHash: 'exact', manifest: { epicId: epic.id, molId: 'ship-a1b2.1', project: '/repo', nodes: [{ key: 'implementation', type: 'implementation', requirement: 'required' }] } } : undefined, planGate: claimed ? { issueId: 'ship-a1b2.1.2', proposalRevision: 1, proposalHash: 'exact', resolution: approved ? 'approved' : 'open' } : undefined });
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
  let finishApproval!: () => void;
  const approvalPending = new Promise<void>((resolve) => { finishApproval = resolve; });
  await page.route(`/api/factory/epics/${epic.id}/plan-gate/approve`, async (route) => { await approvalPending; approved = true; materialized = true; return route.fulfill({ json: view().planGate }); });
	await page.route(`/api/factory/epics/${epic.id}/materializations/${epic.id}.1.3`, (route) => { materialized = true; return route.fulfill({ status: 201, json: {} }); });
  await page.route(`/api/factory/epics/${epic.id}/mols/${epic.id}.1/close`, (route) => {
    if (!delivered) return route.fulfill({ status: 409, body: 'Final delivery is incomplete' });
    molClosed = true;
    return route.fulfill({ status: 204 });
  });
  await page.route(`/api/factory/epics/${epic.id}/close`, (route) => {
    if (!delivered) return route.fulfill({ status: 409, body: 'Final delivery is incomplete' });
    closed = true;
    return route.fulfill({ status: 204 });
  });

  // Every reload below reads state that a POST flips in the route handler above,
  // so the POST has to land before the reload cancels it.
  const posted = (path: string) => page.waitForResponse((response) => response.url().endsWith(path) && response.request().method() === 'POST');

  await page.goto('/factory/epics');
  await page.getByRole('button', { name: 'New epic' }).click();
  const createEpic = page.getByRole('dialog', { name: 'Create epic' });
  await expect(createEpic.getByRole('checkbox')).toHaveCSS('width', '16px');
  await expect(createEpic.getByLabel('Goal')).toHaveCSS('min-height', '38px');
  await page.getByLabel('Goal').fill(epic.goal);
  await page.getByRole('combobox', { name: 'Initial Factory project' }).click();
  await createEpic.getByRole('option', { name: '/repo', exact: true }).click();
  await page.getByRole('checkbox', { name: 'Allow Factory agents to run commands in this project' }).check();
  await page.getByRole('button', { name: 'Create epic', exact: true }).click();
  await page.getByRole('link', { name: epic.goal }).click();
  const pouring = posted(`/api/factory/epics/${epic.id}/pour`);
  await page.getByRole('button', { name: 'Pour graph' }).click();
  await pouring;
  await page.reload();
  await expect(page.getByRole('link', { name: 'Open session' })).toHaveAttribute('href', '/session/plan-session?factoryEpic=ship-a1b2');
  const approving = posted(`/api/factory/epics/${epic.id}/plan-gate/approve`);
  await page.getByRole('button', { name: 'Approve plan' }).click();
  const approvingButton = page.getByRole('button', { name: 'Approving…' });
  await expect(approvingButton).toBeDisabled();
  await expect(approvingButton).toHaveAttribute('aria-busy', 'true');
  expect(await approvingButton.evaluate((button) => getComputedStyle(button, '::before').animationName)).toBe('oc-button-spin');
  await expect(page.getByRole('button', { name: 'Request revision' })).toBeDisabled();
  finishApproval();
  await approving;
  await page.reload();
  await page.getByLabel('Board status').selectOption('all');
  await expect(page.getByRole('button', { name: 'Open issue ship-a1b2.1.4' })).toHaveText('Implementation');
  await expect(page.getByRole('button', { name: 'Open issue ship-a1b2.1', exact: true })).toHaveCount(0);
  await expect(page.getByText('Closure blocked by: Final delivery')).toBeVisible();
  const prematureClose = posted(`/api/factory/epics/${epic.id}/close`);
  page.once('dialog', (dialog) => dialog.dismiss());
  await page.getByRole('button', { name: 'Close epic' }).click();
  expect((await prematureClose).status()).toBe(409);
  expect(closed).toBe(false);
  await expect(page.getByRole('alert')).toHaveText('Final delivery is incomplete');
  // Simulate the final delivery agent completing after the implementation checkpoint.
  delivered = true;
  await page.reload();
  await page.getByLabel('Board status').selectOption('all');
  await expect(page.getByText('Required work: 2/2 complete. Optional work open: 0.')).toBeVisible();
  await page.getByRole('button', { name: 'Open issue ship-a1b2.1.5', exact: true }).click();
  await expect(page.getByRole('link', { name: prURL })).toHaveAttribute('href', prURL);
  await page.getByRole('button', { name: 'Close issue details' }).click();
  const closing = posted(`/api/factory/epics/${epic.id}/close`);
  await page.getByRole('button', { name: 'Close epic' }).click();
  await closing;
  await page.reload();
  await expect(page.getByTestId('epic-status')).toHaveText('closed');
  expect(molClosed).toBe(true);
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

  const posted = (path: string) => page.waitForResponse((response) => response.url().endsWith(path) && response.request().method() === 'POST');

  await page.goto('/factory/epics');
  await page.getByRole('button', { name: 'New epic' }).click();
  const createEpic = page.getByRole('dialog', { name: 'Create epic' });
  await page.getByLabel('Goal').fill(epic.goal);
  await page.getByRole('combobox', { name: 'Initial Factory project' }).click();
  await createEpic.getByRole('option', { name: '/repo', exact: true }).click();
  await page.getByRole('checkbox', { name: 'Allow Factory agents to run commands in this project' }).check();
  await page.getByRole('button', { name: 'Create epic', exact: true }).click();
  await page.getByRole('link', { name: epic.goal }).click();
  const pouring = posted(`/api/factory/epics/${epic.id}/pour`);
  await page.getByRole('button', { name: 'Pour graph' }).click();
  await pouring;
  await page.reload();
  await page.getByRole('button', { name: 'Reject plan' }).click();
  await expect(page.getByRole('status')).toHaveText('Plan rejected.');
  await page.reload();
  await expect(page.getByText('Implementation', { exact: true })).toHaveCount(0);
});

test('Factory issues keep every rendered row reachable', async ({ mockedPage: page }) => {
  const epic = { id: 'many-issues', status: 'open', goal: 'Many issues', brief: '', initialProject: '/repo', attempts: [] };
  const issues = Array.from({ length: 20 }, (_, index) => ({ id: `many-issues.${index + 1}`, epicId: epic.id, kind: 'task', title: `Issue ${index + 1}`, status: 'open' }));
  await page.route('/api/factory/epics', (route) => route.fulfill({ json: [epic] }));
  await page.route(`/api/factory/epics/${epic.id}/issues`, (route) => route.fulfill({ json: issues }));

  await page.goto('/factory/issues');

  await page.getByRole('button', { name: 'Open issue many-issues.20' }).click();
  await expect(page.getByRole('dialog', { name: 'Issue many-issues.20' })).toBeVisible();
  await expect(page.locator('.factory-list')).toHaveJSProperty('scrollHeight', await page.locator('.factory-list').evaluate((list) => list.clientHeight));
});

test.describe('narrow Factory navigation', () => {
  test.use({ viewport: { width: 320, height: 700 } });

  test('keeps every local destination reachable', async ({ mockedPage: page }) => {
    await page.route('/api/factory/queue', (route) => route.fulfill({ json: [] }));
    await page.route('/api/factory/configuration', (route) => route.fulfill({ json: { globalCapacity: 10, projectCapacity: 4, projectOverrides: {} } }));
    await page.route('/api/factory/epics', (route) => route.fulfill({ json: [] }));
    await page.route('/api/factory/formulas**', (route) => route.fulfill({ json: route.request().url().includes('/ocman%2Ftracer/3') ? {
      id: 'ocman/tracer', version: 3, name: 'Tracer', source: 'version: 2\nname: Tracer\nsteps: {}\n', hash: 'compiled-hash', sourceHash: 'source-hash', inputs: ['goal', 'initial_project'], nodes: [{ key: 'plan', kind: 'planning' }], edges: [], valid: true,
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
    await expect(page.getByRole('heading', { name: 'Tracer · ocman/tracer@3' })).toBeVisible();
    await expect(page.getByRole('status')).toContainText('Formula is valid');
    const editor = page.getByRole('textbox', { name: 'Formula YAML', includeHidden: true });
    await expect(editor).toBeHidden();
    await page.getByText('Formula source', { exact: true }).click();
    await expect(editor).toBeVisible();
    await expect(editor).toHaveAttribute('rows', '15');
    await expect(editor).toHaveCSS('resize', 'vertical');
    expect(await editor.evaluate((element) => element.getBoundingClientRect().height)).toBeGreaterThan(250);
  });
});

import { test, expect } from './fixtures';

test('resource tree preserves hierarchy and named context across reloads', async ({ mockedPage: page }) => {
  await page.route('/api/resources?*', (route) => route.fulfill({ json: {
    timestamp: Date.now(), cpuPercent: 125, memoryBytes: 0, history: [],
    processes: [
      { pid: 100, parentPid: 1, name: 'ocman', cpuPercent: 25, memoryBytes: 0 },
      { pid: 101, parentPid: 100, name: 'coding-agent', cpuPercent: 100, memoryBytes: 0,
        sessionId: 'tree-session', sessionTitle: 'Build process tree', projectName: 'ocman' },
      { pid: 102, parentPid: 101, name: 'worker', cpuPercent: 0, memoryBytes: 0 },
    ],
  } }));
  await page.goto('/sessions?activity=resources');
  const dialog = page.getByRole('dialog', { name: 'Activity', exact: true });
  const rows = dialog.getByRole('table').getByRole('row');
  await expect(rows.nth(1)).toContainText('coding-agent');
  await dialog.getByRole('radio', { name: 'Tree', exact: true }).click();
  await expect(dialog.getByRole('radio', { name: 'Tree', exact: true })).toBeChecked();
  await expect(rows.nth(1)).toContainText('ocman');
  await expect(rows.nth(2)).toContainText('coding-agent');
  await expect(rows.nth(3)).toContainText('worker');
  await expect(rows.nth(3)).toHaveAttribute('data-depth', '2');
  await expect(dialog.getByText('Project: ocman')).toBeVisible();
  await expect(dialog.getByRole('link', { name: 'Build process tree' })).toHaveAttribute('href', '/session/tree-session');
  await page.reload();
  await expect(dialog.getByRole('radio', { name: 'Tree', exact: true })).toBeChecked();
  await expect(rows.nth(3)).toHaveAttribute('data-depth', '2');
  await dialog.getByRole('radio', { name: 'Flat', exact: true }).click();
  await expect(dialog.getByRole('radio', { name: 'Flat', exact: true })).toBeChecked();
  await expect(rows.nth(1)).toContainText('coding-agent');
});

test('Activity sits below Inbox and opens background tasks and live resources', async ({ mockedPage: page }) => {
  let samples = 0;
  await page.route('/api/background-tasks?*', (route) => route.fulfill({ json: [] }));
  await page.route('/api/resources?remoteId=local', (route) => {
    samples++;
    const processes = [{ pid: 100, parentPid: 1, name: 'ocman', cpuPercent: 25, memoryBytes: 128 * 1024 ** 2 },
      { pid: 101, parentPid: 100, name: 'coding-agent', cpuPercent: 100, memoryBytes: 896 * 1024 ** 2 }];
    const point = { timestamp: Date.now(), cpuPercent: 125, memoryBytes: 1024 ** 3, processes };
    return route.fulfill({ json: {
      ...point, history: [point],
      processes,
    } });
  });
  await page.goto('/');
  const nav = page.getByRole('navigation', { name: 'Main navigation' });
  const inbox = nav.getByRole('link', { name: /^Inbox/ });
  const activity = nav.getByRole('button', { name: 'Activity', exact: true });
  await expect(activity).toBeVisible();
  const collapse = page.getByRole('button', { name: 'Collapse navigation' });
  if (await collapse.isVisible()) await collapse.click();
  await expect.poll(async () => activity.evaluate((button) => {
    const icon = button.querySelector('i')!.getBoundingClientRect();
    const bounds = button.getBoundingClientRect();
    return icon.left >= bounds.left && icon.right <= bounds.right;
  })).toBe(true);
  const inboxBox = await inbox.boundingBox();
  const activityBox = await activity.boundingBox();
  expect(activityBox!.y).toBeGreaterThan(inboxBox!.y);
  await activity.click();
  const dialog = page.getByRole('dialog', { name: 'Activity', exact: true });
  const viewport = page.viewportSize()!;
  const bounds = await dialog.boundingBox();
  expect(bounds!.width).toBeCloseTo(viewport.width * 0.9, 0);
  expect(bounds!.height).toBeCloseTo(viewport.height * 0.9, 0);
  await expect(dialog.getByRole('tab', { name: 'Resources', exact: true })).toHaveAttribute('aria-selected', 'true');
  await expect(dialog.getByRole('table', { name: 'Ocman processes' })).toBeVisible();
  expect(samples).toBeGreaterThan(0);
  await dialog.getByRole('tab', { name: 'Background tasks' }).click();
  await expect(dialog.getByText('No background tasks match this view.')).toBeVisible();
  await dialog.getByRole('tab', { name: 'Resources', exact: true }).click();
  await expect(dialog.getByRole('table', { name: 'Ocman processes' })).toBeVisible();
  await expect(dialog.getByRole('group', { name: 'CPU history' })).toBeVisible();
  await expect(dialog.getByRole('group', { name: 'Memory history' })).toBeVisible();
  await dialog.getByRole('button', { name: 'CPU: ocman, PID 100', exact: true }).click();
  await expect(dialog.getByRole('button', { name: 'Select ocman, PID 100' })).toHaveAttribute('aria-pressed', 'true');
  await expect(dialog.getByRole('table').getByRole('row').nth(1)).toContainText('ocman');
  await expect(dialog.getByRole('button', { name: 'CPU: coding-agent, PID 101' })).toHaveCount(0);
  await page.reload();
  await expect(dialog.getByRole('button', { name: 'Select ocman, PID 100' })).toHaveAttribute('aria-pressed', 'true');
  await dialog.getByRole('button', { name: 'Show all processes' }).click();
  await expect(dialog.getByRole('button', { name: 'Select coding-agent, PID 101' })).toBeVisible();
  const initial = samples;
  await expect.poll(() => samples).toBeGreaterThan(initial);
  await page.keyboard.press('Escape');
  await expect(dialog).not.toBeVisible();
  await expect(page).not.toHaveURL(/activity=/);
});

test('Activity resource graphs fit a phone viewport and close with the shared control', async ({ mockedPage: page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.route('/api/resources?*', (route) => route.fulfill({ json: {
    timestamp: Date.now(), cpuPercent: 0, memoryBytes: 0, processes: [], history: [],
  } }));
  await page.goto('/sessions');
  await page.getByRole('button', { name: 'More', exact: true }).click();
  await page.getByRole('navigation', { name: 'More destinations' }).getByRole('link', { name: 'Activity' }).click();
  const dialog = page.getByRole('dialog', { name: 'Activity', exact: true });
  await expect(dialog.getByRole('tab', { name: 'Resources', exact: true })).toHaveAttribute('aria-selected', 'true');
  await expect(dialog.getByRole('group', { name: 'CPU history' })).toBeVisible();
  await expect(dialog.getByRole('group', { name: 'Memory history' })).toBeVisible();
  const box = await dialog.boundingBox();
  expect(box!.width).toBeCloseTo(390 * 0.9, 0);
  expect(box!.height).toBeCloseTo(844 * 0.9, 0);
  expect(box!.x).toBeGreaterThanOrEqual(0);
  expect(box!.x + box!.width).toBeLessThanOrEqual(390);
  await dialog.getByRole('button', { name: 'Close activity' }).click();
  await expect(dialog).not.toBeVisible();
});

for (const viewport of [{ width: 1280, height: 720 }, { width: 390, height: 844 }]) {
  test(`activity header and tabs remain fixed while scrolling at ${viewport.width}px`, async ({ mockedPage: page }) => {
    await page.setViewportSize(viewport);
    await page.route('/api/resources?*', (route) => route.fulfill({ json: {
      timestamp: Date.now(), cpuPercent: 0, memoryBytes: 0, history: [],
      processes: Array.from({ length: 100 }, (_, i) => ({ pid: i + 1, parentPid: 0, name: `process-${i}`, cpuPercent: 0, memoryBytes: 0 })),
    } }));
    await page.goto('/sessions?activity=resources');
    const dialog = page.getByRole('dialog', { name: 'Activity', exact: true });
    const close = dialog.getByRole('button', { name: 'Close activity' });
    const tabs = dialog.getByRole('tablist');
    const panel = dialog.getByRole('tabpanel');
    await expect(dialog.getByRole('table')).toBeVisible();
    const headerBefore = await close.boundingBox();
    const tabsBefore = await tabs.boundingBox();
    await panel.evaluate((element) => { element.scrollTop = element.scrollHeight; });
    await expect.poll(() => panel.evaluate((element) => element.scrollTop)).toBeGreaterThan(0);
    expect(await close.boundingBox()).toEqual(headerBefore);
    expect(await tabs.boundingBox()).toEqual(tabsBefore);
    await expect(close).toBeInViewport();
    await close.click();
    await expect(dialog).not.toBeVisible();
  });
}

test('task rows stay compact and open details without closing the activity modal', async ({ mockedPage: page }) => {
  await page.route('/api/background-tasks?*', (route) => route.fulfill({ json: [{
    id: 'sync', title: 'Sync history', kind: 'conversation.history', ownerId: 'local', status: 'running',
    progress: { phase: 'Checking sessions', completed: 3, total: 5, unit: 'sessions', counters: { imported: 2 } },
    createdAt: 1000, updatedAt: 2000,
  }] }));
  await page.goto('/sessions?activity=tasks');
  const row = page.getByRole('button', { name: /Sync history/ });
  await expect(row).toBeVisible();
  expect((await row.boundingBox())!.height).toBeLessThanOrEqual(32);
  await row.click();
  const details = page.getByRole('dialog', { name: 'Details for Sync history' });
  await expect(details.getByText('imported: 2')).toBeVisible();
  await expect(details.getByRole('progressbar')).toHaveAttribute('value', '3');
  expect(await details.evaluate((panel) => {
    const rect = panel.getBoundingClientRect();
    return panel.contains(document.elementFromPoint(rect.left + rect.width / 2, rect.top + rect.height / 2));
  })).toBe(true);
  await details.getByText('imported: 2').click({ trial: true });
  await page.keyboard.press('Escape');
  await expect(details).not.toBeVisible();
  await expect(row).toBeFocused();
  await expect(page.getByRole('dialog', { name: 'Activity', exact: true })).toBeVisible();
});

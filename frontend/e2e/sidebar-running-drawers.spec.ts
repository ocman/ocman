import { test, expect, MOCK_SESSION } from './fixtures';

test.use({ factoryBeta: true });

for (const [label, origin] of [
  ['Routines', { routineId: 'run' }],
  ['Factory', { factoryAttemptId: 'attempt' }],
  ['Plugins', { origin: 'plugin' }],
] as const) {
  for (const view of ['recent', 'projects'] as const) {
    test(`selected and pinned ${label} sessions stay in their highlighted category in ${view} view`, async ({ mockedPage: page }) => {
      const selected = { ...MOCK_SESSION, ...origin, title: 'Selected automated work', backgroundTaskCount: 1 };
      const pinned = { ...MOCK_SESSION, id: 'pinned-child', parentId: selected.id, title: 'Pinned automated child', pinned: true, status: 'busy' };
      await page.route('/api/sessions*', route => route.fulfill({ json: [selected, pinned] }));
      await page.goto(`/session/${selected.id}`);
      if (view === 'projects') await page.getByRole('button', { name: 'Group sessions by project', exact: true }).click();
      const sidebar = page.getByTestId('session-sidebar');
      const toggle = sidebar.getByRole('button', { name: `${label} 2`, exact: true });
      await expect(toggle).toHaveAttribute('aria-current', 'true');
      await expect(toggle).toHaveAttribute('aria-expanded', 'true');
      const category = sidebar.getByRole('region', { name: `${label} 2`, exact: true });
      await expect(category.getByText(selected.title)).toBeVisible();
      await expect(category.getByText(pinned.title)).toBeVisible();
      await expect(category.locator('[aria-selected="true"]')).toHaveCount(1);
      await expect(sidebar.getByText(selected.title)).toHaveCount(1);
      await expect(sidebar.getByText(pinned.title)).toHaveCount(1);
      await toggle.click();
      await expect(toggle).toHaveAttribute('aria-current', 'true');
      await expect(sidebar.getByText(selected.title)).toHaveCount(0);
      await expect(sidebar.getByText(pinned.title)).toHaveCount(0);
    });
  }
}

const rows = [
  { ...MOCK_SESSION, status: 'done', seen: true, title: 'Sidebar filter redesign' },
  { ...MOCK_SESSION, id: 'archive', status: 'busy', archived: true, title: 'Deploy hotfix' },
  { ...MOCK_SESSION, id: 'archive-child', parentId: 'archive', status: 'busy', archived: true, title: 'Rebuild search index' },
  { ...MOCK_SESSION, id: 'routine', status: 'busy', routineId: 'nightly', title: 'Nightly checks' },
  { ...MOCK_SESSION, id: 'plugin', status: 'busy', origin: 'plugin', title: 'Review pull request' },
  { ...MOCK_SESSION, id: 'done', status: 'done', archived: true, title: 'Finished archive' },
  { ...MOCK_SESSION, id: 'snoozed', status: 'done', archived: true, snoozedUntil: 9999999999999, title: 'Resume tomorrow' },
];

for (const view of ['recent', 'projects'] as const) {
  test(`automated sessions stay in their drawers in ${view} view`, async ({ mockedPage: page }) => {
    const automated = [
      rows[3], rows[4],
      { ...MOCK_SESSION, id: 'factory', status: 'busy', factoryAttemptId: 'attempt', title: 'Factory implementation' },
    ];
    await page.route('/api/sessions*', route => route.fulfill({ json: [rows[0], ...automated] }));
    await page.goto(`/session/${MOCK_SESSION.id}`);
    if (view === 'projects') await page.getByRole('button', { name: 'Group sessions by project', exact: true }).click();
    const sidebar = page.getByTestId('session-sidebar');
    await expect(sidebar.getByText(rows[0].title)).toBeVisible();
    const drawers = sidebar.getByRole('group', { name: 'Session drawers' });
    for (const [index, label] of ['Routines', 'Plugins', 'Factory'].entries()) {
      const title = automated[index].title;
      await expect(sidebar.getByText(title)).toHaveCount(0);
      await drawers.getByRole('button', { name: `${label} 1`, exact: true }).click();
      await expect(drawers.getByText(title)).toBeVisible();
      await expect(sidebar.getByText(title)).toHaveCount(1);
    }
  });
}

test('muted drawers share search, open independently and keep rows unindented', async ({ mockedPage: page }, testInfo) => {
  await page.route('/api/sessions*', route => route.fulfill({ json: rows }));
  await page.goto(`/session/${MOCK_SESSION.id}`);
  await page.getByRole('button', { name: 'Group sessions by project', exact: true }).click();
  const sidebar = page.getByTestId('session-sidebar');
  const drawers = sidebar.getByRole('group', { name: 'Session drawers' });
  const archive = drawers.getByRole('button', { name: 'Background 2', exact: true });
  const routines = drawers.getByRole('button', { name: 'Routines 1', exact: true });
  const plugins = drawers.getByRole('button', { name: 'Plugins 1', exact: true });
  const snoozed = drawers.getByRole('button', { name: 'Snoozed 1', exact: true });
  await expect(archive).toHaveAttribute('aria-expanded', 'false');
  await expect(drawers.getByText('Deploy hotfix')).not.toBeVisible();
  await expect(archive).toHaveCSS('color', 'rgb(108, 112, 134)');
  await sidebar.screenshot({ path: testInfo.outputPath('drawers-closed.png') });
  await archive.focus();
  await page.keyboard.press('Enter');
  await routines.click();
  await plugins.click();
  await expect(drawers.getByText('Deploy hotfix')).toBeVisible();
  await expect(drawers.getByText('Nightly checks')).toBeVisible();
  const first = await drawers.getByRole('button').filter({ hasText: 'Deploy hotfix' }).boundingBox();
  const child = await drawers.getByRole('button').filter({ hasText: 'Rebuild search index' }).boundingBox();
  expect(first?.x).toBe(child?.x);
  await sidebar.screenshot({ path: testInfo.outputPath('drawers-open.png') });
  await snoozed.click();
  await expect(drawers.getByText('Resume tomorrow')).toBeVisible();
  await sidebar.getByRole('searchbox', { name: 'Search sessions' }).fill('Deploy hotfix');
  await expect(archive).toHaveCount(0);
  await expect(drawers.getByRole('button', { name: 'Background 1', exact: true })).toHaveAttribute('aria-expanded', 'true');
  await expect(routines).toHaveCount(0);
  await expect(plugins).toHaveCount(0);
  await expect(snoozed).toHaveCount(0);
  await expect(drawers.getByText('Deploy hotfix')).toBeVisible();
  await sidebar.getByRole('searchbox', { name: 'Search sessions' }).fill('no matching sessions');
  await expect(drawers).toHaveCount(0);
  await sidebar.getByRole('searchbox', { name: 'Search sessions' }).fill('');
  await drawers.getByRole('button').filter({ hasText: 'Deploy hotfix' }).click();
  await expect(page).toHaveURL(/\/session\/archive$/);
});

test('expanded drawers stay scrollable and within the phone sidebar', async ({ mockedPage: page }) => {
  await page.setViewportSize({ width: 390, height: 700 });
  const many = Array.from({ length: 25 }, (_, index) => ({ ...rows[1], id: `archive-${index}`, title: `Archived run ${index}` }));
  await page.route('/api/sessions*', route => route.fulfill({ json: [rows[0], ...many, rows[3], rows[4]] }));
  await page.goto(`/session/${MOCK_SESSION.id}`);
  await page.getByRole('button', { name: 'Open session details', exact: true }).click();
  await page.getByRole('button', { name: 'Open session list', exact: true }).click();
  await page.getByRole('button', { name: 'Group sessions by project', exact: true }).click();
  const sidebar = page.getByTestId('session-sidebar');
  const drawers = sidebar.getByRole('group', { name: 'Session drawers' });
  await drawers.getByRole('button', { name: 'Background 25', exact: true }).click();
  await drawers.getByRole('button', { name: 'Plugins 1', exact: true }).scrollIntoViewIfNeeded();
  await expect(drawers.getByRole('button', { name: 'Plugins 1', exact: true })).toBeVisible();
  const dimensions = await sidebar.evaluate(element => ({ width: element.clientWidth, content: element.scrollWidth }));
  expect(dimensions.content).toBeLessThanOrEqual(dimensions.width);
});

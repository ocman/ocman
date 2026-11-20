import { test, expect, MOCK_SESSION, MOCK_SESSION_2 } from './fixtures';

test('shows the session family, expands branches and navigates to a sibling', async ({ mockedPage: page }) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  const parent = { ...MOCK_SESSION_2, title: 'Implement authentication', status: 'done' };
  const current = { ...MOCK_SESSION, title: 'Build login flow', parentId: parent.id, status: 'waiting' };
  const research = { ...parent, id: 'research', title: 'Research providers', parentId: parent.id, timeCreated: current.timeCreated - 1 };
  const docs = { ...parent, id: 'docs', title: 'Update documentation', parentId: parent.id, timeCreated: current.timeCreated + 1 };
  const regression = { ...parent, id: 'regression', title: 'Write regression tests', parentId: current.id };
  const review = { ...parent, id: 'review', title: 'Review implementation', creator: { platform: current.platform, sessionId: current.id, createdAt: current.timeCreated }, status: 'waiting' };
  const rows = [parent, current, research, docs, regression, review];

  await page.route('/api/sessions*', route => route.fulfill({ json: rows.filter(row => row.id !== regression.id) }));
  await page.route(/\/api\/session\/[^/?]+(?:\?.*)?$/, route => {
    const id = new URL(route.request().url()).pathname.split('/').at(-1);
    return route.fulfill({ json: { session: rows.find(row => row.id === id), sessionTree: [parent, current, regression], messages: [], parts: [], totalMessages: 0 } });
  });

  await page.goto(`/session/${current.id}`);
  await page.getByRole('tab', { name: 'Lineage', exact: true }).click();
  const pane = page.getByRole('region', { name: 'Session lineage' });
  await expect(pane.getByRole('link', { name: /Build login flow/ })).toHaveAttribute('aria-current', 'page');
  await expect(pane.getByRole('link', { name: /Write regression tests/ })).toBeVisible();
  await expect(pane.getByRole('link', { name: /Review implementation/ })).toBeVisible();

  if (process.env.LINEAGE_SCREENSHOT) {
    await page.screenshot({ path: process.env.LINEAGE_SCREENSHOT, animations: 'disabled' });
  }

  await pane.getByRole('button', { name: 'Collapse Build login flow' }).click();
  await expect(pane.getByRole('link', { name: /Write regression tests/ })).toHaveCount(0);
  await pane.getByRole('button', { name: 'Expand Build login flow' }).click();
  await expect(pane.getByRole('link', { name: /Write regression tests/ })).toBeVisible();
  await pane.getByRole('link', { name: /Research providers/ }).click();
  await expect(page).toHaveURL(/\/session\/research$/);
  await expect(pane.getByRole('link', { name: /Research providers/ })).toHaveAttribute('aria-current', 'page');
});

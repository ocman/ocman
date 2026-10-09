import { test, expect, MOCK_SESSION } from './fixtures';

test('sidebar project options remain clickable over the conversation', async ({ mockedPage: page }, testInfo) => {
  const directory = '/home/user/projects/myapp-with-a-long-project-name';
  const label = 'projects/myapp-with-a-long-project-name';
  await page.route('/api/sessions*', (route) => route.fulfill({ json: [{ ...MOCK_SESSION, directory }] }));
  await page.goto(`/session/${MOCK_SESSION.id}`);
  await page.getByRole('button', { name: 'Filter sessions', exact: true }).click();
  await page.getByRole('combobox', { name: 'Project', exact: true }).click();
  const option = page.getByRole('option', { name: label, exact: true });
  await expect(option).toBeVisible();
  await page.screenshot({ path: testInfo.outputPath('project-filter.png') });
  const box = (await option.boundingBox())!;
  const sidebar = page.getByTestId('session-sidebar');
  const sidebarBox = (await sidebar.boundingBox())!;
  const sidebarEdge = sidebarBox.x + sidebarBox.width;
  expect(box.x + box.width).toBeGreaterThan(sidebarEdge);
  expect(await option.evaluate((element, edge) => {
    const rect = element.getBoundingClientRect();
    return element.contains(document.elementFromPoint(Math.max(edge + 8, rect.x + 8), rect.y + rect.height / 2));
  }, sidebarEdge)).toBe(true);
  await option.click();
  await expect(page.getByRole('combobox', { name: 'Project', exact: true })).toContainText(label);
  await expect(page.getByRole('group', { name: 'Session filters' })).toBeVisible();
  await page.getByRole('button', { name: 'Filter sessions', exact: true }).click();
  await expect(sidebar).toHaveCSS('overflow', 'hidden');
});

test('sidebar project menu stays inside the phone viewport', async ({ mockedPage: page }, testInfo) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto(`/session/${MOCK_SESSION.id}`);
  await page.getByRole('button', { name: 'Open session list', exact: true }).click();
  await page.getByRole('button', { name: 'Filter sessions', exact: true }).click();
  await page.getByRole('combobox', { name: 'Project', exact: true }).click();
  const search = page.getByRole('textbox', { name: 'Search projects', exact: true });
  await expect(search).toBeFocused();
  const box = (await search.boundingBox())!;
  expect(box.x).toBeGreaterThanOrEqual(0);
  expect(box.x + box.width).toBeLessThanOrEqual(390);
  await page.screenshot({ path: testInfo.outputPath('project-filter-phone.png') });
  await page.getByRole('option', { name: 'projects/myapp', exact: true }).click();
  await expect(page.getByRole('combobox', { name: 'Project', exact: true })).toContainText('projects/myapp');
});

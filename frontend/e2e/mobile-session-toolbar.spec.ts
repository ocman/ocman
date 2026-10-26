import { test, expect, MOCK_SESSION, MOCK_SESSION_2 } from './fixtures';

for (const width of [320, 390, 640]) {
  test(`mobile session toolbar stays on one 44px row at ${width}px`, async ({ mockedPage: page }) => {
    await page.setViewportSize({ width, height: 844 });
    await page.goto('/sessions');
    const toolbar = page.getByRole('group', { name: 'Session list controls' });
    const search = toolbar.getByRole('searchbox', { name: 'Search sessions' });
    const searchBox = (await search.boundingBox())!;
    for (const label of ['Choose project', 'Filter sessions', 'New session']) {
      const box = (await toolbar.getByRole('button', { name: label, exact: true }).boundingBox())!;
      expect(box.y).toBe(searchBox.y);
      expect(box.width).toBeGreaterThanOrEqual(44);
      expect(box.height).toBe(44);
      expect(box.x + box.width).toBeLessThanOrEqual(width);
    }
    expect((await toolbar.boundingBox())!.height).toBe(44);
    await expect(page.getByRole('radiogroup', { name: 'Time range' })).toHaveCount(0);
    await expect(page.getByRole('radiogroup', { name: 'Session status' })).toHaveCount(0);
    const row = (await page.getByRole('link', { name: /Fix the login bug/ }).boundingBox())!;
    expect(row.y - searchBox.y - searchBox.height).toBeLessThanOrEqual(16);
    await toolbar.getByRole('button', { name: 'Filter sessions' }).click();
    const filters = page.getByRole('dialog', { name: 'Session filters' });
    await expect(filters.getByRole('checkbox')).toHaveCount(7);
    await expect(filters.getByRole('checkbox', { name: 'Show routines' })).toBeVisible();
    await expect(filters.getByRole('checkbox', { name: 'Show plugins' })).toBeVisible();
    await expect(filters.getByRole('checkbox', { name: 'Show archived' })).toBeVisible();
    const box = (await filters.boundingBox())!;
    expect(box.x).toBeGreaterThanOrEqual(0);
    expect(box.x + box.width).toBeLessThanOrEqual(width);
    expect((await page.getByRole('link', { name: /Fix the login bug/ }).boundingBox())!.y).toBe(row.y);
  });
}

test('phone filter selections survive closing, and desktop keeps its inline filters', async ({ mockedPage: page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.route('/api/sessions*', route => route.fulfill({ json: [
    { ...MOCK_SESSION, status: 'busy' }, { ...MOCK_SESSION_2, status: 'waiting', parentId: MOCK_SESSION.id },
  ] }));
  await page.goto('/sessions');
  const toggle = page.getByRole('button', { name: 'Filter sessions' });
  await toggle.click();
  await page.getByRole('checkbox', { name: 'Show children', exact: true }).uncheck();
  await toggle.click();
  await expect(page.getByRole('link', { name: /Refactor auth module/ })).toHaveCount(0);
  await expect(toggle).toHaveClass(/oc-button--accent/);
  await toggle.click();
  await expect(page.getByRole('checkbox', { name: 'Show children', exact: true })).not.toBeChecked();
  await page.setViewportSize({ width: 641, height: 844 });
  await expect(toggle).toHaveCount(0);
  await expect(page.getByRole('radiogroup', { name: 'Time range' })).toBeVisible();
  await expect(page.getByRole('link', { name: /Refactor auth module/ })).toBeVisible();
});

test('project options opened from the icon fit the phone and preserve scope selection', async ({ mockedPage: page }) => {
  await page.setViewportSize({ width: 320, height: 844 });
  await page.goto('/sessions');
  const project = page.getByRole('button', { name: 'Choose project' });
  await project.click();
  await page.getByRole('combobox', { name: 'Project scope' }).click();
  const option = page.getByRole('option', { name: 'projects/myapp', exact: true });
  await expect(option).toBeVisible();
  const box = (await option.boundingBox())!;
  expect(box.x).toBeGreaterThanOrEqual(0);
  expect(box.x + box.width).toBeLessThanOrEqual(320);
  await option.click();
  await expect(page.getByRole('dialog')).toHaveCount(0);
  await expect(project).toHaveClass(/oc-button--accent/);
  await expect(project).toBeFocused();
});

test('phone filters can widen a retained time range and fetch older sessions', async ({ mockedPage: page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  const older = { ...MOCK_SESSION_2, timeUpdated: Date.now() - 48 * 3600_000 };
  await page.route('/api/sessions*', route => {
    const since = Number(new URL(route.request().url()).searchParams.get('since'));
    return route.fulfill({ json: since > older.timeUpdated ? [MOCK_SESSION] : [MOCK_SESSION, older] });
  });
  await page.goto('/sessions?t=12');
  await expect(page.getByRole('link', { name: /Fix the login bug/ })).toBeVisible();
  await expect(page.getByRole('link', { name: /Refactor auth module/ })).toHaveCount(0);
  await page.getByRole('button', { name: 'Filter sessions' }).click();
  const range = page.getByRole('radiogroup', { name: 'Time range' });
  await expect(range.getByRole('radio', { name: '12h' })).toBeChecked();
  await range.getByRole('radio', { name: '7d' }).click();
  await expect(range.getByRole('radio', { name: '7d' })).toBeChecked();
  await expect(page).toHaveURL(/t=168/);
  await expect(page.getByRole('link', { name: /Refactor auth module/ })).toBeVisible();
});

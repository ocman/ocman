import { test, expect, MOCK_SESSION } from './fixtures';

test.use({ viewport: { width: 1280, height: 900 }, timezoneId: 'UTC', serviceWorkers: 'block' });

test.beforeEach(async ({ mockedPage: page }) => {
  await page.clock.setFixedTime(new Date('2026-10-28T22:32:00Z'));
  await page.route('/api/sessions*', route => route.fulfill({ json: [{
    ...MOCK_SESSION,
    timeCreated: Date.parse('2026-10-28T21:32:00Z'),
    timeUpdated: Date.parse('2026-10-28T22:31:00Z'),
  }] }));
});

test('copies the desktop session ID from the keyboard without opening the row', async ({ mockedPage: page }) => {
  await page.addInitScript(() => {
    Object.defineProperty(navigator, 'clipboard', {
      value: { writeText: async (text: string) => { sessionStorage.setItem('copied-text', text); } },
    });
  });
  await page.goto('/sessions');
  const row = page.getByRole('row').filter({ hasText: MOCK_SESSION.title });
  const link = row.getByRole('link', { name: new RegExp(MOCK_SESSION.title) });
  const copy = row.getByRole('button', { name: 'Copy session ID' });
  await expect(row.getByText(MOCK_SESSION.id, { exact: true })).toBeVisible();
  await expect(copy).toBeVisible();
  await link.focus();
  await page.keyboard.press('Tab');
  await expect(copy).toBeFocused();
  await page.keyboard.press('Enter');
  await expect(row.getByRole('status').filter({ hasText: 'Copied!' })).toBeVisible();
  expect(await page.evaluate(() => sessionStorage.getItem('copied-text'))).toBe(MOCK_SESSION.id);
  await expect(page).toHaveURL(/\/sessions$/);

  await link.press('Enter');
  await expect(page).toHaveURL(new RegExp(`/session/${MOCK_SESSION.id}$`));
  await page.goBack();
  await row.getByText('12', { exact: true }).click();
  await expect(page).toHaveURL(new RegExp(`/session/${MOCK_SESSION.id}$`));
});

test('reveals the exact Started time on keyboard focus', async ({ mockedPage: page }) => {
  await page.goto('/sessions');
  const row = page.getByRole('row').filter({ hasText: MOCK_SESSION.title });
  const started = row.getByText(/^1h ago/);
  await expect(started).toBeVisible();
  // Start just before the Started column (rather than focusing the timestamp programmatically).
  await row.getByRole('link', { name: new RegExp(MOCK_SESSION.title) }).focus();
  await page.keyboard.press('Tab');
  await page.keyboard.press('Tab');
  await expect(started).toBeFocused();
  await expect(page.getByRole('tooltip')).toHaveText('Oct 28, 21:32');
  await page.keyboard.press('Escape');
  await expect(page.getByRole('tooltip')).toBeHidden();
  await expect(page).toHaveURL(/\/sessions$/);
});

test('keeps the session ID hidden and the session link visible on phones', async ({ mockedPage: page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto('/sessions');
  const link = page.getByRole('link', { name: new RegExp(MOCK_SESSION.title) });
  await expect(link).toBeVisible();
  await expect(page.getByText(MOCK_SESSION.id, { exact: true })).toBeHidden();
  await expect(page.getByRole('button', { name: 'Copy session ID' })).toBeHidden();
  await link.click();
  await expect(page).toHaveURL(new RegExp(`/session/${MOCK_SESSION.id}$`));
});

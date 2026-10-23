import { test, expect, MOCK_SESSION, MOCK_SESSION_2 } from './fixtures';

test('bottom stats start collapsed, align expanded items and remember the choice', async ({ mockedPage: page }, testInfo) => {
  await page.route(/\/api\/sessions\?view=running-count$/, (route) => route.fulfill({ json: { count: 5 } }));
  await page.goto(`/session/${MOCK_SESSION.id}`);
  await expect(page.getByRole('button', { name: 'Total active sessions: 5' })).toHaveText('tas: 5');
  const show = page.getByRole('button', { name: 'Show system stats' });
  await expect(show).toHaveAttribute('aria-expanded', 'false');
  await expect(page.getByTitle('Backend memory usage')).toHaveCount(0);
  await page.getByTestId('session-sidebar').screenshot({ path: testInfo.outputPath('collapsed.png') });

  await show.click();
  const backend = page.getByTitle('Backend memory usage');
  const uptime = page.getByTitle('Time since the backend started');
  await expect(backend).toBeVisible();
  await expect(uptime).toBeVisible();
  const backendBox = (await backend.boundingBox())!;
  const frontendBox = (await page.getByTitle(/^Frontend Memory \(/).boundingBox())!;
  const uptimeBox = (await uptime.boundingBox())!;
  expect(frontendBox.y).toBe(backendBox.y);
  expect(frontendBox.x).toBeGreaterThan(backendBox.x);
  expect([backendBox.x, frontendBox.x]).toContain(uptimeBox.x);
  await page.getByTestId('session-sidebar').screenshot({ path: testInfo.outputPath('expanded.png') });
  await page.reload();
  await expect(page.getByRole('button', { name: 'Hide system stats' })).toHaveAttribute('aria-expanded', 'true');
  await page.getByRole('button', { name: 'Hide system stats' }).click();
  await page.reload();
  await expect(page.getByRole('button', { name: 'Show system stats' })).toHaveAttribute('aria-expanded', 'false');
});

test('active session popover groups projects, accepts keyboard entry and closes on selection', async ({ mockedPage: page }) => {
  await page.route(/\/api\/sessions\?view=running-count$/, route => route.fulfill({ json: { count: 3 } }));
  // The open session's row follows its live detail status (waiting), so the
  // busy row in its project is another session.
  const busy = { ...MOCK_SESSION, id: 'busy-myapp', title: 'Busy myapp task', status: 'busy' };
  await page.route(url => url.pathname === '/api/sessions' && url.searchParams.get('limit') === '0' && url.searchParams.get('view') === 'sidebar', route => route.fulfill({ json: [
    busy,
    { ...MOCK_SESSION_2, status: 'busy', archived: true, parentId: MOCK_SESSION.id },
    { ...MOCK_SESSION, id: 'other', title: 'Other project task', directory: '/projects/other', status: 'busy' },
  ] }));
  await page.goto(`/session/${MOCK_SESSION.id}`);
  const trigger = page.getByRole('button', { name: 'Total active sessions: 3' });
  await trigger.hover();
  const panel = page.getByRole('dialog', { name: 'Active sessions' });
  await expect(panel).toBeVisible();
  await expect(panel.getByRole('heading', { level: 2, name: 'Total active sessions' })).toBeVisible();
  await expect(panel.getByRole('heading', { level: 3 })).toHaveCount(2);
  await expect(panel.getByRole('link')).toHaveCount(3);
  await trigger.focus();
  await page.keyboard.press('Tab');
  await expect(panel).toBeFocused();
  await page.keyboard.press('Tab');
  await expect(panel.getByRole('link', { name: busy.title })).toBeFocused();
  await panel.getByRole('link', { name: MOCK_SESSION_2.title }).click();
  await expect(panel).toHaveCount(0);
  await expect(page).toHaveURL(new RegExp(`/session/${MOCK_SESSION_2.id}\\?platform=opencode$`));
});

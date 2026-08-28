/**
 * e2e: Dashboard (Sessions / Projects / Analytics / Settings views)
 *
 * Covers:
 *  - All sidebar links are visible and navigable
 *  - Sessions tab renders the session table with mock data
 *  - Time-range filter buttons are rendered and change the active state
 *  - Projects tab renders a project row with mock data
 *  - Analytics renders metrics, logs, and usage filters
 *  - Settings tab renders the bell sound toggle
 *  - Clicking a session row navigates to the session detail page
 *  - Clicking a project row navigates to the project detail page
 *  - Archive button is present on each session row
 *  - "No active sessions found" empty state when sessions list is empty
 */

import { test, expect, MOCK_SESSION, MOCK_SESSION_2, MOCK_PROJECT } from './fixtures';

// ---------------------------------------------------------------------------
// Navigation
// ---------------------------------------------------------------------------

test('all dashboard destinations are visible with Settings at the bottom', async ({ mockedPage: page }) => {
  await page.goto('/sessions');
  const nav = page.getByRole('navigation', { name: 'Main navigation' });
  await expect(page.getByRole('banner').getByRole('heading', { name: 'Sessions' })).toBeVisible();
  for (const label of ['Home', 'Sessions', 'Projects', 'Analytics', 'Settings']) {
    await expect(nav.getByRole('link', { name: label })).toBeVisible();
  }

  const navBox = await nav.boundingBox();
  const settingsBox = await nav.getByRole('link', { name: 'Settings' }).boundingBox();
  expect(navBox).not.toBeNull();
  expect(settingsBox).not.toBeNull();
  expect(navBox!.y + navBox!.height - settingsBox!.y - settingsBox!.height).toBeLessThanOrEqual(12);
});

test('dashboard content has space below the header', async ({ mockedPage: page }) => {
  await page.goto('/sessions');
  await expect(page.locator('.dashboard-content')).toHaveCSS('padding-top', '24px');
});

test('clicking Settings navigates to /settings', async ({ mockedPage: page }) => {
  await page.goto('/sessions');
  await page.getByRole('navigation', { name: 'Main navigation' }).getByRole('link', { name: 'Settings' }).click();
  await expect(page).toHaveURL('/settings');
  await expect(page.getByRole('link', { name: 'Settings', current: 'page' })).toBeVisible();
});

test('settings tab shows bell sound toggle', async ({ mockedPage: page }) => {
  await page.goto('/settings');
  // Scope to the Bell sound row — there are now multiple toggle rows
  // in the Notifications section (system notifications + bell sound).
  const bellRow = page.locator('.settings-row', { has: page.locator('.settings-row-label', { hasText: 'Bell sound' }) });
  await expect(bellRow.locator('.settings-row-label', { hasText: 'Bell sound' })).toBeVisible();
  // The <input> is visually hidden for styling (see .settings-toggle input in
  // Dashboard.css); assert on the label (which IS visible) and that the
  // checkbox is attached to the DOM.
  await expect(bellRow.locator('.settings-toggle')).toBeVisible();
  await expect(bellRow.locator('.settings-toggle input[type="checkbox"]')).toBeAttached();
});

test('settings sidebar switches the visible group', async ({ mockedPage: page }) => {
  await page.goto('/settings');
  // Notifications is the default group: its Bell sound row is visible,
  // while the Auto-approve group's content is hidden.
  await expect(page.locator('.settings-row-label', { hasText: 'Bell sound' })).toBeVisible();
  await expect(page.locator('.settings-row-label', { hasText: 'Human review window' })).toBeHidden();

  // Selecting the Auto-approve sidebar item reveals it and hides Notifications.
  await page.getByRole('button', { name: 'Auto-approve' }).click();
  await expect(page.locator('.settings-row-label', { hasText: 'Human review window' })).toBeVisible();
  await expect(page.locator('.settings-row-label', { hasText: 'Bell sound' })).toBeHidden();
});

test('settings sidebar uses a stable sticky offset', async ({ mockedPage: page }) => {
  await page.goto('/settings');
  const nav = page.getByRole('navigation', { name: 'Settings groups' });
  await expect(nav).toHaveCSS('top', '0px');
});

test('Sessions navigation is active by default', async ({ mockedPage: page }) => {
  await page.goto('/sessions');
  await expect(page.getByRole('link', { name: 'Sessions', current: 'page' })).toBeVisible();
});

test('clicking Projects navigates to /projects', async ({ mockedPage: page }) => {
  await page.goto('/sessions');
  await page.getByRole('link', { name: 'Projects' }).click();
  await expect(page).toHaveURL('/projects');
  await expect(page.getByRole('link', { name: 'Projects', current: 'page' })).toBeVisible();
});

test('clicking Analytics opens its overview', async ({ mockedPage: page }) => {
  await page.goto('/sessions');
  await page.getByRole('link', { name: 'Analytics' }).click();
  await expect(page).toHaveURL('/analytics/overview');
  await expect(page.getByRole('link', { name: 'Analytics', current: 'page' })).toBeVisible();
});

test('logo collapses and expands the sidebar', async ({ mockedPage: page }) => {
  await page.goto('/stats');
  await page.getByRole('button', { name: 'Collapse navigation' }).click();
  await expect(page.getByRole('button', { name: 'Expand navigation' })).toBeVisible();
  await page.getByRole('button', { name: 'Expand navigation' }).click();
  await expect(page.getByRole('button', { name: 'Collapse navigation' })).toBeVisible();
});

test('Home returns to the last opened active session', async ({ mockedPage: page }) => {
  await page.goto(`/session/${MOCK_SESSION_2.id}`);
  await expect(page.getByTestId('session-layout')).toBeVisible();

  const nav = page.getByRole('navigation', { name: 'Main navigation' });
  await nav.getByRole('link', { name: 'Sessions' }).click();
  await expect(page).toHaveURL('/sessions');
  await nav.getByRole('link', { name: 'Home' }).click();

  await expect(page).toHaveURL(`/session/${MOCK_SESSION_2.id}`);
});

test.describe('phone navigation', () => {
  test.use({ viewport: { width: 390, height: 844 } });

  test('opens from the header and closes after navigation', async ({ mockedPage: page }) => {
    await page.goto('/sessions');
    await page.getByRole('button', { name: 'Open navigation' }).click();
    await expect(page.getByRole('button', { name: 'Close navigation' }).first()).toBeVisible();

    await page.getByRole('link', { name: 'Projects' }).click();

    await expect(page).toHaveURL('/projects');
    await expect(page.getByRole('button', { name: 'Open navigation' })).toBeVisible();
  });
});

// ---------------------------------------------------------------------------
// Root redirect (/)
// ---------------------------------------------------------------------------

test('/ redirects to the latest session when sessions exist', async ({ mockedPage: page }) => {
  await page.goto('/');
  // The first session returned by /api/sessions is the redirect target.
  await expect(page).toHaveURL(`/session/${MOCK_SESSION.id}`);
});

test('/ redirects to /session/new when no sessions exist', async ({ mockedPage: page }) => {
  await page.route('/api/sessions*', (route) =>
    route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify([]) }),
  );
  await page.goto('/');
  await expect(page).toHaveURL('/session/new');
});

// ---------------------------------------------------------------------------
// Sessions tab
// ---------------------------------------------------------------------------

test('sessions tab shows session titles from mock data', async ({ mockedPage: page }) => {
  await page.goto('/sessions');
  await expect(page.locator('.session-title', { hasText: 'Fix the login bug' })).toBeVisible();
  await expect(page.locator('.session-title', { hasText: 'Refactor auth module' })).toBeVisible();
});

test('sessions tab shows time-range filter buttons', async ({ mockedPage: page }) => {
  await page.goto('/sessions');
  for (const label of ['12h', '24h', '7d', '30d', 'All']) {
    await expect(page.locator('.oc-time-range-btn', { hasText: label })).toBeVisible();
  }
});

test('time-range filter button becomes active when clicked', async ({ mockedPage: page }) => {
  await page.goto('/sessions');
  const btn7d = page.locator('.oc-time-range-btn', { hasText: '7d' });
  await btn7d.click();
  await expect(btn7d).toHaveClass(/active/);
  await expect(page).toHaveURL(/[?&]t=168/);
});

test('archive button present on each session row', async ({ mockedPage: page }) => {
  await page.goto('/sessions');
  const archiveBtns = page.locator('button[aria-label="Archive session"]');
  await expect(archiveBtns).toHaveCount(2);
});

test('clicking a session row navigates to session detail', async ({ mockedPage: page }) => {
  await page.goto('/sessions');
  await page.locator('.session-title', { hasText: 'Fix the login bug' }).click();
  await expect(page).toHaveURL(`/session/${MOCK_SESSION.id}`);
});

test('pressing Enter on a focused session row navigates to session detail', async ({
  mockedPage: page,
}) => {
  await page.goto('/sessions');
  // The row-wide onClick is mouse-only, so the row's primary cell has to
  // expose a real link: reachable with Tab, activated with Enter.
  const rowLink = page.getByRole('link', { name: /Fix the login bug/ });
  await expect(rowLink).toBeVisible();

  await rowLink.focus();
  await expect(rowLink).toBeFocused();

  await page.keyboard.press('Enter');
  await expect(page).toHaveURL(`/session/${MOCK_SESSION.id}`);
});

test('sessions tab shows empty state when no sessions', async ({ mockedPage: page }) => {
  await page.route('/api/sessions*', (route) =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify([]),
    }),
  );
  await page.goto('/sessions');
  // SessionTable shows 'No sessions found' when includeArchived=true (default), or
  // 'No active sessions found' when includeArchived=false. Either way, the empty
  // cell should be visible.
  await expect(page.locator('td').filter({ hasText: /No (active )?sessions found/ })).toBeVisible();
});

test('sessions tab shows loading skeleton before data arrives', async ({ mockedPage: page }) => {
  let resolveSessions!: (v: unknown) => void;
  const sessionsPromise = new Promise((resolve) => { resolveSessions = resolve; });

  await page.route('/api/sessions*', async (route) => {
    await sessionsPromise;
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify([MOCK_SESSION]),
    });
  });

  const gotoPromise = page.goto('/sessions');
  await expect(page.locator('.oc-skeleton-tbody').first()).toBeVisible({ timeout: 3_000 });
  resolveSessions(null);
  await gotoPromise;
});

// ---------------------------------------------------------------------------
// Projects tab
// ---------------------------------------------------------------------------

test('projects tab shows project from mock data', async ({ mockedPage: page }) => {
  await page.goto('/projects');
  await expect(page.locator('td', { hasText: 'myapp' })).toBeVisible();
});

test('projects tab shows column headers', async ({ mockedPage: page }) => {
  await page.goto('/projects');
  for (const header of ['Project', 'Sessions', 'Messages', 'Last Active']) {
    await expect(page.locator('th', { hasText: header })).toBeVisible();
  }
});

test('clicking a project row navigates to project detail', async ({ mockedPage: page }) => {
  await page.goto('/projects');
  await page.locator('td', { hasText: 'myapp' }).first().click();
  await expect(page).toHaveURL(
    `/project/${encodeURIComponent(MOCK_PROJECT.directory)}`,
  );
});

test('projects tab shows empty state when no projects have sessions', async ({
  mockedPage: page,
}) => {
  await page.route('/api/projects*', (route) =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify([{ ...MOCK_PROJECT, sessionCount: 0 }]),
    }),
  );
  await page.goto('/projects');
  await expect(page.locator('td', { hasText: 'No projects found' })).toBeVisible();
});

// ---------------------------------------------------------------------------
// Analytics performance and logs
// ---------------------------------------------------------------------------

test('performance renders metrics summary cards', async ({ mockedPage: page }) => {
  await page.goto('/analytics/performance');
  // The MetricCard labels
  for (const label of ['Requests', 'Total Tokens', 'Total Cost']) {
    await expect(page.locator('.label', { hasText: label })).toBeVisible();
  }
});

test('performance shows agent and model filter dropdowns', async ({ mockedPage: page }) => {
  await page.goto('/analytics/performance');
  await expect(page.getByRole('combobox', { name: 'Agent' })).toBeVisible();
  await expect(page.getByRole('combobox', { name: 'Model' })).toBeVisible();
});

test('logs shows session log sub-tab', async ({ mockedPage: page }) => {
  await page.goto('/analytics/logs');
  await expect(page.getByRole('button', { name: 'Session Log' })).toBeVisible();
});

test('logs shows request log sub-tab', async ({ mockedPage: page }) => {
  await page.goto('/analytics/logs');
  await expect(page.getByRole('button', { name: 'Request Log' })).toBeVisible();
});

test('logs project log sub-tab switches view', async ({ mockedPage: page }) => {
  await page.goto('/analytics/logs');
  const projectLogTab = page.getByRole('button', { name: 'Project Log' });
  await projectLogTab.click();
  await expect(projectLogTab).toHaveClass(/active/);
});

// ---------------------------------------------------------------------------
// Analytics overview
// ---------------------------------------------------------------------------

test('overview renders project-scope, model, and date-range filters', async ({ mockedPage: page }) => {
  await page.goto('/analytics/overview');
  // Three SearchSelect controls: Project scope, Model, and Last N days
  const selects = page.locator('.metrics-filter .oc-search-select');
  await expect(selects).toHaveCount(3);
});

test('overview shows "All models" option in model filter', async ({ mockedPage: page }) => {
  await page.goto('/analytics/overview');
  // Open the Model filter and assert the "All models" option is listed.
  await page.getByRole('combobox', { name: 'Model' }).click();
  await expect(page.getByRole('option', { name: 'All models' })).toBeVisible();
});

test('overview can change date range', async ({ mockedPage: page }) => {
  await page.goto('/analytics/overview');
  const rangeSelect = page.getByRole('combobox', { name: 'Last' });
  await rangeSelect.click();
  await page.getByRole('option', { name: '7 days' }).click();
  await expect(rangeSelect).toHaveText('7 days');
});

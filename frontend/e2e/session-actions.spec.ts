/**
 * e2e: Session actions — rename, archive, project detail, keyboard shortcuts
 *
 * All API calls are mocked; no real agent or LLM is involved.
 *
 * Rename covers:
 *  - Rename modal opens via scoped command in command palette
 *  - Entering a new title in the modal calls PATCH /api/session/:id
 *  - Header title updates after rename
 *  - Cancel button closes modal without saving
 *  - Escape key closes modal without saving
 *  - "Session renamed" toast appears on success
 *
 * Archive covers:
 *  - Archive button in session table calls POST /api/session/archive
 *  - Session row disappears from table optimistically after archive
 *  - Archive button in session detail sidebar archives and navigates away
 *
 * Project detail covers:
 *  - /project/:dir renders the directory path and VS Code button
 *  - SessionTable inside project detail shows sessions for that project
 *  - "No sessions found" empty state when project has no sessions
 *
 * Keyboard shortcuts dialog covers:
 *  - Alt+Shift+? opens the dialog
 *  - Dialog has the correct aria role and label
 *  - Site-wide shortcuts section is visible
 *  - Close button / Escape / backdrop click closes the dialog
 */

import {
  test,
  expect,
  MOCK_SESSION,
  MOCK_SESSION_2,
  MOCK_PROJECT,
  mockSessionWithLiveConnection,
} from './fixtures';

const SESSION_URL = `/session/${MOCK_SESSION.id}`;

// ---------------------------------------------------------------------------
// Shared helpers
// ---------------------------------------------------------------------------

async function setupSessionPage(
  page: import('@playwright/test').Page,
  opts: { live?: boolean } = {},
) {
  const session = opts.live ? mockSessionWithLiveConnection() : MOCK_SESSION;
  await page.route(new RegExp(`/api/session/${MOCK_SESSION.id}(\\?|$)`), (route) =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ session, messages: [], parts: [], totalMessages: 0, defaultAgent: 'build', defaultModel: '' }),
    }),
  );
  await page.goto(SESSION_URL);
  await expect(page.locator('.session-layout')).toBeVisible();
}

// ===========================================================================
// RENAME
// ===========================================================================

test('rename modal opens via command palette scoped "rename" command', async ({ mockedPage: page }) => {
  await setupSessionPage(page, { live: true });

  // Open the command palette and select the "rename" scoped command
  await page.locator('body').click();
  await page.keyboard.press('Alt+Space');
  await expect(page.locator('.oc-cmd-palette')).toBeVisible({ timeout: 3_000 });
  await page.fill('.oc-cmd-input', 'rename');
  await expect(page.locator('.oc-cmd-item', { hasText: 'rename' })).toBeVisible();
  await page.locator('.oc-cmd-item', { hasText: 'rename' }).click();

  // The rename modal should appear
  await expect(page.locator('.oc-rename-dialog')).toBeVisible({ timeout: 3_000 });
  await expect(page.locator('h3', { hasText: 'Rename Session' })).toBeVisible();
});

test('rename modal: entering a title and clicking Rename calls PATCH', async ({ mockedPage: page }) => {
  await setupSessionPage(page, { live: true });

  await page.route(
    new RegExp(`/api/session/${encodeURIComponent(MOCK_SESSION.id)}$`),
    (route) => {
      if (route.request().method() === 'PATCH') {
        return route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ ok: true }) });
      }
      return route.fallback();
    },
  );

  // Open the rename modal via palette
  await page.locator('body').click();
  await page.keyboard.press('Alt+Space');
  await page.fill('.oc-cmd-input', 'rename');
  await page.locator('.oc-cmd-item', { hasText: 'rename' }).click();
  await expect(page.locator('.oc-rename-dialog')).toBeVisible({ timeout: 3_000 });

  // Clear and type new title
  const input = page.locator('.oc-rename-input');
  await input.fill('My new session title');

  const [req] = await Promise.all([
    page.waitForRequest(
      (r) => r.url().includes(`/api/session/${encodeURIComponent(MOCK_SESSION.id)}`) && r.method() === 'PATCH',
      { timeout: 5_000 },
    ),
    page.locator('button', { hasText: 'Rename' }).click(),
  ]);
  expect(JSON.parse(req.postData() ?? '{}')).toMatchObject({ title: 'My new session title' });
});

test('rename modal: Cancel closes without saving', async ({ mockedPage: page }) => {
  await setupSessionPage(page, { live: true });

  let patchCalled = false;
  await page.route(
    new RegExp(`/api/session/${encodeURIComponent(MOCK_SESSION.id)}$`),
    (route) => {
      if (route.request().method() === 'PATCH') {
        patchCalled = true;
        return route.fulfill({ status: 200, body: '{}' });
      }
      return route.fallback();
    },
  );

  await page.locator('body').click();
  await page.keyboard.press('Alt+Space');
  await page.fill('.oc-cmd-input', 'rename');
  await page.locator('.oc-cmd-item', { hasText: 'rename' }).click();
  await expect(page.locator('.oc-rename-dialog')).toBeVisible({ timeout: 3_000 });

  await page.locator('button', { hasText: 'Cancel' }).click();
  await expect(page.locator('.oc-rename-dialog')).toHaveCount(0, { timeout: 2_000 });
  expect(patchCalled).toBe(false);
});

test('rename modal: Escape closes without saving', async ({ mockedPage: page }) => {
  await setupSessionPage(page, { live: true });

  await page.locator('body').click();
  await page.keyboard.press('Alt+Space');
  await page.fill('.oc-cmd-input', 'rename');
  await page.locator('.oc-cmd-item', { hasText: 'rename' }).click();
  await expect(page.locator('.oc-rename-dialog')).toBeVisible({ timeout: 3_000 });

  await page.locator('.oc-rename-input').press('Escape');
  await expect(page.locator('.oc-rename-dialog')).toHaveCount(0, { timeout: 2_000 });
});

test('rename succeeds and shows "Session renamed" toast', async ({ mockedPage: page }) => {
  await setupSessionPage(page, { live: true });

  await page.route(
    new RegExp(`/api/session/${encodeURIComponent(MOCK_SESSION.id)}$`),
    (route) =>
      route.request().method() === 'PATCH'
        ? route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ ok: true }) })
        : route.fallback(),
  );

  await page.locator('body').click();
  await page.keyboard.press('Alt+Space');
  await page.fill('.oc-cmd-input', 'rename');
  await page.locator('.oc-cmd-item', { hasText: 'rename' }).click();
  await expect(page.locator('.oc-rename-dialog')).toBeVisible({ timeout: 3_000 });
  await page.locator('.oc-rename-input').fill('Renamed title');
  await page.locator('button', { hasText: 'Rename' }).click();

  // Toast should appear
  await expect(page.locator('.oc-toast-root', { hasText: 'Session renamed' })).toBeVisible({ timeout: 3_000 });
});

// ===========================================================================
// ARCHIVE — session table (dashboard)
// ===========================================================================

test('archive button in session table calls POST /api/session/archive', async ({ mockedPage: page }) => {
  await page.route('/api/session/archive', (route) =>
    route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ ok: true }) }),
  );

  await page.goto('/');
  await expect(page.locator('.session-title', { hasText: MOCK_SESSION.title })).toBeVisible({ timeout: 5_000 });

  const [req] = await Promise.all([
    page.waitForRequest((r) => r.url().includes('/api/session/archive') && r.method() === 'POST'),
    // Click the first archive button
    page.locator('button[aria-label="Archive session"]').first().click(),
  ]);

  const body = JSON.parse(req.postData() ?? '{}');
  expect(body).toMatchObject({ archived: true });
});

test('archived session disappears from the dashboard after archiving', async ({ mockedPage: page }) => {
  await page.route('/api/session/archive', (route) =>
    route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ ok: true }) }),
  );

  await page.goto('/');
  await expect(page.locator('.session-title', { hasText: MOCK_SESSION.title })).toBeVisible({ timeout: 5_000 });

  // Archived sessions are hidden by default on the dashboard ("Include
  // archived" is off), so archiving a row optimistically removes it.
  await page.locator('button[aria-label="Archive session"]').first().click();

  // The row should disappear from the table (hidden by locallyArchivedSessionIds filter)
  await expect(page.locator('.session-title', { hasText: MOCK_SESSION.title })).toHaveCount(0, { timeout: 3_000 });
});

// ===========================================================================
// ARCHIVE — sidebar in session detail
// ===========================================================================

test('archive button in session detail sidebar navigates away from archived session', async ({ mockedPage: page }) => {
  await page.route('/api/session/archive', (route) =>
    route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ ok: true }) }),
  );
  // Only return the current session in the sidebar so there's no "next" session to navigate to
  await page.route('/api/sessions*', (route) =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify([MOCK_SESSION]),
    }),
  );
  await setupSessionPage(page);

  // Wait for the sidebar to show sessions
  await expect(page.locator('.session-sidebar-archive-btn').first()).toBeVisible({ timeout: 5_000 });
  await page.locator('.session-sidebar-archive-btn').first().click();

  // After archiving the only session with no adjacent session, the app
  // navigates to the /session/new sentinel (keeps the sidebar open with an
  // empty-state hint) instead of bouncing back to the dashboard.
  await expect(page).toHaveURL('/session/new', { timeout: 5_000 });
});

// ===========================================================================
// PROJECT DETAIL
// ===========================================================================

test('project detail page renders directory path', async ({ mockedPage: page }) => {
  await page.goto(`/project/${encodeURIComponent(MOCK_PROJECT.directory)}`);
  // The h2 should show the full directory
  await expect(page.locator('h2.section-title')).toContainText(MOCK_PROJECT.directory, { timeout: 5_000 });
});

test('project detail page shows VS Code button', async ({ mockedPage: page }) => {
  await page.goto(`/project/${encodeURIComponent(MOCK_PROJECT.directory)}`);
  await expect(page.locator('button.vscode-btn', { hasText: 'VS Code' })).toBeVisible({ timeout: 5_000 });
});

test('project detail shows sessions from the mocked sessions API', async ({ mockedPage: page }) => {
  // Return sessions that belong to this project
  await page.route('/api/sessions*', (route) =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify([MOCK_SESSION, MOCK_SESSION_2]),
    }),
  );

  await page.goto(`/project/${encodeURIComponent(MOCK_PROJECT.directory)}`);
  await expect(page.locator('.session-title', { hasText: MOCK_SESSION.title })).toBeVisible({ timeout: 5_000 });
});

test('project detail shows "No sessions found" when API returns empty', async ({ mockedPage: page }) => {
  await page.route('/api/sessions*', (route) =>
    route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify([]) }),
  );

  await page.goto(`/project/${encodeURIComponent(MOCK_PROJECT.directory)}`);
  await expect(page.locator('td').filter({ hasText: /No sessions found/ })).toBeVisible({ timeout: 5_000 });
});

test('clicking a session row in project detail navigates to session', async ({ mockedPage: page }) => {
  await page.route('/api/sessions*', (route) =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify([MOCK_SESSION]),
    }),
  );

  await page.goto(`/project/${encodeURIComponent(MOCK_PROJECT.directory)}`);
  await page.locator('.session-title', { hasText: MOCK_SESSION.title }).click();
  await expect(page).toHaveURL(`/session/${MOCK_SESSION.id}`);
});

test('project detail shows time-range filter buttons and "Exclude archived" toggle', async ({ mockedPage: page }) => {
  await page.goto(`/project/${encodeURIComponent(MOCK_PROJECT.directory)}`);

  const filterBar = page.locator('.oc-time-range');
  await expect(filterBar).toBeVisible({ timeout: 5_000 });
  // 5 time-range buttons (12h / 24h / 7d / 30d / All) plus the
  // "Exclude archived" toggle.
  await expect(filterBar.locator('button')).toHaveCount(6);
  // 7d is the default for project detail.
  await expect(filterBar.locator('button.active', { hasText: '7d' })).toBeVisible();
  // Archived is included by default ⇒ the toggle is NOT active.
  await expect(filterBar.locator('button', { hasText: 'Exclude archived' })).not.toHaveClass(/active/);
});

test('project detail "Exclude archived" toggle hides locally-archived sessions', async ({ mockedPage: page }) => {
  // Mark MOCK_SESSION_2 as archived in the response.
  const archivedSession = { ...MOCK_SESSION_2, archived: true };
  await page.route('/api/sessions*', (route) =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify([MOCK_SESSION, archivedSession]),
    }),
  );

  await page.goto(`/project/${encodeURIComponent(MOCK_PROJECT.directory)}`);
  // Both visible by default (archived included).
  await expect(page.locator('.session-title', { hasText: MOCK_SESSION.title })).toBeVisible({ timeout: 5_000 });
  await expect(page.locator('.session-title', { hasText: MOCK_SESSION_2.title })).toBeVisible();

  // Click the toggle — archived row disappears.
  await page.locator('.oc-time-range-btn', { hasText: 'Exclude archived' }).click();
  await expect(page.locator('.session-title', { hasText: MOCK_SESSION.title })).toBeVisible();
  await expect(page.locator('.session-title', { hasText: MOCK_SESSION_2.title })).not.toBeVisible();
});

// ===========================================================================
// KEYBOARD SHORTCUTS DIALOG
// ===========================================================================

test('keyboard shortcuts dialog opens with Alt+Shift+?', async ({ mockedPage: page }) => {
  await page.goto('/');
  await page.locator('body').click();
  await page.keyboard.press('Alt+Shift+Slash');
  await expect(page.locator('[role="dialog"][aria-label="Keyboard shortcuts"]')).toBeVisible({ timeout: 3_000 });
});

test('keyboard shortcuts dialog has "Keyboard shortcuts" heading', async ({ mockedPage: page }) => {
  await page.goto('/');
  await page.locator('body').click();
  await page.keyboard.press('Alt+Shift+Slash');
  await expect(page.locator('.oc-shortcuts-dialog h2')).toContainText('Keyboard shortcuts', { timeout: 3_000 });
});

test('keyboard shortcuts dialog shows site-wide shortcuts section', async ({ mockedPage: page }) => {
  await page.goto('/');
  await page.locator('body').click();
  await page.keyboard.press('Alt+Shift+Slash');
  await expect(page.locator('.oc-shortcuts-section-title', { hasText: 'Site-wide shortcuts' })).toBeVisible({ timeout: 3_000 });
});

test('close button closes the shortcuts dialog', async ({ mockedPage: page }) => {
  await page.goto('/');
  await page.locator('body').click();
  await page.keyboard.press('Alt+Shift+Slash');
  await expect(page.locator('.oc-shortcuts-dialog')).toBeVisible({ timeout: 3_000 });
  await page.locator('button[aria-label="Close keyboard shortcuts"]').click();
  await expect(page.locator('.oc-shortcuts-dialog')).toHaveCount(0);
});

test('Escape closes the shortcuts dialog', async ({ mockedPage: page }) => {
  await page.goto('/');
  await page.locator('body').click();
  await page.keyboard.press('Alt+Shift+Slash');
  await expect(page.locator('.oc-shortcuts-dialog')).toBeVisible({ timeout: 3_000 });
  await page.keyboard.press('Escape');
  await expect(page.locator('.oc-shortcuts-dialog')).toHaveCount(0);
});

test('clicking the backdrop closes the shortcuts dialog', async ({ mockedPage: page }) => {
  await page.goto('/');
  await page.locator('body').click();
  await page.keyboard.press('Alt+Shift+Slash');
  await expect(page.locator('.oc-shortcuts-dialog')).toBeVisible({ timeout: 3_000 });
  await page.locator('.oc-shortcuts-backdrop').click({ position: { x: 10, y: 10 } });
  await expect(page.locator('.oc-shortcuts-dialog')).toHaveCount(0);
});

/**
 * e2e: Session detail page
 *
 * Covers:
 *  - Page renders with session title in the header breadcrumb
 *  - Sidebar shows "Recent sessions" heading
 *  - Other sessions from the mock list appear in the sidebar
 *  - Loading spinner shown while session data loads
 *  - Error banner shown when session fetch fails + Retry button
 *  - Navigating directly to a session URL works (deep-link)
 *  - Back navigation via header logo returns to dashboard
 *  - "New session" (+) button is visible
 *  - "Open in VS Code" button is visible
 *  - Archive button is present on sidebar session items
 *  - Sidebar item for the active session has 'active' class
 */

import { test, expect, MOCK_SESSION, MOCK_SESSION_2 } from './fixtures';

const SESSION_URL = `/session/${MOCK_SESSION.id}`;

// ---------------------------------------------------------------------------
// Basic render
// ---------------------------------------------------------------------------

test('session detail page renders without crashing', async ({ mockedPage: page }) => {
  await page.goto(SESSION_URL);
  // The layout should render (sidebar + main area)
  await expect(page.locator('.session-layout')).toBeVisible();
});

test('shows loading spinner while session data is loading', async ({ mockedPage: page }) => {
  let resolveSession!: () => void;
  const sessionReadyP = new Promise<void>((resolve) => { resolveSession = resolve; });

  await page.route(`/api/session/${MOCK_SESSION.id}`, async (route) => {
    const url = route.request().url();
    if (url.includes('/agents') || url.includes('/commands') || url.includes('/models') ||
        url.includes('/permissions') || url.includes('/questions') || url.includes('/events')) {
      await route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify([]) });
      return;
    }
    await sessionReadyP;
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        session: MOCK_SESSION,
        messages: [],
        parts: [],
        totalMessages: 0,
        defaultAgent: '',
        defaultModel: '',
      }),
    });
  });

  const gotoPromise = page.goto(SESSION_URL);
  await expect(page.locator('.oc-loading, .oc-spinner')).toBeVisible({ timeout: 3_000 });
  resolveSession();
  await gotoPromise;
});

test('shows error banner when session fetch fails', async ({ mockedPage: page }) => {
  // Override the session detail fetch to return 500. We use a regex to match
  // the base session URL with any query string but exclude sub-paths like /agents.
  await page.route(
    new RegExp(`/api/session/${MOCK_SESSION.id}(\\?|$)`),
    (route) => route.fulfill({ status: 500, body: 'Internal Server Error' }),
  );

  await page.goto(SESSION_URL);
  await expect(page.locator('.oc-error-banner')).toBeVisible({ timeout: 5_000 });
  await expect(page.locator('button', { hasText: 'Retry' })).toBeVisible();
});

// ---------------------------------------------------------------------------
// Header breadcrumb
// ---------------------------------------------------------------------------

test('session title appears in header breadcrumb', async ({ mockedPage: page }) => {
  await page.goto(SESSION_URL);
  // The header should eventually contain the session title
  await expect(page.locator('header')).toContainText(MOCK_SESSION.title, { timeout: 5_000 });
});

test('header logo links back to dashboard', async ({ mockedPage: page }) => {
  await page.goto(SESSION_URL);
  await page.locator('h1 a', { hasText: 'ocman' }).click();
  await expect(page).toHaveURL('/');
});

// ---------------------------------------------------------------------------
// Sidebar
// ---------------------------------------------------------------------------

test('sidebar shows "Recent sessions" heading', async ({ mockedPage: page }) => {
  await page.goto(SESSION_URL);
  await expect(page.locator('.session-sidebar-heading')).toContainText('Recent sessions');
});

test('sidebar shows both mock sessions', async ({ mockedPage: page }) => {
  await page.goto(SESSION_URL);
  // Both sessions should appear in the sidebar list
  await expect(
    page.locator('.session-sidebar-title', { hasText: 'Fix the login bug' }),
  ).toBeVisible({ timeout: 5_000 });
  await expect(
    page.locator('.session-sidebar-title', { hasText: 'Refactor auth module' }),
  ).toBeVisible({ timeout: 5_000 });
});

test('active session sidebar item has active class', async ({ mockedPage: page }) => {
  await page.goto(SESSION_URL);
  const activeItem = page.locator('.session-sidebar-item.active');
  await expect(activeItem).toBeVisible({ timeout: 5_000 });
  await expect(activeItem).toContainText('Fix the login bug');
});

test('clicking a sidebar session navigates to that session', async ({ mockedPage: page }) => {
  await page.goto(SESSION_URL);
  // Click the other session in the sidebar
  await page.locator('.session-sidebar-title', { hasText: 'Refactor auth module' }).click();
  await expect(page).toHaveURL(`/session/${MOCK_SESSION_2.id}`);
});

test('sidebar archive button is visible on session items', async ({ mockedPage: page }) => {
  await page.goto(SESSION_URL);
  const archiveBtns = page.locator('.session-sidebar-archive-btn');
  // At least one archive button exists
  await expect(archiveBtns.first()).toBeVisible({ timeout: 5_000 });
});

// ---------------------------------------------------------------------------
// Session action buttons
// ---------------------------------------------------------------------------

test('"New session" button is visible in session detail', async ({ mockedPage: page }) => {
  await page.goto(SESSION_URL);
  // The + button for creating a new session
  await expect(page.locator('.session-detail-actions button', { hasText: '+' })).toBeVisible({
    timeout: 5_000,
  });
});

test('"Open in VS Code" button is visible', async ({ mockedPage: page }) => {
  await page.goto(SESSION_URL);
  // The </> button
  await expect(page.locator('.session-detail-actions button', { hasText: '</>' })).toBeVisible({
    timeout: 5_000,
  });
});

// ---------------------------------------------------------------------------
// Deep-link navigation
// ---------------------------------------------------------------------------

test('navigating directly to a session URL loads the correct session', async ({
  mockedPage: page,
}) => {
  await page.goto(`/session/${MOCK_SESSION_2.id}`);
  await expect(page.locator('.session-layout')).toBeVisible();
  // The header should contain the second session's title
  await expect(page.locator('header')).toContainText(MOCK_SESSION_2.title, { timeout: 5_000 });
});

test('unknown session ID shows an error', async ({ mockedPage: page }) => {
  // Override the session detail fetch for the unknown ID to return 404.
  // Use a regex to match the base URL with optional query string.
  await page.route(
    new RegExp('/api/session/nonexistent-id(\\?|$)'),
    (route) => route.fulfill({ status: 404, body: 'Not Found' }),
  );

  await page.goto('/session/nonexistent-id');
  await expect(page.locator('.oc-error-banner')).toBeVisible({ timeout: 5_000 });
});

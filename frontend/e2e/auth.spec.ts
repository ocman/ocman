/**
 * e2e: Authentication flow
 *
 * Covers:
 *  - Auth-gate bypass when the server doesn't require a password
 *  - Login page rendered when auth is required and user is not authenticated
 *  - Successful login submits the password and transitions to the dashboard
 *  - Wrong password shows an error message
 *  - Sign-out button is visible in the Settings tab when auth is required and logs the user out
 */

import { test, expect } from './fixtures';

// ---------------------------------------------------------------------------
// No-auth: app loads straight to the dashboard
// ---------------------------------------------------------------------------

test('bypasses auth gate when authRequired=false', async ({ mockedPage: page }) => {
  await page.goto('/sessions');
  await expect(page.getByRole('button', { name: 'Collapse navigation' })).toBeVisible();
  await expect(page.getByRole('form', { name: 'Sign in' })).toHaveCount(0);
  await expect(page.getByRole('link', { name: 'Sessions' })).toBeVisible();
});

// ---------------------------------------------------------------------------
// Auth required: login gate
// ---------------------------------------------------------------------------

test.describe('auth required', () => {
  test.beforeEach(async ({ mockedPage: page }) => {
    // Override /api/auth/me to require auth
    await page.route('/api/auth/me', (route) =>
      route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({ authenticated: false, authRequired: true }),
      }),
    );
  });

  test('shows login page when unauthenticated', async ({ mockedPage: page }) => {
    await page.goto('/');
    await expect(page.getByRole('form', { name: 'Sign in' })).toBeVisible();
    await expect(page.getByRole('heading', { name: 'ocman' })).toBeVisible();
    await expect(page.getByLabel('Password', { exact: true })).toBeVisible();
    await expect(page.getByRole('button', { name: 'Sign in', exact: true })).toBeVisible();
    await expect(page.getByRole('link', { name: 'Sessions' })).toHaveCount(0);
  });

  test('submit button is disabled when password field is empty', async ({ mockedPage: page }) => {
    await page.goto('/');
    await expect(page.getByRole('button', { name: 'Sign in', exact: true })).toBeDisabled();
  });

  test('submit button enables when password is typed', async ({ mockedPage: page }) => {
    await page.goto('/');
    await page.getByLabel('Password', { exact: true }).fill('hunter2');
    await expect(page.getByRole('button', { name: 'Sign in', exact: true })).toBeEnabled();
  });

  test('successful login navigates to the dashboard', async ({ mockedPage: page }) => {
    // Intercept the login POST to return success
    await page.route('/api/auth/login', (route) =>
      route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({ ok: true }),
      }),
    );
    // After login, /api/auth/me should report authenticated
    let loginDone = false;
    await page.route('/api/auth/me', (route) =>
      route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify(
          loginDone
            ? { authenticated: true, authRequired: true }
            : { authenticated: false, authRequired: true },
        ),
      }),
    );

    await page.goto('/sessions');
    await page.getByLabel('Password', { exact: true }).fill('correctpassword');
    loginDone = true;
    await page.getByRole('button', { name: 'Sign in', exact: true }).click();

    // The login form should disappear and the dashboard should appear
    await expect(page.getByRole('form', { name: 'Sign in' })).toHaveCount(0, { timeout: 5_000 });
    await expect(page.getByRole('link', { name: 'Sessions' })).toBeVisible();
  });

  test('wrong password shows error banner', async ({ mockedPage: page }) => {
    await page.route('/api/auth/login', (route) =>
      route.fulfill({
        status: 401,
        contentType: 'application/json',
        body: JSON.stringify({ error: 'Invalid password' }),
      }),
    );

    await page.goto('/');
    await page.getByLabel('Password', { exact: true }).fill('wrongpassword');
    await page.getByRole('button', { name: 'Sign in', exact: true }).click();

    await expect(page.getByRole('alert')).toBeVisible({ timeout: 3_000 });
    // Still on the login page
    await expect(page.getByRole('form', { name: 'Sign in' })).toBeVisible();
  });

  test('sign-out button appears in settings tab when authRequired=true and authenticated', async ({
    mockedPage: page,
  }) => {
    // Simulate already authenticated
    await page.route('/api/auth/me', (route) =>
      route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({ authenticated: true, authRequired: true }),
      }),
    );

    // Sign out button lives in the Settings tab, not the global header.
    // It sits in the "Account" group, which the settings sidebar reveals
    // only after the matching nav item is selected.
    await page.goto('/settings');
    // authRequired loads async from /api/auth/me; wait for the Account
    // sidebar item to appear (and settle) before selecting it.
    const accountNav = page.getByRole('button', { name: 'Account' });
    await expect(accountNav).toBeVisible();
    await accountNav.click();
    await expect(page.getByRole('button', { name: 'Sign out' })).toBeVisible();
  });

  test('sign-out returns to login page', async ({ mockedPage: page }) => {
    await page.route('/api/auth/me', (route) =>
      route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({ authenticated: true, authRequired: true }),
      }),
    );
    await page.route('/api/auth/logout', (route) =>
      route.fulfill({ status: 200, contentType: 'application/json', body: '{}' }),
    );

    // Navigate to Settings where the Sign out button lives.
    await page.goto('/settings');
    await expect(page.getByRole('link', { name: 'Settings', current: 'page' })).toBeVisible();

    // Sign out lives in the "Account" settings group — select it first.
    // Wait for the async-loaded Account nav item to settle before clicking.
    const accountNav = page.getByRole('button', { name: 'Account' });
    await expect(accountNav).toBeVisible();
    await accountNav.click();
    await page.getByRole('button', { name: 'Sign out' }).click();
    await expect(page.getByRole('form', { name: 'Sign in' })).toBeVisible({ timeout: 5_000 });
  });
});

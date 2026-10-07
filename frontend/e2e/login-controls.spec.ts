import { test, expect } from './fixtures';

for (const width of [1280, 390]) {
  test(`login keeps credentials masked and prevents duplicate submissions at ${width}px`, async ({ mockedPage: page }) => {
    await page.setViewportSize({ width, height: width === 390 ? 844 : 600 });
    let finishProbe!: () => void;
    const probe = new Promise<void>(resolve => { finishProbe = resolve; });
    let authenticated = false;
    await page.route('/api/auth/me', async route => {
      await probe;
      await route.fulfill({ json: { authenticated, authRequired: true } });
    });
    let finishLogin!: () => void;
    const pending = new Promise<void>(resolve => { finishLogin = resolve; });
    const passwords: string[] = [];
    await page.route('/api/auth/login', async route => {
      passwords.push(route.request().postDataJSON().password);
      if (passwords.length === 1) {
        await pending;
        await route.fulfill({ status: 401, json: { error: 'Invalid password' } });
      } else {
        authenticated = true;
        await route.fulfill({ json: { ok: true } });
      }
    });
    await page.goto('/sessions');
    await expect(page.getByRole('status')).toContainText('Checking authentication');
    await expect(page.getByRole('link', { name: 'Sessions', exact: true })).toHaveCount(0);
    finishProbe();
    const field = page.getByLabel('Password', { exact: true });
    await expect(field).toBeFocused();
    await expect(field).toHaveAttribute('type', 'password');
    await expect(field).toHaveAttribute('autocomplete', 'current-password');
    await expect(page.getByRole('button', { name: 'Sign in', exact: true })).toBeDisabled();
    await field.fill(' sample password ');
    await field.press('Enter');
    const submit = page.getByRole('button', { name: 'Signing in…', exact: true });
    await expect(submit).toBeDisabled();
    await expect(submit).toHaveAttribute('aria-busy', 'true');
    await expect(field).toBeDisabled();
    await page.keyboard.press('Enter');
    expect(passwords).toEqual([' sample password ']);
    finishLogin();
    await expect(page.getByRole('alert')).toContainText('Incorrect password');
    await expect(field).toHaveValue(' sample password ');
    await expect(field).toHaveAttribute('aria-invalid', 'true');
    await field.fill('correct password');
    await field.press('Enter');
    await expect(page.getByRole('form', { name: 'Sign in' })).toBeHidden();
    await expect(page.getByRole('button', { name: 'Include archived', exact: true })).toBeVisible();
    expect(passwords).toEqual([' sample password ', 'correct password']);
  });
}

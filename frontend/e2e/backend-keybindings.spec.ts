import { test, expect } from './fixtures';
import defaultKeybindings from '../../internal/keybindings/defaults.json' with { type: 'json' };

test('backend bindings replace defaults in dispatch and shortcut help', async ({ mockedPage: page }) => {
  let requests = 0;
  await page.route('/api/keybindings', (route) => {
    requests++;
    return route.fulfill({ json: {
      ...defaultKeybindings,
      'site.open-inbox': [{ code: 'KeyZ', alt: true }],
      'site.scroll-down': [{ code: 'KeyU', alt: true }],
    } });
  });
  await page.goto('/');
  await expect(page.getByRole('button', { name: /for shortcuts/ })).toBeVisible();
  await page.keyboard.press('Alt+Shift+Slash');
  const dialog = page.getByRole('dialog', { name: 'Keyboard shortcuts' });
  await expect(dialog).toBeVisible();
  await expect(dialog.getByText('Open inbox').locator('..').getByText('Z', { exact: true })).toBeVisible();
  await page.keyboard.press('Escape');
  // Direction belongs to the action, not to the physical arrow key.
  await page.evaluate(() => {
    const calls: number[] = [];
    Element.prototype.scrollBy = function (options: ScrollToOptions | number, _y?: number) {
      calls.push(typeof options === 'number' ? (_y ?? 0) : (options.top ?? 0));
    };
    Object.assign(window, { scrollCalls: calls });
  });
  await page.keyboard.press('Alt+u');
  expect(await page.evaluate(() => (window as unknown as { scrollCalls: number[] }).scrollCalls[0])).toBeGreaterThan(0);
  await page.keyboard.press('Alt+i');
  await expect(page).not.toHaveURL(/\/inbox/);
  await page.keyboard.press('Alt+z');
  await expect(page).toHaveURL(/\/inbox/);
  expect(requests).toBe(1);
});

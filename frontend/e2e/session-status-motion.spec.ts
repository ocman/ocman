import { readFileSync } from 'node:fs';
import { test, expect } from './fixtures';

const indicatorCSS = readFileSync(new URL('../src/components/SessionStatusIndicator.css', import.meta.url), 'utf8');

for (const reducedMotion of ['no-preference', 'reduce'] as const) {
  for (const state of ['busy', 'background', 'draft-busy']) {
    test(`${state} keeps rotating with reduced motion ${reducedMotion}`, async ({ page }) => {
      await page.emulateMedia({ reducedMotion });
      await page.setContent(`<style>${indicatorCSS}</style>
        <span class="session-status-indicator" data-state="${state}" role="status" aria-label="Working"></span>`);

      const indicator = page.getByRole('status', { name: 'Working' });
      await expect(indicator).toHaveCSS('animation-name', 'session-status-spin');
      await expect(indicator).toHaveCSS('animation-duration', state === 'background' ? '2.4s' : '0.8s');
      const initialTime = await indicator.evaluate(el => Number(el.getAnimations()[0].currentTime));
      await expect.poll(() => indicator.evaluate(el => Number(el.getAnimations()[0].currentTime))).toBeGreaterThan(initialTime);
    });
  }
}

test('reduced motion still disables the permission pulse', async ({ page }) => {
  await page.emulateMedia({ reducedMotion: 'reduce' });
  await page.setContent(`<style>${indicatorCSS}</style>
    <span class="session-status-indicator" data-state="permission" role="status" aria-label="Permission required"></span>`);

  await expect(page.getByRole('status', { name: 'Permission required' })).toHaveCSS('animation-name', 'none');
});

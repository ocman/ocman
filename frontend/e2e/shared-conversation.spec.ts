import { createCipheriv } from 'node:crypto';
import { test, expect } from '@playwright/test';

for (const width of [390, 768, 1440]) {
  test(`relay header keeps actions above the conversation at ${width}px`, async ({ page }) => {
    await page.setViewportSize({ width, height: 900 });
    const id = 'header-layout';
    const key = Buffer.alloc(32, 1);
    const cipher = createCipheriv('aes-256-gcm', key, Buffer.alloc(12));
    cipher.setAAD(Buffer.concat([Buffer.from(id), Buffer.alloc(8)]));
    const payload = JSON.stringify({
      session: { id: 's1', title: 'Remove deprecated GitHub action and clean up the remaining references' },
      messages: [], parts: [], readOnly: true,
    });
    const data = Buffer.concat([cipher.update(payload), cipher.final(), cipher.getAuthTag()]).toString('base64');
    await page.route(`**/s/${id}?*`, route => route.fulfill({ json: { chunks: [{ seq: 0, data }], last: 0 } }));
    await page.goto(`/v/${id}#k=${key.toString('base64url')}`);
    const header = page.getByRole('banner');
    const print = header.getByRole('button', { name: 'Print / Save as PDF' });
    const fork = header.getByRole('link', { name: 'Fork in local ocman' });
    await expect(print).toBeVisible();
    const headerBox = (await header.boundingBox())!;
    const mainBox = (await page.getByRole('main').boundingBox())!;
    for (const control of [print, fork, header.getByLabel('Collapse tool outputs')]) {
      const box = (await control.boundingBox())!;
      expect(box.y + box.height).toBeLessThanOrEqual(headerBox.y + headerBox.height);
      expect(box.y + box.height).toBeLessThanOrEqual(mainBox.y);
      expect(box.x + box.width).toBeLessThanOrEqual(width);
      await control.click({ trial: true });
    }
    await expect(print).toHaveClass(/oc-button/);
    await expect(fork).toHaveClass(/oc-button/);
    await expect(print).toHaveCSS('border-radius', '0px');
    await expect(fork).toHaveCSS('border-radius', '0px');
    await expect(header.getByRole('heading')).toHaveCSS('white-space', 'normal');
  });
}

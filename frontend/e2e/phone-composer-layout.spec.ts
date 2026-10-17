import { test, expect, MOCK_SESSION } from './fixtures';
import { installPhoneReviewRoutes } from './phone-review-fixtures';

for (const width of [320, 390, 640]) {
  test(`phone composer uses the available width without an extra background at ${width}px`, async ({ mockedPage: page }, testInfo) => {
    await page.setViewportSize({ width, height: 844 });
    await installPhoneReviewRoutes(page);
    await page.goto(`/session/${MOCK_SESSION.id}`);
    const input = page.getByTestId('conversation-composer').getByRole('textbox');
    await expect(input).toBeVisible();
    const layout = await input.evaluate(el => {
      const frame = el.closest('.oc-composer')!;
      const wrap = frame.closest('.oc-composer-wrap')!;
      return { width: frame.getBoundingClientRect().width, x: frame.getBoundingClientRect().x,
        background: getComputedStyle(wrap).backgroundColor, image: getComputedStyle(wrap).backgroundImage };
    });
    expect(layout.background).toBe('rgba(0, 0, 0, 0)');
    expect(layout.image).toBe('none');
    expect(layout.x).toBe(8);
    expect(layout.width).toBeGreaterThanOrEqual(width - 16);
    await expect(page.getByRole('button', { name: /^(Send message|Stop generation)$/ })).toHaveCSS('border-radius', '50%');
    await page.screenshot({ path: testInfo.outputPath(`phone-composer-${width}.png`) });
  });
}

for (const width of [390, 1440]) {
  test(`composer uses a compact paperclip and omits dictation at ${width}px`, async ({ mockedPage: page }) => {
    await page.setViewportSize({ width, height: 844 });
    await page.addInitScript(() => { Object.assign(window, { SpeechRecognition: class {} }); });
    await installPhoneReviewRoutes(page);
    await page.goto(`/session/${MOCK_SESSION.id}`);
    const attach = page.getByRole('button', { name: 'Attach file', exact: true });
    await expect(attach).toBeVisible();
    await expect(attach.locator('.bi-paperclip')).toBeVisible();
    await expect(page.getByRole('button', { name: 'Record voice message' })).toHaveCount(0);
    await expect(attach).toHaveCSS('border-top-width', '0px');
    const send = page.getByRole('button', { name: /^(Send message|Stop generation)$/ });
    const box = (await send.boundingBox())!;
    expect(box.width).toBeLessThanOrEqual(44);
    expect(box.height).toBeLessThanOrEqual(44);
    const chooser = page.waitForEvent('filechooser');
    await attach.click();
    expect((await chooser).isMultiple()).toBe(true);
  });
}

import { test, expect, MOCK_SESSION } from './fixtures';

test('image previews have shareable URLs and keyboard navigation', async ({ mockedPage: page }, testInfo) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  const session = { ...MOCK_SESSION, status: 'idle' };
  const message = { id: 'answer', sessionId: session.id, timeCreated: 2000, data: { role: 'assistant', finish: 'stop' } };
  const parts = [{ id: 'images', messageId: message.id, sessionId: session.id, data: {
    type: 'text', text: '![First image](/api/file/first)\n\n![Second image](/api/file/second)',
  } }];
  await page.route(new RegExp(`/api/session/${session.id}(\\?|$)`), route => route.fulfill({
    json: { session, messages: [message], parts, totalMessages: 1 },
  }));
  await page.route(/\/api\/file\/(first|second)$/, route => route.fulfill({
    contentType: 'image/svg+xml',
    body: '<svg xmlns="http://www.w3.org/2000/svg" width="800" height="480"><rect width="800" height="480" fill="#385b87"/><circle cx="400" cy="240" r="160" fill="#f6ca76"/></svg>',
  }));
  await page.goto(`/session/${session.id}?platform=opencode#preview`);
  await page.getByRole('button', { name: 'Expand First image' }).click();
  await expect(page.getByRole('dialog', { name: 'First image' })).toBeVisible();
  await expect(page).toHaveURL(/platform=opencode&image=[^#]+#preview$/);
  const firstURL = page.url();
  await page.keyboard.press('ArrowRight');
  const dialog = page.getByRole('dialog', { name: 'Second image' });
  await expect(dialog).toBeVisible();
  await expect(dialog.getByRole('button', { name: 'Next image' })).toBeDisabled();
  expect(page.url()).not.toBe(firstURL);
  const secondURL = page.url();
  const screenshot = testInfo.outputPath('image-preview.png');
  await page.screenshot({ path: screenshot, animations: 'disabled' });
  await testInfo.attach('image-preview', { path: screenshot, contentType: 'image/png' });
  await page.reload();
  await expect(dialog).toBeVisible();
  expect(page.url()).toBe(secondURL);
  await page.keyboard.press('ArrowLeft');
  await expect(page.getByRole('dialog', { name: 'First image' })).toBeVisible();
  await expect(page).toHaveURL(firstURL);
  await page.keyboard.press('Escape');
  await expect(page.getByRole('dialog')).toHaveCount(0);
  await expect(page).toHaveURL(`/session/${session.id}?platform=opencode#preview`);
});

import { test, expect, MOCK_SESSION, mockSessionWithLiveConnection } from './fixtures';

test('composer model uses available width and truncates only when crowded', async ({ mockedPage: page }) => {
  const name = 'Qwen3.8 Flash Next NVFP4 256K (Spark)';
  const model = `spark/${name}`;
  await page.route(new RegExp(`/api/session/${MOCK_SESSION.id}(\\?|$)`), (route) => route.fulfill({
    json: {
      session: mockSessionWithLiveConnection(), messages: [], parts: [], totalMessages: 0,
      defaultAgent: 'build', defaultModel: model,
    },
  }));
  await page.route('**/api/session/*/models*', (route) => route.fulfill({ json: [model] }));
  await page.setViewportSize({ width: 1440, height: 900 });
  await page.goto(`/session/${MOCK_SESSION.id}`);
  const button = page.getByRole('button', { name, exact: true });
  await expect(button).toBeVisible();

  const dimensions = () => button.evaluate((element) => {
    const label = element.querySelector('.oc-model-label')!;
    const text = label.lastElementChild ?? label;
    const style = getComputedStyle(text);
    return {
      width: text.clientWidth, fullWidth: text.scrollWidth,
      whiteSpace: style.whiteSpace, overflow: style.textOverflow,
    };
  });
  const wide = await dimensions();
  expect(wide.width).toBe(wide.fullWidth);
  expect(wide.whiteSpace).toBe('nowrap');

  await page.setViewportSize({ width: 400, height: 900 });
  await expect.poll(async () => {
    const narrow = await dimensions();
    return narrow.fullWidth > narrow.width && narrow.whiteSpace === 'nowrap' && narrow.overflow === 'ellipsis';
  }).toBe(true);
  await expect(page.getByRole('button', { name: 'Send message', exact: true })).toBeInViewport();
});

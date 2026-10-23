import { test, expect, MOCK_SESSION, mockStream } from './fixtures';

for (const width of [1280, 390]) {
  test(`background task stop controls fit the popover at ${width}px`, async ({ mockedPage: page }) => {
    await page.setViewportSize({ width, height: 800 });
    await page.route(new RegExp(`/api/session/${MOCK_SESSION.id}(\\?|$)`), route => route.fulfill({ json: {
      session: { ...MOCK_SESSION, status: 'busy', liveConnection: true }, messages: [], parts: [],
    } }));
    await mockStream(page, MOCK_SESSION.id, [{
      type: 'background.changed', count: 1, tasks: [{ id: 'one', description: 'pnpm test '.repeat(12) }],
    }]);
    await page.goto(`/session/${MOCK_SESSION.id}`);
    const trigger = page.getByRole('button', { name: '1 background task running' });
    await expect(trigger).toHaveText('1');
    await expect(page.locator('.oc-composer-footer-right').getByRole('button', { name: '1 background task running' })).toBeVisible();
    await expect(page.getByTestId('composer-notices').getByRole('button', { name: '1 background task running' })).toHaveCount(0);
    await trigger.click();
    const panel = page.getByRole('dialog', { name: 'Background tasks' });
    const stop = panel.getByRole('button', { name: /^Stop / });
    await expect(stop).toBeVisible();
    const panelBox = (await panel.boundingBox())!;
    const stopBox = (await stop.boundingBox())!;
    expect(stopBox.x + stopBox.width).toBeLessThanOrEqual(panelBox.x + panelBox.width - 10);
    expect(await panel.evaluate(element => element.scrollWidth)).toBeLessThanOrEqual(await panel.evaluate(element => element.clientWidth));
  });
}

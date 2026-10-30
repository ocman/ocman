import { test, expect, MOCK_SESSION } from './fixtures';
import { sampleResult } from '../src/test/a2uiFixture';

for (const width of [1280, 390]) {
  test(`A2UI result renders under strict CSP and confirms responses at ${width}px`, async ({ mockedPage: page }, testInfo) => {
    await page.setViewportSize({ width, height: 900 });
    // Vite preview does not set the Go server's CSP. Enforce its script policy
    // here so runtime schema compilation cannot accidentally pass the test.
    await page.route(/\/session\/[^/?]+(?:\?.*)?$/, async route => {
      if (route.request().resourceType() !== 'document') return route.fallback();
      const response = await route.fetch();
      await route.fulfill({ response, headers: {
        ...response.headers(),
        'content-security-policy': "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data: blob:; connect-src 'self' ws:; font-src 'self' data:; worker-src 'self' blob:; object-src 'none'",
      } });
    });
    const sample = sampleResult();
    for (const message of sample.messages) {
      if ('updateComponents' in message) {
        const table = message.updateComponents.components.find(component => component.component === 'Table');
        if (table?.component === 'Table') table.columns[0] = 'Sample observation';
      }
    }
    const output = JSON.stringify({ ocmanA2ui: sample });
    await page.route(new RegExp(`/api/session/${MOCK_SESSION.id}(\\?|$)`), route => route.fulfill({ json: {
      session: { ...MOCK_SESSION, status: 'done', liveConnection: true },
      liveConnection: true, platform: 'opencode',
      messages: [{ id: 'a', sessionId: MOCK_SESSION.id, timeCreated: 2000, data: {
        role: 'assistant', finish: 'stop', time: { created: 2000, completed: 2001 },
      } }],
      parts: [{ id: 'p', messageId: 'a', sessionId: MOCK_SESSION.id, data: {
        type: 'tool', tool: 'ocman_render_ui', callID: 'render-result',
        state: { status: 'completed', input: { action: 'render', intent: 'Show the CI investigation' }, output, time: { start: 2000, end: 2001 } },
      } }],
      totalMessages: 1,
    } }));
    const sends: { url: string; message: string }[] = [];
    await page.route(new RegExp(`/api/session/${MOCK_SESSION.id}/message`), route => {
      sends.push({ url: route.request().url(), message: route.request().postDataJSON().message });
      return route.fulfill({ status: 204 });
    });
    await page.goto(`/session/${MOCK_SESSION.id}`);
    const result = page.getByRole('region', { name: 'Build investigation' });
    await expect(result.getByRole('heading', { name: 'Failed jobs' })).toBeVisible();
    await expect(result).toHaveCSS('border-radius', '0px');
    const header = result.getByRole('columnheader', { name: 'Sample observation' });
    const sortButton = header.getByRole('button', { name: 'Sample observation' });
    await expect(sortButton).toHaveCSS('justify-content', 'space-between');
    await expect(sortButton).toHaveCSS('text-align', 'left');
    await expect(sortButton).toHaveCSS('border-radius', '0px');
    expect(await sortButton.evaluate(button => button.scrollHeight <= button.clientHeight)).toBe(true);
    expect(await sortButton.evaluate(button => getComputedStyle(button).color))
      .toBe(await result.evaluate(element => getComputedStyle(element).color));
    await expect(sortButton).toHaveAttribute('title', 'Sort ascending');
    await sortButton.click();
    await expect(header).toHaveAttribute('aria-sort', 'ascending');
    await expect(sortButton).toHaveAttribute('title', 'Sort descending');
    await expect(result.getByRole('row').nth(1)).toContainText('Backend');
    await sortButton.click();
    await expect(header).toHaveAttribute('aria-sort', 'descending');
    await expect(result.getByRole('row').nth(1)).toContainText('Frontend');
    await expect(result.getByRole('textbox', { name: 'Investigate job' })).toBeEnabled();
    await result.getByRole('textbox', { name: 'Investigate job' }).fill('Frontend');
    await result.getByRole('checkbox', { name: 'Include logs' }).check();
    expect(sends).toHaveLength(0);
    await result.getByRole('button', { name: 'Continue investigation' }).click();
    const review = page.getByRole('dialog', { name: 'Review response to agent' });
    await expect(review).toContainText('Frontend');
    await expect(review.getByRole('button', { name: 'Cancel' })).toBeFocused();
    expect(sends).toHaveLength(0);
    await review.getByRole('button', { name: 'Send response' }).click();
    await expect(result.getByRole('status')).toHaveText('Response sent to the agent.');
    expect(sends).toHaveLength(1);
    expect(new URL(sends[0].url).searchParams.get('platform')).toBe('opencode');
    expect(JSON.parse(sends[0].message.split('\n').slice(1).join('\n')).action.context).toEqual({ job: 'Frontend', logs: true, fixed: 'literal', missing: null });
    await page.reload();
    await expect(result.getByRole('textbox', { name: 'Investigate job' })).toHaveValue('Backend');
    await result.screenshot({ path: testInfo.outputPath('a2ui-result.png'), animations: 'disabled' });
  });
}

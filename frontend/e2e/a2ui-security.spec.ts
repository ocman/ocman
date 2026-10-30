import { test, expect, MOCK_SESSION, type Page } from './fixtures';
import { sampleResult } from '../src/test/a2uiFixture';

const HOST = 'a2ui-egress.invalid';
const attack = `<img src="https://${HOST}/pixel"><svg onload="window.__a2uiSecurityProbe=1"></svg><iframe src="https://${HOST}/frame"></iframe><style>body{background:url(https://${HOST}/css)}</style><script>window.__a2uiSecurityProbe=1;fetch('https://${HOST}/fetch');new WebSocket('wss://${HOST}/socket')</script>![image](https://${HOST}/markdown) [link](https://${HOST}/navigate)`;

async function showResult(page: Page, result: unknown) {
  await page.route(new RegExp(`/api/session/${MOCK_SESSION.id}(\\?|$)`), route => route.fulfill({ json: {
    session: { ...MOCK_SESSION, status: 'done', liveConnection: true },
    liveConnection: true, platform: 'opencode',
    messages: [{ id: 'a', sessionId: MOCK_SESSION.id, timeCreated: 2000, data: {
      role: 'assistant', finish: 'stop', time: { created: 2000, completed: 2001 },
    } }],
    parts: [{ id: 'p', messageId: 'a', sessionId: MOCK_SESSION.id, data: {
      type: 'tool', tool: 'ocman_render_ui', callID: 'render-result',
      state: { status: 'completed', input: { action: 'render', intent: 'Show an untrusted result' },
        output: JSON.stringify({ ocmanA2ui: result }), time: { start: 2000, end: 2001 } },
    } }],
    totalMessages: 1,
  } }));
  await page.goto(`/session/${MOCK_SESSION.id}`);
}

test('A2UI strings cannot become executable content, remote resources or navigation', async ({ mockedPage: page }) => {
  // No extra CSP is added here: the renderer itself must refuse to create
  // executable or URL-bearing content, even without that additional defense.
  const outbound: string[] = [];
  const sent: string[] = [];
  page.on('request', request => { if (request.url().includes(HOST)) outbound.push(request.url()); });
  page.on('websocket', socket => { if (socket.url().includes(HOST)) outbound.push(socket.url()); });
  // A regression must never contact a real external service during the test.
  await page.route(`**://${HOST}/**`, route => route.fulfill({ status: 204 }));
  await page.route(new RegExp(`/api/session/${MOCK_SESSION.id}/message`), route => {
    sent.push(route.request().postData() ?? '');
    return route.fulfill({ status: 204 });
  });
  const ui = sampleResult();
  ui.summary = attack;
  for (const message of ui.messages) {
    if ('updateComponents' in message) {
      for (const c of message.updateComponents.components) {
        if (c.component === 'Text') c.text = attack;
        if (c.component === 'Table') c.rows = [[attack, attack]];
        if (c.component === 'BarChart') c.data = [{ label: attack.slice(0, 200), value: 1 }];
      }
    }
    if ('updateDataModel' in message) message.updateDataModel.value.job = attack;
  }
  await showResult(page, ui);
  const result = page.getByRole('region', { name: 'Build investigation' });
  await expect(result.getByRole('textbox', { name: 'Investigate job' })).toHaveValue(attack);
  expect(outbound).toEqual([]);
  await page.evaluate(async host => { await fetch(`https://${host}/control`, { mode: 'no-cors' }); }, HOST);
  expect(outbound.splice(0)).toEqual([`https://${HOST}/control`]);
  await expect(result.locator('img, svg, iframe, script, style, link, a, form, object, embed')).toHaveCount(0);
  const originalURL = page.url();
  await result.getByRole('button', { name: attack, exact: true }).click();
  const dialog = page.getByRole('dialog', { name: 'Review response to agent' });
  await expect(dialog).toContainText(JSON.stringify(attack));
  await expect(dialog.locator('img, svg, iframe, script, style, link, a, form, object, embed')).toHaveCount(0);
  await dialog.getByRole('button', { name: 'Cancel' }).click();
  expect(sent).toEqual([]);
  expect(outbound).toEqual([]);
  expect(page.url()).toBe(originalURL);
  expect(await page.evaluate(() => Object.hasOwn(window, '__a2uiSecurityProbe'))).toBe(false);
});

for (const component of [
  { id: 'root', component: 'Image', url: `https://${HOST}/pixel` },
  { id: 'root', component: 'Iframe', src: `https://${HOST}/frame` },
  { id: 'root', component: 'Text', text: 'Unsafe HTML', html: attack },
  { id: 'root', component: 'Text', text: 'Unsafe style', style: { background: `url(https://${HOST}/css)` } },
  { id: 'root', component: 'Text', text: { call: 'openUrl', args: { url: `https://${HOST}/function` } } },
]) {
  test(`A2UI rejects URL or code capabilities: ${JSON.stringify(component)}`, async ({ mockedPage: page }) => {
    const outbound: string[] = [];
    page.on('request', request => { if (request.url().includes(HOST)) outbound.push(request.url()); });
    await page.route(`**://${HOST}/**`, route => route.fulfill({ status: 204 }));
    const ui = sampleResult();
    const messages: unknown[] = [ui.messages[0], { version: 'v0.9.1', updateComponents: { surfaceId: 'build', components: [component] } }];
    await showResult(page, { ...ui, messages });
    await expect(page.getByRole('button', { name: 'ocman_render_ui Show an untrusted result' })).toBeVisible();
    await expect(page.getByRole('region', { name: 'Build investigation' })).toHaveCount(0);
    expect(outbound).toEqual([]);
  });
}

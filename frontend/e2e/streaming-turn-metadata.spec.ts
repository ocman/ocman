import { test, expect, MOCK_SESSION } from './fixtures';
import type { OceEvent } from '../src/lib/conversation/events';

for (const width of [1280, 390]) {
  test(`turn summary stays visible through text streaming at ${width}px`, async ({ mockedPage: page }) => {
    await page.setViewportSize({ width, height: 720 });
    const sessionId = MOCK_SESSION.id;
    const created = Date.now() - 1000;
    await page.route(new RegExp(`/api/session/${sessionId}(\\?|$)`), route => route.fulfill({ json: {
      session: { ...MOCK_SESSION, status: 'busy', liveConnection: true },
      messages: [
        { id: 'u1', sessionId, timeCreated: created, data: { role: 'user' } },
        { id: 'a1', sessionId, timeCreated: created + 100, data: {
          role: 'assistant', modelID: 'test-model', providerID: 'test', time: { created: created + 100 },
        } },
      ],
      parts: [
        { id: 'p1', sessionId, messageId: 'u1', data: { type: 'text', text: 'Explain the implementation.' } },
        { id: 'step-a1', sessionId, messageId: 'a1', data: { type: 'step-start' } },
        { id: 'p2', sessionId, messageId: 'a1', data: { type: 'text', text: 'Starting the response.' } },
      ],
      totalMessages: 2,
    } }));
    await page.addInitScript(({ sessionId }) => {
      const NativeEventSource = window.EventSource;
      let seq = 0;
      class StreamingSource extends EventTarget {
        readyState = 0;
        onopen: ((event: Event) => void) | null = null;
        onerror = null;
        onmessage = null;
        constructor(public url: string) {
          super();
          if (!url.includes(`/api/session/${sessionId}/stream`)) return new NativeEventSource(url) as unknown as StreamingSource;
          window.addEventListener('test-stream-event', event => {
            if (this.readyState !== 1) return;
            this.dispatchEvent(new MessageEvent('oce', { data: JSON.stringify({
              v: 1, epoch: 'metadata-test', seq: ++seq, sessionId, platform: 'opencode',
              at: Date.now(), durability: 'live', event: (event as CustomEvent).detail,
            }) }));
          });
          queueMicrotask(() => {
            this.readyState = 1;
            this.onopen?.(new Event('open'));
            window.dispatchEvent(new CustomEvent('test-stream-event', { detail: {
              type: 'stream.hello', protocol: 1, resumed: true, revision: 0,
            } }));
          });
        }
        close() { this.readyState = 2; }
      }
      window.EventSource = StreamingSource as unknown as typeof EventSource;
    }, { sessionId });
    await page.goto(`/session/${sessionId}`);
    const viewport = page.getByTestId('conversation-viewport');
    const composer = page.getByTestId('conversation-composer');
    await expect(viewport.getByText('Starting the response.', { exact: true })).toBeVisible();
    const summary = viewport.locator('.oc-turn-stats');
    await expect(summary).toHaveCount(1);
    await viewport.evaluate(el => { el.scrollTop = el.scrollHeight; });
    const emit = async (event: OceEvent) => {
      await page.evaluate(event => window.dispatchEvent(new CustomEvent('test-stream-event', { detail: event })), event);
    };
    let offset = 'Starting the response.'.length;
    for (let chunk = 0; chunk < 8; chunk++) {
      const text = `\n\n${'A detailed explanation of how the implementation works. '.repeat(8)} End of chunk ${chunk}.`;
      const event: OceEvent = { type: 'part.delta', messageId: 'a1', partId: 'p2', field: 'text', offset, text, partType: 'text' };
      await emit(event);
      offset += text.length;
      await expect(viewport.getByText(`End of chunk ${chunk}.`, { exact: false })).toBeAttached();
      await expect(summary).toHaveCount(1);
      await expect.poll(async () => {
        const row = await summary.boundingBox();
        const input = await composer.boundingBox();
        return row && input ? input.y - row.y - row.height : -1;
      }).toBeGreaterThanOrEqual(8);
    }
    for (const [id, content] of [
      ['a2', { type: 'tool', tool: 'bash', callID: 'call1', state: { status: 'completed', input: { command: 'pwd' }, output: '/project' } }],
      ['a3', { type: 'text', text: 'Continuing the response after the tool.' }],
      ['a4', { type: 'text', text: '' }],
    ] as const) {
      await emit({ type: 'message.upserted', message: { id, sessionId, timeCreated: Date.now(), data: { role: 'assistant' } } });
      await emit({ type: 'part.upserted', part: { id: `step-${id}`, messageId: id, sessionId, data: { type: 'step-start' } }, final: false });
      await emit({ type: 'part.upserted', part: { id: `p-${id}`, messageId: id, sessionId, data: content }, final: false });
      await expect(summary).toHaveCount(1);
      await expect(viewport.locator(`[data-message-id="${id}"] .oc-turn-stats`)).toHaveCount(1);
    }
  });
}

test('shows final text metadata when the original prompt is outside the loaded history', async ({ mockedPage: page }) => {
  const sessionId = MOCK_SESSION.id;
  await page.route(new RegExp(`/api/session/${sessionId}(\\?|$)`), route => route.fulfill({ json: {
    session: MOCK_SESSION,
    messages: [
      { id: 'notice', sessionId, timeCreated: 1000, data: { role: 'user', synthetic: true } },
      { id: 'answer', sessionId, timeCreated: 2000, data: { role: 'assistant', finish: 'stop',
        providerID: 'test', modelID: 'test-model', time: { created: 2000, completed: 3000 } } },
    ],
    parts: [
      { id: 'notice-text', sessionId, messageId: 'notice', data: { type: 'text',
        text: '<shell id="sh1" state="completed" command="make lint">\nPassed\n</shell>' } },
      { id: 'answer-text', sessionId, messageId: 'answer', data: { type: 'text', text: 'All checks passed.' } },
    ],
    totalMessages: 60,
  } }));
  await page.goto(`/session/${sessionId}`);
  const viewport = page.getByTestId('conversation-viewport');
  await expect(viewport.getByText('All checks passed.', { exact: true })).toBeVisible();
  await expect(viewport.getByRole('group', { name: 'Turn actions' })).toHaveCount(1);
});

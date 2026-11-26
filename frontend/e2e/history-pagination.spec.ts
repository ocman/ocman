import { test, expect, MOCK_SESSION, MOCK_SESSION_2 } from './fixtures';

test('loads the opening prompt without scrolling when opening a prefetched session', async ({ mockedPage: page }) => {
  await page.setViewportSize({ width: 1100, height: 1900 });
  await page.addInitScript(() => localStorage.setItem('ocman:ui', JSON.stringify({
    state: { compactToolCalls: true, changesSidebarOpenTabs: [] }, version: 7,
  })));
  const offsets: number[] = [];
  let finishOpen!: () => void;
  const openGate = new Promise<void>(resolve => { finishOpen = resolve; });
  const messages = Array.from({ length: 62 }, (_, position) => ({
    id: `tool-history-${position}`, sessionId: MOCK_SESSION.id,
    timeCreated: MOCK_SESSION.timeCreated + position * 1000,
    data: { role: position === 0 ? 'user' : 'assistant', finish: position ? 'tool-calls' : undefined },
  }));
  const historyUrl = new RegExp(`/api/session/${MOCK_SESSION.id}(\\?|$)`);
  await page.route(historyUrl, async (route) => {
    const { searchParams } = new URL(route.request().url());
    const offset = Number(searchParams.get('offset') || 0);
    offsets.push(offset);
    if (!searchParams.has('peek')) await openGate;
    const end = messages.length - offset;
    const batch = messages.slice(Math.max(0, end - 30), end);
    return route.fulfill({ json: {
      session: { ...MOCK_SESSION, status: 'busy', messageCount: messages.length },
      messages: batch,
      parts: batch.map((message) => ({
        id: `part-${message.id}`, messageId: message.id, sessionId: MOCK_SESSION.id,
        data: message.data.role === 'user'
          ? { type: 'text', text: 'Opening prompt before the tool activity' }
          : { type: 'tool', tool: 'bash', callID: message.id, state: { status: 'completed', input: { command: 'pwd' }, output: '/project' } },
      })),
      totalMessages: messages.length,
    } });
  });

  await page.goto(`/session/${MOCK_SESSION_2.id}`);
  const row = page.getByTestId('session-sidebar').getByRole('button', { name: /Fix the login bug/ });
  const [prefetch] = await Promise.all([page.waitForResponse(historyUrl), row.focus()]);
  await prefetch.finished();
  await row.click();
  const viewport = page.getByTestId('conversation-viewport');
  try {
    await expect(viewport.getByText(/30 tools/).first()).toBeAttached();
    expect(await viewport.evaluate(el => el.scrollHeight - el.clientHeight)).toBe(0);
    await viewport.evaluate(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))));
  } finally {
    finishOpen();
  }
  await expect(viewport.getByText('Opening prompt before the tool activity')).toBeVisible();
  expect(offsets.filter(offset => offset > 0)).toEqual([30, 60]);
});

for (const resize of [false, true]) {
  test(`fills the conversation viewport with older pages, resize=${resize}`, async ({ mockedPage: page }) => {
    await page.setViewportSize({ width: 1400, height: resize ? 700 : 8000 });
    const offsets: number[] = [];
    await page.route(new RegExp(`/api/session/${MOCK_SESSION.id}(\\?|$)`), (route) => {
      const offset = Number(new URL(route.request().url()).searchParams.get('offset') || 0);
      offsets.push(offset);
      const messages = Array.from({ length: Math.min(30, 61 - offset) }, (_, index) => {
        const position = Math.max(0, 61 - offset - 30) + index;
        return {
          id: `fill-${position}`, sessionId: MOCK_SESSION.id,
          timeCreated: MOCK_SESSION.timeCreated + position * 1000,
          data: { role: position === 0 ? 'user' : 'assistant', finish: 'stop' },
        };
      });
      return route.fulfill({ json: {
        session: { ...MOCK_SESSION, messageCount: 61 }, messages,
        parts: messages.map(message => ({
          id: `part-${message.id}`, messageId: message.id, sessionId: MOCK_SESSION.id,
          data: { type: 'text', text: message.id === 'fill-0' ? 'Original prompt in older history' : 'Short reply for viewport filling' },
        })),
        totalMessages: 61,
      } });
    });
    await page.goto(`/session/${MOCK_SESSION.id}`);
    const viewport = page.getByTestId('conversation-viewport');
    if (resize) {
      await expect.poll(() => viewport.evaluate(el => el.scrollTop)).toBeGreaterThan(0);
      expect(offsets.filter(offset => offset > 0)).toEqual([]);
      await page.setViewportSize({ width: 1400, height: 8000 });
    }
    await expect(viewport.getByText('Original prompt in older history')).toBeAttached();
    expect(offsets.filter(offset => offset > 0)).toEqual([30, 60]);
  });
}

for (const hidden of [true, false]) {
  test(`loads earlier pages while the oldest message is visible, hidden=${hidden}`, async ({ mockedPage: page }) => {
    const messages = Array.from({ length: 61 }, (_, index) => ({
      id: `history-${index}`,
      sessionId: MOCK_SESSION.id,
      timeCreated: MOCK_SESSION.timeCreated + index * 1000,
      data: {
        role: index === 0 ? 'user' : 'assistant',
        finish: index === 0 ? undefined : 'stop',
        time: { created: MOCK_SESSION.timeCreated + index * 1000, completed: MOCK_SESSION.timeCreated + index * 1000 + 100 },
      },
    }));
    const offsets: number[] = [];
    let releasePage!: () => void;
    const nextPage = new Promise<void>(resolve => { releasePage = resolve; });
    await page.route(new RegExp(`/api/session/${MOCK_SESSION.id}(\\?|$)`), async (route) => {
      const params = new URL(route.request().url()).searchParams;
      const offset = Number(params.get('offset') || 0);
      offsets.push(offset);
      if (!hidden && offset === 30) await nextPage;
      const batch = messages.slice(Math.max(0, messages.length - offset - 30), messages.length - offset);
      return route.fulfill({ json: {
        session: { ...MOCK_SESSION, messageCount: messages.length },
        messages: batch,
        parts: batch.flatMap((message) => hidden && message.data.role !== 'user' ? [] : [{
          id: `part-${message.id}`,
          messageId: message.id,
          sessionId: MOCK_SESSION.id,
          data: { type: 'text', text: message.data.role === 'user' ? 'The original user prompt' : 'A short reply' },
        }]),
        totalMessages: messages.length,
      } });
    });

    await page.goto(`/session/${MOCK_SESSION.id}`);
    const viewport = page.getByTestId('conversation-viewport');
    if (!hidden) {
      await expect(viewport.getByText('A short reply').first()).toBeAttached();
      // The tail starts scrollable, so it must not fetch pages until the
      // oldest message is revealed by a deliberate scroll to the top.
      await expect.poll(() => viewport.evaluate(el => el.scrollTop)).toBeGreaterThan(0);
      expect(offsets.filter(offset => offset > 0)).toEqual([]);
      await viewport.dispatchEvent('wheel', { deltaY: -100 });
      await viewport.evaluate(el => { el.scrollTop = 0; });
      const anchor = viewport.locator('[data-message-id="history-31"]');
      const before = await anchor.evaluate(el => el.getBoundingClientRect().top);
      releasePage();
      await expect(viewport.getByText('A short reply')).toHaveCount(60);
      await expect.poll(() => viewport.evaluate(el => el.scrollTop)).toBeGreaterThan(0);
      await expect.poll(async () => Math.abs(await anchor.evaluate(el => el.getBoundingClientRect().top) - before)).toBeLessThan(2);
      await viewport.evaluate(el => { el.scrollTop = 0; });
    }
    await expect(viewport.getByText('The original user prompt')).toBeVisible();
    expect(offsets.filter(offset => offset > 0)).toEqual([30, 60]);
  });
}

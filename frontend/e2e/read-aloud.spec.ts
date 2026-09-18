import { test, expect, MOCK_SESSION } from './fixtures';

for (const width of [1280, 390]) {
  test(`speaker and bookmark share the turn end at ${width}px`, async ({ mockedPage: page }) => {
    await page.setViewportSize({ width, height: 800 });
    await page.addInitScript(() => {
      const spoken: string[] = [];
      Object.defineProperty(window, 'speechSynthesis', { value: {
        getVoices: () => [],
        speak: (utterance: SpeechSynthesisUtterance) => spoken.push(utterance.text),
        cancel: () => {},
      } });
      Object.assign(window, { spoken });
    });
    const messages = [
      { id: 'user', sessionId: MOCK_SESSION.id, timeCreated: 1000, data: { role: 'user' } },
      { id: 'progress', sessionId: MOCK_SESSION.id, timeCreated: 1500, data: { role: 'assistant', finish: 'tool-calls' } },
      { id: 'answer', sessionId: MOCK_SESSION.id, timeCreated: 2000,
        data: { role: 'assistant', finish: 'stop', time: { created: 1500, completed: 2000 } } },
    ];
    const parts = [
      { id: 'p1', sessionId: MOCK_SESSION.id, messageId: 'user', data: { type: 'text', text: 'Check the login fix.' } },
      { id: 'p2', sessionId: MOCK_SESSION.id, messageId: 'progress', data: { type: 'text', text: 'Checking the tests.' } },
      { id: 'p3', sessionId: MOCK_SESSION.id, messageId: 'answer', data: { type: 'reasoning', text: 'Private reasoning.' } },
      { id: 'p4', sessionId: MOCK_SESSION.id, messageId: 'answer', data: { type: 'text', text: '## Fixed\n\nThe login tests pass.\n\n```sh\npnpm test\n```' } },
    ];
    await page.route(new RegExp(`/api/session/${MOCK_SESSION.id}(\\?|$)`), (route) => route.fulfill({
      json: { session: MOCK_SESSION, messages, parts, totalMessages: messages.length },
    }));
    await page.goto(`/session/${MOCK_SESSION.id}`);
    const actions = page.getByRole('group', { name: 'Turn actions' });
    await expect(actions).toHaveCount(1);
    const speaker = actions.getByRole('button', { name: 'Read aloud' });
    const bookmark = actions.getByRole('button', { name: 'Bookmark message' });
    await expect(speaker).toBeVisible();
    await expect(bookmark).toBeVisible();
    const speakerBox = (await speaker.boundingBox())!;
    const bookmarkBox = (await bookmark.boundingBox())!;
    expect(speakerBox.y).toBe(bookmarkBox.y);
    expect(bookmarkBox.x).toBeGreaterThan(speakerBox.x);
    await speaker.click();
    await expect(actions.getByRole('button', { name: 'Stop reading' })).toBeVisible();
    expect(await page.evaluate(() => (window as unknown as { spoken: string[] }).spoken))
      .toEqual(['Fixed The login tests pass.']);
    await actions.getByRole('button', { name: 'Stop reading' }).click();
    await expect(speaker).toBeVisible();
  });
}

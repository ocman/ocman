import { test, expect, MOCK_SESSION, mockSessionWithLiveConnection } from './fixtures';

for (const width of [1280, 390]) {
  test(`sets reasoning from the model picker at ${width}px`, async ({ mockedPage: page }, testInfo) => {
    await page.setViewportSize({ width, height: 720 });
    const url = `/api/session/${MOCK_SESSION.id}`;
    await page.route(new RegExp(`${url}(\\?|$)`), (route) => route.fulfill({ json: {
      session: mockSessionWithLiveConnection(), messages: [], parts: [], defaultModel: 'anthropic/sonnet',
    } }));
    await page.route(new RegExp(`${url}/models(\\?|$)`), (route) => route.fulfill({ json: {
      hasProviders: true,
      models: [
        { provider: 'anthropic', model: 'sonnet', modelName: 'Sonnet', isAvailable: true },
        { provider: 'anthropic', model: 'opus', modelName: 'Opus', isAvailable: true, reasoning: ['low', 'medium', 'high', 'max'] },
      ],
    } }));

    await page.goto(`/session/${MOCK_SESSION.id}`);
    const composer = page.getByTestId('conversation-composer');
    await composer.getByRole('button', { name: 'Sonnet' }).click();
    const picker = page.getByRole('dialog', { name: /Select a model/ });
    const levels = picker.getByRole('group', { name: 'Reasoning level' });
    await expect(levels).toBeHidden();
    await picker.getByRole('option', { name: 'Opus anthropic' }).click();
    await expect(levels.getByRole('button', { name: 'Default', pressed: true })).toBeFocused();
    const listBox = (await picker.getByRole('listbox').boundingBox())!;
    const levelsBox = (await levels.boundingBox())!;
    if (width > 560) expect(levelsBox.x).toBeGreaterThanOrEqual(listBox.x + listBox.width - 1);
    else expect(levelsBox.y).toBeGreaterThanOrEqual(listBox.y + listBox.height - 1);
    await picker.screenshot({ path: testInfo.outputPath('model-picker-reasoning.png'), animations: 'disabled' });
    await levels.getByRole('button', { name: 'high' }).click();
    await expect(picker).toBeHidden();
    await expect(composer.getByRole('button', { name: 'Opus' })).toBeVisible();
    await composer.getByRole('button', { name: 'high', exact: true }).click();
    await expect(levels.getByRole('button', { name: 'high', pressed: true })).toBeVisible();
    await expect(picker.getByRole('combobox')).toBeFocused();
    await page.keyboard.press('Tab');
    await expect(levels.getByRole('button', { name: 'max', exact: true })).toBeFocused();
    const favorites = picker.getByRole('button', { name: 'Add to favorites' });
    await expect(favorites).toHaveCount(2);
    for (const favorite of await favorites.all()) {
      await page.keyboard.press('Tab');
      await expect(favorite).toBeFocused();
    }
    await page.keyboard.press('Tab');
    await expect(levels.getByRole('button', { name: 'Default' })).toBeFocused();
    await page.keyboard.press('Shift+Tab');
    await expect(favorites.last()).toBeFocused();
    await expect(levels.getByRole('button', { name: 'high', pressed: true })).toBeVisible();
    await page.keyboard.press('Tab');
    await page.keyboard.press('ArrowLeft');
    await expect(picker.getByRole('combobox')).toBeFocused();
    await levels.getByRole('button', { name: 'Default' }).click();
    await expect(picker).toBeHidden();
    await composer.getByRole('button', { name: 'Opus' }).click();
    await expect(levels.getByRole('button', { name: 'Default', pressed: true })).toBeVisible();
  });
}

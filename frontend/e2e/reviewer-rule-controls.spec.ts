import { test, expect } from './fixtures';

type Section = { title: string; content: string; enabled?: boolean };
for (const width of [1280, 390]) {
  test(`reviewer rules edit, grow, toggle, add and remove at ${width}px`, async ({ mockedPage: page }) => {
    await page.setViewportSize({ width, height: 844 });
    let sections: Section[] = [
      { title: 'Project safety', content: 'Review commands before approving changes outside this project.' },
      { title: 'Network access', content: 'Ask for review before sending project data to an external service.', enabled: false },
    ];
    const writes: Section[][] = [];
    await page.route('/api/settings/prompt-sections', route => {
      if (route.request().method() === 'POST') {
        sections = route.request().postDataJSON();
        writes.push(sections);
        return route.fulfill({ status: 204 });
      }
      return route.fulfill({ json: sections });
    });
    await page.route('/api/settings/judge-model', route => route.fulfill({ json: { model: '' } }));
    await page.route('/api/settings/judge-model/options', route => route.fulfill({ json: { models: [], default: 'Default reviewer' } }));
    await page.goto('/settings');
    await page.getByRole('button', { name: 'Auto-approve', exact: true }).click();
    const title = page.getByRole('textbox', { name: 'Section 1 title' });
    await expect(title).toHaveValue('Project safety');
    if (width === 390) {
      const titleBounds = await title.boundingBox();
      const instructionBounds = await page.getByRole('textbox', { name: 'Section 1 instructions' }).boundingBox();
      expect(titleBounds!.width).toBeGreaterThanOrEqual(instructionBounds!.width - 2);
    }
    await expect(page.getByRole('checkbox', { name: 'Enable section 1' })).toBeChecked();
    await expect(page.getByRole('checkbox', { name: 'Enable section 2' })).not.toBeChecked();
    await title.fill('Repository safety');
    await expect.poll(() => writes.at(-1)?.[0].title).toBe('Repository safety');
    expect(writes.at(-1)![0].content).toContain('Review commands');
    await page.getByRole('checkbox', { name: 'Enable section 1' }).uncheck();
    await expect.poll(() => writes.at(-1)?.[0].enabled).toBe(false);
    const instructions = page.getByRole('textbox', { name: 'Section 1 instructions' });
    const before = await instructions.boundingBox();
    await instructions.fill('Review this command carefully.\n'.repeat(12));
    const expanded = await instructions.boundingBox();
    expect(expanded!.height).toBeGreaterThan(before!.height);
    expect(await instructions.evaluate(el => el.scrollHeight)).toBeLessThanOrEqual(await instructions.evaluate(el => el.clientHeight));
    await instructions.fill('Short rule.');
    const shrunk = await instructions.boundingBox();
    expect(shrunk!.height).toBeLessThan(expanded!.height);
    await page.getByRole('button', { name: '+ Add section', exact: true }).click();
    await expect(page.getByRole('textbox', { name: 'Section 3 title' })).toHaveValue('');
    await expect.poll(() => writes.at(-1)?.length).toBe(3);
    await page.getByRole('button', { name: 'Remove section 2', exact: true }).click();
    await expect.poll(() => writes.at(-1)?.length).toBe(2);
    expect(writes.at(-1)!.some(section => section.title === 'Network access')).toBe(false);
    expect(writes.at(-1)![0]).toEqual({ title: 'Repository safety', content: 'Short rule.', enabled: false });
  });
}

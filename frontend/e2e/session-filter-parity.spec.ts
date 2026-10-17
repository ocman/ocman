import { test, expect, MOCK_SESSION } from './fixtures';

const rows = [
  MOCK_SESSION,
  { ...MOCK_SESSION, id: 'routine', title: 'Routine work', routineId: 'routine-1' },
  { ...MOCK_SESSION, id: 'routine-child', title: 'Routine child', parentId: 'routine', status: 'busy' },
  { ...MOCK_SESSION, id: 'factory', title: 'Factory work', factoryAttemptId: 'attempt-1' },
  { ...MOCK_SESSION, id: 'child', title: 'Normal child', parentId: MOCK_SESSION.id, status: 'busy' },
  { ...MOCK_SESSION, id: 'archived', title: 'Archived work', archived: true },
  { ...MOCK_SESSION, id: 'snoozed', title: 'Snoozed work', archived: true, snoozedUntil: Date.now() + 86400_000 },
  { ...MOCK_SESSION, id: 'prompt', title: 'Pending prompt work', routineId: 'routine-1', archived: true, pendingQuestion: true },
];

test('phone filters use desktop options, defaults and saved preferences', async ({ mockedPage: page }) => {
  await page.route('/api/sessions*', route => route.fulfill({ json: rows }));
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto('/sessions');
  for (const name of ['Routine work', 'Routine child', 'Factory work', 'Archived work', 'Snoozed work']) {
    await expect(page.getByRole('link', { name: new RegExp(name) })).toHaveCount(0);
  }
  await expect(page.getByRole('link', { name: /Normal child/ })).toBeVisible();
  await expect(page.getByRole('link', { name: /Pending prompt work/ })).toBeVisible();
  await page.getByRole('button', { name: 'Filter sessions' }).click();
  const mobileMenu = page.getByRole('dialog', { name: 'Session filters' });
  const defaults = await mobileMenu.getByRole('checkbox').evaluateAll(elements => elements.map(element => ({
    label: element.closest('label')!.textContent!.trim(), checked: (element as HTMLInputElement).checked,
  })));
  expect(defaults).toEqual([
    { label: 'Always show prompts', checked: true }, { label: 'Show archived', checked: false },
    { label: 'Show snoozed', checked: false }, { label: 'Show children', checked: true },
    { label: 'Show factory', checked: false }, { label: 'Show routines', checked: false },
  ]);
  await mobileMenu.getByRole('checkbox', { name: 'Show routines' }).check();
  await mobileMenu.getByRole('checkbox', { name: 'Show factory' }).check();
  await mobileMenu.getByRole('checkbox', { name: 'Show snoozed' }).check();
  await expect(page.getByRole('link', { name: /Routine work/ })).toBeVisible();
  await expect(page.getByRole('link', { name: /Routine child/ })).toBeVisible();
  await expect(page.getByRole('link', { name: /Factory work/ })).toBeVisible();
  await expect(page.getByRole('link', { name: /Snoozed work/ })).toBeVisible();
  await expect(page.getByRole('link', { name: /Archived work/ })).toHaveCount(0);
  await mobileMenu.getByRole('checkbox', { name: 'Show children' }).uncheck();
  await mobileMenu.getByRole('checkbox', { name: 'Always show prompts' }).uncheck();
  await expect(page.getByRole('link', { name: /Routine child/ })).toHaveCount(0);
  await expect(page.getByRole('link', { name: /Normal child/ })).toHaveCount(0);
  await expect(page.getByRole('link', { name: /Pending prompt work/ })).toHaveCount(0);
  await mobileMenu.getByRole('checkbox', { name: 'Show archived' }).check();
  await expect(page.getByRole('link', { name: /Archived work/ })).toBeVisible();
  const selected = await mobileMenu.getByRole('checkbox').evaluateAll(elements => elements.map(element => (element as HTMLInputElement).checked));

  await page.setViewportSize({ width: 1280, height: 900 });
  await page.goto(`/session/${MOCK_SESSION.id}`);
  await page.getByRole('button', { name: 'Filter sessions' }).click();
  const desktopMenu = page.getByRole('group', { name: 'Session filters' });
  expect(await desktopMenu.getByRole('checkbox').evaluateAll(elements => elements.map(element => (element as HTMLInputElement).checked))).toEqual(selected);
  await desktopMenu.getByRole('checkbox', { name: 'Show routines' }).uncheck();
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto('/sessions');
  await expect(page.getByRole('link', { name: /Routine work/ })).toHaveCount(0);
  await page.getByRole('button', { name: 'Filter sessions' }).click();
  await expect(page.getByRole('checkbox', { name: 'Show routines' })).not.toBeChecked();
});

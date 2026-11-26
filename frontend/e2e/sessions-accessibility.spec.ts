import { test, expect, MOCK_SESSION, MOCK_SESSION_2 } from './fixtures';

test.use({ serviceWorkers: 'block' });

const sessionsPath = /\/api\/sessions(?:\?.*)?$/;

test('the viewport allows zoom without changing safe-area or keyboard layout', async ({ mockedPage: page }) => {
  await page.goto('/sessions');
  const viewport = page.locator('meta[name="viewport"]');
  await expect(viewport).not.toHaveAttribute('content', /maximum-scale|user-scalable/);
  await expect(viewport).toHaveAttribute('content', /viewport-fit=cover/);
  await expect(viewport).toHaveAttribute('content', /interactive-widget=resizes-content/);
});

test('same-prefix titles remain distinguishable at 320px without shrinking Archive', async ({ mockedPage: page }) => {
  const staging = 'Review the release checklist for staging';
  const production = 'Review the release checklist for production';
  await page.route(sessionsPath, route => route.fulfill({ json: [
    { ...MOCK_SESSION, title: staging },
    { ...MOCK_SESSION_2, title: production },
  ] }));
  await page.setViewportSize({ width: 320, height: 750 });
  await page.goto('/sessions');
  for (const title of [staging, production]) {
    const row = page.getByRole('row').filter({ has: page.getByRole('link', { name: new RegExp(title) }) });
    const text = row.getByText(title, { exact: true });
    await expect(text).toBeVisible();
    // DOM text alone is not enough: check that the suffix is inside the visible box.
    expect(await text.evaluate(el => {
      const lastWord = el.textContent!.split(' ').at(-1)!;
      const range = document.createRange();
      range.setStart(el.firstChild!, el.textContent!.length - lastWord.length);
      range.setEnd(el.firstChild!, el.textContent!.length);
      const end = range.getBoundingClientRect();
      const box = el.getBoundingClientRect();
      return end.right <= box.right + 1 && end.bottom <= box.bottom + 1;
    })).toBe(true);
    await expect(text).toHaveAttribute('title', title);
    const archive = await row.getByRole('button', { name: 'Archive session', exact: true }).boundingBox();
    expect(archive?.width).toBeGreaterThanOrEqual(44);
    expect(archive?.height).toBeGreaterThanOrEqual(44);
  }
  await page.setViewportSize({ width: 1280, height: 900 });
  await expect(page.getByText(production, { exact: true })).toHaveCSS('white-space', 'nowrap');
});

test('pending session placeholders stay visible without animation under reduced motion', async ({ mockedPage: page }) => {
  let finish!: () => void;
  const pending = new Promise<void>(resolve => { finish = resolve; });
  await page.route(sessionsPath, async route => {
    await pending;
    await route.fulfill({ json: [MOCK_SESSION] });
  });
  try {
    await page.emulateMedia({ reducedMotion: 'reduce' });
    await page.goto('/sessions');
    const table = page.getByRole('table', { name: 'Loading sessions' });
    const placeholder = table.locator('tbody [aria-hidden="true"]').first();
    await expect(placeholder).toBeVisible();
    await expect(placeholder).toHaveCSS('animation-name', 'none');
    await page.emulateMedia({ reducedMotion: 'no-preference' });
    await expect(placeholder).not.toHaveCSS('animation-name', 'none');
  } finally {
    finish();
  }
  await expect(page.getByRole('link', { name: /Fix the login bug/ })).toBeVisible();
});

test('first use offers to start a session from the empty state', async ({ mockedPage: page }) => {
  await page.route(sessionsPath, route => route.fulfill({ json: [] }));
  await page.goto('/sessions');
  const empty = page.getByRole('region', { name: 'No sessions yet' });
  await empty.getByRole('button', { name: 'New session', exact: true }).click();
  await expect(page.getByPlaceholder('Select a project to start a session...')).toBeVisible();
});

test('clear filters restores sessions and preserves unrelated URL parameters', async ({ mockedPage: page }) => {
  await page.goto('/sessions?t=12&dir=%2Fmissing&remoteId=build&sort=started');
  await page.getByRole('searchbox', { name: 'Search sessions' }).fill('missing query');
  const empty = page.getByRole('region', { name: 'No matching sessions' });
  await empty.getByRole('button', { name: 'Clear filters' }).click();
  await expect(page.getByRole('link', { name: /Fix the login bug/ })).toBeVisible();
  await expect(page.getByRole('searchbox', { name: 'Search sessions' })).toHaveValue('');
  await expect(page).toHaveURL(url => url.searchParams.get('t') === '0'
    && !url.searchParams.has('dir') && url.searchParams.get('remoteId') === 'build'
    && url.searchParams.get('sort') === 'started');
});

test('an empty default time window is not first use when older sessions exist', async ({ mockedPage: page }) => {
  await page.route(sessionsPath, route => route.fulfill({ json:
    new URL(route.request().url()).searchParams.has('sinceHours') ? [] : [MOCK_SESSION],
  }));
  await page.goto('/sessions');
  const empty = page.getByRole('region', { name: 'No matching sessions' });
  await empty.getByRole('button', { name: 'Clear filters' }).click();
  await expect(page.getByRole('link', { name: /Fix the login bug/ })).toBeVisible();
});

test('a failed load is unavailable, not empty, and retry can reach first use', async ({ mockedPage: page }) => {
  let failed = true;
  await page.route(sessionsPath, route => failed
    ? route.fulfill({ status: 400, contentType: 'text/plain', body: 'Sessions could not be read.' })
    : route.fulfill({ json: [] }));
  await page.goto('/sessions');
  const error = page.getByRole('alert', { name: 'Could not load sessions' });
  await expect(error).toContainText('Sessions could not be read.');
  await expect(page.getByRole('heading', { name: /No (matching )?sessions/ })).toHaveCount(0);
  failed = false;
  await error.getByRole('button', { name: 'Retry' }).click();
  await expect(page.getByRole('heading', { name: 'No sessions yet' })).toBeVisible();
});

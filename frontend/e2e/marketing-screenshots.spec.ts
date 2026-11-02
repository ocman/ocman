import { resolve } from 'node:path';
import { test, expect, MOCK_SESSION, MOCK_PROJECT, type Page } from './fixtures';
import { installChangesDemo, installAnalyticsDemo } from './marketing-extra-fixtures';

// Entirely fictional fixtures. Never connect this capture to a running backend.
const now = Date.parse('2026-10-20T14:30:00Z');
const directory = '/demo/projects/atlas-web';
const sessions = [
  { title: 'Add keyboard navigation', platform: 'opencode', status: 'idle', directory },
  { title: 'Polish the onboarding flow', platform: 'claude-code', status: 'busy', directory },
  { title: 'Cover checkout edge cases', platform: 'codex', status: 'waiting', directory },
  { title: 'Build the activity feed', platform: 'opencode', status: 'idle', directory: '/demo/projects/orbit-api' },
  { title: 'Review the search endpoint', platform: 'claude-code', status: 'busy', directory: '/demo/projects/orbit-api' },
  { title: 'Document the release process', platform: 'codex', status: 'idle', directory: '/demo/projects/field-notes' },
].map((item, index) => ({
  ...MOCK_SESSION, ...item, id: `demo-session-${index}`, seen: true,
  liveConnection: true, messageCount: 8 + index * 4,
  timeCreated: now - (index + 1) * 1_800_000,
  timeUpdated: now - (index + 1) * 60_000, durationMs: (index + 1) * 240_000,
}));
const session = sessions[0];
const projects = [...new Set(sessions.map(item => item.directory))].map(path => ({
  ...MOCK_PROJECT, directory: path, lastUsed: now - 60_000,
  sessionCount: sessions.filter(item => item.directory === path).length,
}));
const messages = [
  { id: 'demo-user', sessionId: session.id, timeCreated: now - 300_000, data: { role: 'user' } },
  { id: 'demo-answer', sessionId: session.id, timeCreated: now - 240_000, data: {
    role: 'assistant', finish: 'stop', modelID: 'demo-model', providerID: 'demo',
  } },
];
const parts = [
  { id: 'demo-prompt', messageId: 'demo-user', sessionId: session.id, data: {
    type: 'text', text: 'Add keyboard navigation to the project switcher. Arrow keys should move between projects, Enter should select one, and Escape should close the menu. Include tests.',
  } },
  { id: 'demo-result', messageId: 'demo-answer', sessionId: session.id, data: {
    type: 'text', text: [
      '## Keyboard navigation is ready',
      '',
      'The project switcher now works without a mouse. Focus stays inside the menu until you select a project or dismiss it.',
      '',
      '| Key | Action |',
      '| --- | --- |',
      '| Arrow up / down | Move between projects |',
      '| Enter | Open the selected project |',
      '| Escape | Close the menu and restore focus |',
      '',
      '### Changes',
      '',
      '- Added roving focus in `src/components/ProjectSwitcher.tsx`.',
      '- Kept the active project visible while navigating.',
      '- Added regression tests for selection, dismissal, and focus restoration.',
      '',
      '**Checks passed:** 24 tests, TypeScript, and lint. Ready for review.',
    ].join('\n'),
  } },
];

test.use({
  viewport: { width: 1600, height: 1000 }, deviceScaleFactor: 1,
  serviceWorkers: 'block', locale: 'en-US', timezoneId: 'UTC', colorScheme: 'dark',
});

async function installMarketingData(page: Page, changes: boolean) {
  await page.clock.setFixedTime(now);
  await page.addInitScript((showChanges) => {
    // An open, quiet synthetic stream. Fulfilled SSE responses close at once
    // in Playwright and would otherwise show an artificial reconnect warning.
    class DemoEventSource extends EventTarget {
      readonly readyState = 1;
      onopen: ((event: Event) => void) | null = null;
      constructor(readonly url: string | URL) {
        super();
        queueMicrotask(() => this.onopen?.(new Event('open')));
      }
      close() { /* No socket was opened. */ }
    }
    window.EventSource = DemoEventSource as unknown as typeof EventSource;
    localStorage.setItem('ocman:ui', JSON.stringify({ version: 7, state: {
      changesSidebarOpenTabs: showChanges ? ['working-tree', 'upstream'] : [], sidebarWidth: 270,
      changesSidebarWidth: 420, changesSidebarTabSizes: { 'working-tree': 0.4, upstream: 0.6 },
    } }));
  }, changes);
  await page.route('/api/sessions*', route => route.fulfill({ json: sessions }));
  await page.route('/api/sessions/notify*', route => route.fulfill({ json: [] }));
  await page.route('/api/projects*', route => route.fulfill({ json: projects }));
  await page.route('/api/git/info*', route => route.fulfill({ json: {
    branch: 'feat/keyboard-navigation', ahead: 2, behind: 0, dirty: false,
  } }));
  const demoParts = changes ? [parts[0], { ...parts[1], data: {
    type: 'text', text: '## Keyboard navigation is ready\n\nArrow keys select a project. Enter opens it. Escape closes the menu and restores focus.\n\n**Checks passed:** 24 tests, TypeScript, and lint. PR #42 is ready for review.',
  } }, await installChangesDemo(page, session.id, directory)] : parts;
  await page.route(new RegExp(`/api/session/${session.id}(\\?|$)`), route => route.fulfill({
    json: { session, messages, parts: demoParts, totalMessages: messages.length, defaultAgent: 'build' },
  }));
}

for (const screen of ['conversation', 'session-browser', 'changes', 'analytics'] as const) {
  test(`marketing screenshot: ${screen} uses only fictional data`, async ({ mockedPage: page, context, baseURL }, info) => {
    const escapedRequests: string[] = [];
    const pageErrors: string[] = [];
    page.on('pageerror', error => pageErrors.push(error.message));
    // Page-level fixture routes handle all APIs. Fail closed if any request
    // escapes them, including future endpoints or external image requests.
    await context.route('**/*', route => {
      const url = new URL(route.request().url());
      if (url.origin !== new URL(baseURL!).origin || url.pathname.startsWith('/api/')) {
        escapedRequests.push(`${url.origin}${url.pathname}`);
        return route.abort();
      }
      return route.continue();
    });
    await installMarketingData(page, screen === 'changes');
    if (screen === 'analytics') {
      await page.setViewportSize({ width: 1600, height: 1600 });
      await installAnalyticsDemo(page);
    }
    await page.goto(screen === 'analytics' ? '/analytics/overview' : screen === 'session-browser' ? '/sessions' : `/session/${session.id}`);
    if (screen === 'conversation' || screen === 'changes') {
      await expect(page.getByRole('heading', { name: 'Keyboard navigation is ready' })).toBeVisible();
      await expect(page.getByText('Checks passed:', { exact: false })).toBeVisible();
      await expect(page.getByTestId('session-sidebar').getByText('Polish the onboarding flow')).toBeVisible();
      if (screen === 'changes') {
        await expect(page.getByRole('region', { name: 'Linked pull requests' }).getByText('Watching', { exact: true })).toBeVisible();
        const edit = page.getByTestId('conversation-viewport').getByRole('button', { name: /Update src\/components\/ProjectSwitcher.tsx/ });
        await expect(edit).toBeVisible();
        if (await edit.getAttribute('aria-expanded') !== 'true') await edit.click();
        await expect(page.getByTestId('conversation-viewport').getByText('restoreFocusOnClose', { exact: false }).first()).toBeVisible();
      }
    } else if (screen === 'analytics') {
      await expect(page.getByRole('heading', { name: 'Token Usage', exact: true })).toBeVisible();
      await expect(page.getByText('384', { exact: true })).toBeVisible();
      await expect(page.getByText('No database size samples yet.')).toHaveCount(0);
      await expect(page.locator('canvas')).toHaveCount(2);
    } else {
      for (const item of sessions) await expect(page.getByRole('link').filter({ hasText: item.title })).toBeVisible();
    }
    await page.evaluate(() => document.fonts.ready);
    await expect(page.locator('body')).not.toContainText(/myapp|Fix the login bug|\/Users\/|NoUseFreak|dries/);
    await expect(page.getByText('Reconnecting to live updates', { exact: false })).toHaveCount(0);
    expect(escapedRequests).toEqual([]);
    expect(pageErrors).toEqual([]);
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
    const output = screen === 'conversation' ? '../docs/sessions.png' : `../site/static/screenshots/${screen}.png`;
    await page.screenshot({
      path: process.env.UPDATE_MARKETING_SCREENSHOTS === '1' ? resolve(output) : info.outputPath(`${screen}.png`),
      animations: 'disabled', caret: 'hide',
    });
    expect(escapedRequests).toEqual([]);
  });
}

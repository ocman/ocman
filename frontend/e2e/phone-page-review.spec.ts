import { test, expect } from './fixtures';
import { installPhoneReviewRoutes, phoneReviewPages } from './phone-review-fixtures';

for (const width of [320, 390]) {
  for (const [path, action] of [
    ['/routines', 'New routine'], ['/routines?tab=inboxes', 'New inbox'],
    ['/factory/epics', 'New epic'], ['/artifacts/mobile-review', 'Share'],
  ]) {
    test(`${action} drawer fits ${width}px`, async ({ mockedPage: page }, testInfo) => {
      await page.setViewportSize({ width, height: 844 });
      await installPhoneReviewRoutes(page);
      await page.goto(path);
      await page.getByRole('button', { name: action, exact: true }).click();
      const dialog = page.getByRole('dialog');
      await expect(dialog).toBeVisible();
      expect(await dialog.evaluate(el => el.scrollWidth - el.clientWidth)).toBeLessThanOrEqual(1);
      const undersized = await dialog.evaluate(el => [...el.querySelectorAll<HTMLElement>('button, input:not([type="checkbox"], [type="radio"], [type="hidden"]), select')]
        .filter(e => e.checkVisibility() && e.getBoundingClientRect().height < 43).map(e => e.getAttribute('aria-label') || e.textContent));
      expect(undersized).toEqual([]);
      await page.screenshot({ path: testInfo.outputPath(`drawer-${action.replaceAll(' ', '-')}-${width}.png`) });
    });
  }

  for (const [name, path] of phoneReviewPages) {
    test(`${name} fits ${width}px with touch-sized controls`, async ({ mockedPage: page }, testInfo) => {
      await page.setViewportSize({ width, height: 844 });
      await installPhoneReviewRoutes(page);
      const errors: string[] = [];
      page.on('pageerror', error => errors.push(error.message));
      await page.goto(path);
      await expect(page.getByRole('banner').getByRole('heading')).toBeVisible();
      if (name === 'conversation') {
        // Live sessions reconnect and poll; their rendered content, not network idle, is readiness.
        await expect(page.getByTestId('conversation-viewport').getByText('Desktop tables keep their current layout.')).toBeVisible();
      } else if (name === 'new-conversation') {
        await expect(page.getByRole('combobox', { name: 'Session target' })).toBeVisible();
      } else {
        await page.waitForLoadState('networkidle');
      }
      await expect(page.getByText('Something went wrong', { exact: true })).toHaveCount(0);
      expect(errors).toEqual([]);
      const layout = await page.evaluate(() => {
        const root = document.querySelector('.page-content') ?? document.querySelector('.content')!;
        const controls = [...root.querySelectorAll<HTMLElement>('button, .oc-button, select, textarea, input:not([type="checkbox"], [type="radio"], [type="hidden"])')].filter(e => e.checkVisibility());
        const label = (e: HTMLElement) => e.getAttribute('aria-label') || e.textContent?.trim().slice(0, 60) || e.tagName;
        const clipped = controls.filter(e => {
          const box = e.getBoundingClientRect();
          if (box.left >= -1 && box.right <= innerWidth + 1) return false;
          // Tables and tab strips may scroll inside their own bounded container.
          for (let p = e.parentElement; p && p !== root; p = p.parentElement) {
            if (['auto', 'scroll'].includes(getComputedStyle(p).overflowX) && p.scrollWidth > p.clientWidth) return false;
          }
          return true;
        }).map(label);
        return { overflow: root.scrollWidth - root.clientWidth, clipped,
          undersized: controls.filter(e => e.getBoundingClientRect().height < 43).map(label) };
      });
      expect(layout.overflow, 'The page itself must not need horizontal scrolling').toBeLessThanOrEqual(1);
      expect(layout.clipped, 'Controls must fit or have a bounded horizontal scroller').toEqual([]);
      expect(layout.undersized, 'Control touch targets must be at least 44px high').toEqual([]);
      await page.screenshot({ path: testInfo.outputPath(`${name}-${width}.png`) });
    });
  }
}

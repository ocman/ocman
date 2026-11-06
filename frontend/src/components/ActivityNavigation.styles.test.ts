import { expect, it } from 'vitest';
// @ts-expect-error Vitest runs in Node; application types intentionally exclude Node globals.
import { readFileSync } from 'node:fs';
const css = readFileSync(new URL('./MainNav.css', import.meta.url), 'utf8');

it('keeps the activity icon at the leading edge when the sidebar clips its hidden label', () => {
  const rule = css.match(/\.main-nav nav \.main-nav-usage \{([^}]+)\}/)?.[1];
  expect(rule).toContain('justify-content: flex-start');
});

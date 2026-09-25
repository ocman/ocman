// @ts-expect-error Vitest runs in Node; application types intentionally exclude Node globals.
import { readFileSync, readdirSync } from 'node:fs';
// @ts-expect-error Vitest runs in Node; application types intentionally exclude Node globals.
import { join } from 'node:path';
import { describe, expect, it } from 'vitest';

// The shared look-and-feel classes in shared.css are imported once at the
// app root; a component stylesheet redefining one at the same specificity
// silently wins app-wide, because component CSS resolves later in the
// import graph. jsdom applies no stylesheets and vitest serves CSS
// imports as empty, so this reads the source off disk instead.
const SRC = 'src';

function cssFiles(dir: string): string[] {
  const out: string[] = [];
  for (const entry of readdirSync(dir, { withFileTypes: true })) {
    const full = join(dir, entry.name);
    if (entry.isDirectory()) out.push(...cssFiles(full));
    else if (entry.name.endsWith('.css')) out.push(full);
  }
  return out;
}

/** Files carrying a top-level rule for exactly `selector`. */
function definedIn(selector: string): string[] {
  const pattern = new RegExp(`^\\s*\\${selector}\\s*(,[^{]*)?\\{`, 'm');
  return cssFiles(SRC)
    .filter((f) => pattern.test(readFileSync(f, 'utf8')))
    .map((f) => f.slice(SRC.length + 1))
    .sort();
}

describe('stylesheet ownership', () => {
  it.each([
    ['.oc-error-banner', ['shared.css']],
    ['.oc-error-boundary', ['shared.css']],
    // Print coordinates hiding app chrome across component boundaries.
    ['.main-nav', ['components/MainNav.css', 'print.css']],
    ['.app-header', ['components/AppHeader.css', 'print.css']],
  ] as const)('%s stays within its owner and explicit print overrides', (selector, owners) => {
    expect(definedIn(selector)).toEqual(owners);
  });
});

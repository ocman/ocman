import { readFileSync, readdirSync } from 'node:fs';
import { join } from 'node:path';
import { describe, expect, it } from 'vitest';

// The shared look-and-feel classes in tokens.css are imported once at the
// app root; a component stylesheet redefining one at the same specificity
// silently wins app-wide, because component CSS resolves later in the
// import graph. That can't be caught by a render test — jsdom applies no
// stylesheets — so check the source instead.
const SRC = join(__dirname);

function cssFiles(dir: string): string[] {
  const out: string[] = [];
  for (const entry of readdirSync(dir, { withFileTypes: true })) {
    const full = join(dir, entry.name);
    if (entry.isDirectory()) out.push(...cssFiles(full));
    else if (entry.name.endsWith('.css')) out.push(full);
  }
  return out;
}

/** Files containing a top-level rule for exactly `selector` (no parent scope). */
function definedIn(selector: string): string[] {
  const pattern = new RegExp(`^\\s*\\${selector}\\s*(,[^{]*)?\\{`, 'm');
  return cssFiles(SRC)
    .filter((f) => pattern.test(readFileSync(f, 'utf8')))
    .map((f) => f.slice(SRC.length + 1));
}

describe('shared token classes', () => {
  it.each([
    ['.oc-error-banner'],
    ['.oc-error-boundary'],
  ])('%s is defined in exactly one stylesheet', (selector) => {
    expect(definedIn(selector)).toEqual(['tokens.css']);
  });
});

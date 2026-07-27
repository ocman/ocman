import { describe, it, expect } from 'vitest';

// strictNullChecks and noImplicitAny were both off, so every null check
// in the codebase was unverified by the compiler. Enabling strict was
// free (zero new errors); this pins it so it can't be quietly dropped —
// tsc itself stays green if the flag disappears.
//
// Loaded through import.meta.glob rather than node:fs so the test needs
// no node types (tsconfig.app.json deliberately omits them).
const configs = import.meta.glob('../../tsconfig.{app,node}.json', {
  eager: true,
  query: '?raw',
  import: 'default',
}) as Record<string, string>;

describe('tsconfig strict mode', () => {
  it('finds both tsconfigs', () => {
    expect(Object.keys(configs).sort()).toEqual([
      '../../tsconfig.app.json',
      '../../tsconfig.node.json',
    ]);
  });

  for (const [path, raw] of Object.entries(configs)) {
    it(`${path} enables strict`, () => {
      // Strip the /* ... */ section comments so JSON.parse accepts it.
      const json = raw.replace(/^\s*\/\*.*?\*\/\s*$/gm, '');
      const config = JSON.parse(json) as { compilerOptions?: Record<string, unknown> };
      expect(config.compilerOptions?.strict).toBe(true);
    });
  }
});

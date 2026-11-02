import { expect, it, vi } from 'vitest';

// Setup has already run before this file. Re-evaluate it during collection so
// its hooks can register, while recording browser-only dependency imports.
vi.resetModules();
const reactSetup = vi.fn(async () => vi.importActual('@testing-library/react'));
const domMatchers = vi.fn(async () => vi.importActual('@testing-library/jest-dom/vitest'));
vi.doMock('@testing-library/react', reactSetup);
vi.doMock('@testing-library/jest-dom/vitest', domMatchers);
await import('../vitest.setup');

it('does not load DOM testing dependencies in the Node environment', () => {
  expect(typeof document).toBe('undefined');
  expect(reactSetup).not.toHaveBeenCalled();
  expect(domMatchers).not.toHaveBeenCalled();
});

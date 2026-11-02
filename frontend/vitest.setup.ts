// Shared state isolation applies to both Node and jsdom tests. Only jsdom
// tests load DOM matchers and React Testing Library.
import './test/broadcastChannel';
import 'fake-indexeddb/auto';
import { IDBFactory } from 'fake-indexeddb';
import { closeDraftDbForTests } from './src/lib/draftDb';
import { queryClient } from './src/lib/queryClient';

import { afterEach } from 'vitest';

// A static import would load the DOM dependency graph even for pure Node tests.
const cleanup = typeof document === 'undefined'
  ? undefined
  : (await import('./test/setupDom')).cleanup;

// jsdom ships no ResizeObserver, which anything measuring its own box (React
// Flow, xterm) constructs on mount. A no-op observer is enough: jsdom never
// lays out, so a real one would only ever report zeroes.
if (!('ResizeObserver' in globalThis)) {
  Object.defineProperty(globalThis, 'ResizeObserver', {
    configurable: true,
    value: class {
      observe() {}
      unobserve() {}
      disconnect() {}
    },
  });
}

// Keep DOM cleanup before shared state resets so unmount effects finish first.
afterEach(() => {
  cleanup?.();
  // Each test gets an empty draft database; module memory is reset by the tests that use it.
  closeDraftDbForTests();
  // The app-wide session cache (lib/sessionsCache) starts empty per test.
  queryClient.clear();
  globalThis.indexedDB = new IDBFactory();
});

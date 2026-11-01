import { lazyModule } from './lazyModule';

/**
 * highlight.js with its common grammars, loaded on first use. Code renders
 * escaped and unhighlighted until it arrives.
 */
export const highlighter = lazyModule(() => import('highlight.js/lib/common').then((m) => m.default));

import { highlighter } from '../../lib/highlighter';
import { useLazyModule } from '../../lib/lazyModule';
import { highlightDiffCode } from '../../lib/threadHelpers';

/**
 * A code snippet highlighted with highlight.js. It renders escaped plain text
 * while highlight.js loads, then re-renders highlighted.
 */
export function HighlightedCode({ code, language, className }: { code: string; language?: string; className?: string }) {
  // Subscribing starts the load and re-renders once highlight.js arrives.
  useLazyModule(highlighter);
  return <span className={className} dangerouslySetInnerHTML={{ __html: highlightDiffCode(code, language) }} />;
}

import remarkGfm from 'remark-gfm';
import remarkParse from 'remark-parse';
import remarkRehype from 'remark-rehype';
import { unified } from 'unified';

// Match react-markdown's parser/tree, but never construct React elements, HTML,
// or a browser document. Raw HTML stays literal text, not executable markup.
const parser = unified().use(remarkParse).use(remarkGfm)
  .use(remarkRehype, { allowDangerousHtml: true });
type SpeechNode = { type: string; value?: string; tagName?: string; children?: SpeechNode[] };
const boundaries = new Set(['p', 'li', 'tr', 'td', 'th', 'h1', 'h2', 'h3', 'h4', 'h5', 'h6']);

function treeText(node: SpeechNode): string {
  if (node.type === 'text' || node.type === 'raw') return node.value ?? '';
  if (node.tagName === 'pre' || node.tagName === 'img') return '';
  return (node.children?.map(treeText).join('') ?? '')
    + (node.tagName && boundaries.has(node.tagName) ? ' ' : '');
}

const SPEECH_CACHE_MAX = 500;
const speechCache = new Map<string, string>();

export function speechText(markdown: string): string {
  let prose = speechCache.get(markdown);
  if (prose === undefined) {
    prose = treeText(parser.runSync(parser.parse(markdown))).replace(/\s+/g, ' ').trim();
    if (speechCache.size >= SPEECH_CACHE_MAX) speechCache.delete(speechCache.keys().next().value!);
    speechCache.set(markdown, prose);
  }
  return prose;
}

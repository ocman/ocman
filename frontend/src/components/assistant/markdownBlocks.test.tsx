// @vitest-environment jsdom
import { cleanup, render } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';

// Reference rendering: the same component with splitting disabled.
const split = vi.hoisted(() => ({ on: true }));
vi.mock('./markdownBlocks', async (importOriginal) => {
  const real = await importOriginal<typeof import('./markdownBlocks')>();
  return { splitMarkdownBlocks: (t: string) => (split.on ? real.splitMarkdownBlocks(t) : [t]) };
});

import { splitMarkdownBlocks } from './markdownBlocks';
import { MarkdownContent } from './MarkdownText';

afterEach(cleanup);

function html(text: string, preserveLineBreaks = false) {
  const { container, unmount } = render(<MarkdownContent text={text} preserveLineBreaks={preserveLineBreaks} />);
  const out = container.innerHTML;
  unmount();
  return out;
}

function expectSameDom(text: string, preserveLineBreaks = false) {
  split.on = false;
  const whole = html(text, preserveLineBreaks);
  split.on = true;
  expect(html(text, preserveLineBreaks)).toBe(whole);
}

const CORPUS: Record<string, string> = {
  paragraphs: 'One\ntwo\n\nThree **bold** and `code`.\n\n\n\nFour',
  headings: '# H1\n\nText\n\nSetext\n===\n\n## H2\n---\n\n***\n\nend',
  tightList: '- a\n- b\n- c\n\nAfter list',
  looseList: '- a\n\n- b\n\n- c\n\nAfter',
  orderedStart: '3. three\n\n4. four\n\n10) ten\n\nTail',
  listContinuation: '- item\n\n  continued para\n\n      indented code in item\n\nOut',
  nested: '1. outer\n   - inner\n\n     inner para\n\n2. next\n\nDone',
  blockquote: '> quote\n> more\n\n> second\n\nafter\n> lazy',
  fence: 'Intro\n\n```ts\nconst a = 1;\n\n\nconst b = 2;\n```\n\nAfter',
  tildeFence: '~~~\nx\n\ny\n~~~\n\nz',
  longFence: '````md\n```\ninner\n\n```\nstill inside\n````\n\noutside',
  backtickInfo: '```a`b\nnot a fence\n\nnext',
  unterminated: 'Start\n\n```go\nfunc x() {\n\n  y()',
  indentedCode: 'Para\n\n    code line\n\n    more code\n\nback',
  table: '| a | b |\n| --- | ---: |\n| 1 | 2 |\n\n| c |\n| - |\n| 3 |\n\ntext',
  htmlBlock: '<div class="x">\n\n*md*\n\n</div>\n\npara',
  rawPre: '<pre>\n\nkeep\n\n</pre>\n\nafter',
  refDefs: 'See [docs][d].\n\n[d]: https://example.com\n\nend',
  footnote: 'Claim[^1].\n\n[^1]: Source.\n\nmore',
  factoryCard: 'Done [[ocman:card type=factory-epic epic=e1 action=created]]\n\nnext',
  links: 'Go [home](/factory) or [out](https://x.y) and <https://auto.link>\n\n#hash',
  leadingTrailing: '\n\n\nfirst\n\nsecond\n\n\n',
  crlf: 'a\r\n\r\nb\r\n- c\r\n\r\nd',
  breaks: 'line one\nline two\n\nline three\nline four',
};

describe('splitMarkdownBlocks', () => {
  it('splits at safe top-level boundaries only', () => {
    expect(splitMarkdownBlocks('a\n\nb')).toEqual(['a', 'b']);
    expect(splitMarkdownBlocks('- a\n\n- b')).toEqual(['- a\n\n- b']);
    expect(splitMarkdownBlocks('- a\n\n  b')).toEqual(['- a\n\n  b']);
    expect(splitMarkdownBlocks('```\na\n\nb\n```\n\nc')).toEqual(['```\na\n\nb\n```', 'c']);
    expect(splitMarkdownBlocks('x [r]\n\n[r]: /u')).toEqual(['x [r]\n\n[r]: /u']);
  });

  it.each(Object.entries(CORPUS))('renders %s identically to a single parse', (_name, text) => {
    expectSameDom(text);
  });

  it('keeps remark-breaks output identical', () => {
    expectSameDom(CORPUS.breaks, true);
  });

  it('renders every streaming prefix of a long answer identically', () => {
    const text = Object.entries(CORPUS)
      .filter(([name]) => !['refDefs', 'footnote', 'rawPre'].includes(name))
      .map(([, t]) => t).join('\n\n');
    for (let end = 1; end <= text.length; end += 89) expectSameDom(text.slice(0, end));
    expectSameDom(text);
  });
});

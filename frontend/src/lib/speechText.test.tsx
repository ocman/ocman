// @vitest-environment jsdom
import { renderToStaticMarkup } from 'react-dom/server';
import Markdown from 'react-markdown';
import remarkGfm from 'remark-gfm';
import { describe, expect, it, vi } from 'vitest';
import { speechText } from './turnSpeech';

// Retain the old pipeline as a test oracle, never as a production dependency.
function legacy(markdown: string) {
  const html = renderToStaticMarkup(<Markdown remarkPlugins={[remarkGfm]} components={{
    pre: () => null, img: () => null,
    p: ({ children }) => <p>{children}{'\n'}</p>,
    li: ({ children }) => <li>{children}{'\n'}</li>,
    tr: ({ children }) => <tr>{children}{'\n'}</tr>,
    td: ({ children }) => <td>{children}{' '}</td>,
    th: ({ children }) => <th>{children}{' '}</th>,
    h1: ({ children }) => <h1>{children}{'\n'}</h1>,
    h2: ({ children }) => <h2>{children}{'\n'}</h2>,
    h3: ({ children }) => <h3>{children}{'\n'}</h3>,
    h4: ({ children }) => <h4>{children}{'\n'}</h4>,
    h5: ({ children }) => <h5>{children}{'\n'}</h5>,
    h6: ({ children }) => <h6>{children}{'\n'}</h6>,
  }}>{markdown}</Markdown>);
  return new DOMParser().parseFromString(html, 'text/html').body.textContent!.replace(/\s+/g, ' ').trim();
}

describe('speech output equivalence', () => {
  it.each([
    '', '# Heading\n\nParagraph **bold** and *emphasis*, ~~deleted~~, `inline code`.',
    'one  \ntwo\nthree\n\n---\n\nfour',
    '- one\n- two\n  - nested\n\n1. ordered\n2. last',
    '- loose\n\n  paragraph\n\n- [x] checked\n- [ ] unchecked',
    '> quote\n>\n> second\n\nend',
    '| A | B |\n| - | - |\n| **one** | `two` |\n| ![alt](secret) | [label](https://secret) |',
    '[label](javascript:alert%281%29) <https://example.com> test@example.com',
    '[reference][id]\n\n[id]: https://secret.example "title"',
    'before![secret alt](https://secret.example)after ![missing][unknown]',
    '&amp; &lt; &#65; &#x1f600; &nbsp; &copy; &unknown; \\*escaped\\*',
    '<script>alert("secret")</script>\n\n<span>literal HTML</span><!-- comment -->',
    'before\n\n```sh\nrm -rf /secret\n```\n\nafter\n\n    indented secret',
    '~~~sh\nunterminated secret command',
    'before\n\n- item\n\n  ```sh\n  nested secret\n  ```\n\n  after',
    'Footnote[^one].\n\n[^one]: Note with **formatting**.',
    'a\u0000b\r\nc\t d\u00a0e',
  ])('matches the existing rendered text for %j', (markdown) => {
    expect(speechText(markdown)).toBe(legacy(markdown));
  });

  it('extracts eagerly without HTML serialization or DOM parsing', () => {
    const parse = vi.spyOn(DOMParser.prototype, 'parseFromString').mockImplementation(() => {
      throw new Error('speech must not parse HTML');
    });
    try {
      expect(speechText('DOM-free **eager** speech')).toBe('DOM-free eager speech');
      expect(parse).not.toHaveBeenCalled();
    } finally { parse.mockRestore(); }
  });
});

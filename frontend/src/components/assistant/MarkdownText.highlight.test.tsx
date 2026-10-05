// @vitest-environment jsdom
import { render } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';

// Each rehype-highlight attach builds a lowlight instance with ~37 languages.
const attached = vi.hoisted(() => ({ count: 0 }));
vi.mock('rehype-highlight', async (importOriginal) => {
  const real = (await importOriginal<typeof import('rehype-highlight')>()).default;
  return { default: (...args: Parameters<typeof real>) => { attached.count++; return real(...args); } };
});

import { MarkdownContent } from './MarkdownText';

describe('MarkdownContent highlighting', () => {
  it('builds the highlighter once across renders and still highlights', () => {
    const code = 'Intro\n\n```ts\nconst a = 1;\n```';
    const first = render(<MarkdownContent text={code} />);
    first.rerender(<MarkdownContent text={`${code}\n\nmore`} />);
    render(<MarkdownContent text={'```go\nfunc x() {}\n```'} />);
    expect(attached.count).toBe(1);
    expect(first.container.querySelector('code.hljs.language-ts .hljs-keyword')).toHaveTextContent('const');
  });
});

// @vitest-environment jsdom
import { render, waitFor } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';

// Each rehype-highlight attach builds a lowlight instance with ~37 languages.
const attached = vi.hoisted(() => ({ count: 0 }));
vi.mock('rehype-highlight', async (importOriginal) => {
  const real = (await importOriginal<typeof import('rehype-highlight')>()).default;
  return { default: (...args: Parameters<typeof real>) => { attached.count++; return real(...args); } };
});

import { MarkdownContent } from './MarkdownText';

describe('MarkdownContent highlighting', () => {
  it('renders code unhighlighted while the highlighter loads, then highlights it', async () => {
    const code = 'Intro\n\n```ts\nconst a = 1;\n```';
    const first = render(<MarkdownContent text={code} />);
    expect(first.container.querySelector('code.language-ts')).toHaveTextContent('const a = 1;');
    expect(first.container.querySelector('code.hljs')).toBeNull();
    await waitFor(() => expect(first.container.querySelector('code.hljs.language-ts .hljs-keyword')).toHaveTextContent('const'));
  });

  it('builds the highlighter once across renders and still highlights', async () => {
    const code = 'Intro\n\n```ts\nconst a = 1;\n```';
    const first = render(<MarkdownContent text={code} />);
    first.rerender(<MarkdownContent text={`${code}\n\nmore`} />);
    const second = render(<MarkdownContent text={'```go\nfunc x() {}\n```'} />);
    await waitFor(() => expect(first.container.querySelector('code.hljs.language-ts .hljs-keyword')).toHaveTextContent('const'));
    expect(second.container.querySelector('code.hljs.language-go')).not.toBeNull();
    expect(attached.count).toBe(1);
  });
});

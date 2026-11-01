// @vitest-environment jsdom
import { act, render, screen, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';

const diffProps = vi.hoisted(() => [] as { oldFile: { lang?: string }; newFile: { lang?: string } }[]);
vi.mock('@pierre/diffs/react', () => ({
  MultiFileDiff: (props: (typeof diffProps)[number]) => { diffProps.push(props); return null; },
}));

import { LazyInlineDiff } from './LazyInlineDiff';

afterEach(() => { vi.unstubAllGlobals(); diffProps.length = 0; });

describe('LazyInlineDiff', () => {
  it('loads the diff renderer, renders plain text until near the viewport, then highlights', async () => {
    let fire: (entries: { isIntersecting: boolean }[]) => void = () => {};
    vi.stubGlobal('IntersectionObserver', class {
      constructor(cb: typeof fire) { fire = cb; }
      observe() {}
      disconnect() {}
    });
    const { container } = render(<LazyInlineDiff payload={{ __diff: true, filePath: 'a.ts', before: 'a', after: 'b' }} />);
    expect(container.firstElementChild).toHaveClass('oc-tool-output');
    expect(screen.getByRole('status')).toHaveTextContent('Loading diff…');
    await waitFor(() => expect(screen.queryByRole('status')).not.toBeInTheDocument());
    expect(diffProps.at(-1)!.oldFile.lang).toBe('text');
    expect(diffProps.at(-1)!.newFile.lang).toBe('text');
    act(() => fire([{ isIntersecting: true }]));
    expect(diffProps.at(-1)!.oldFile.lang).toBeUndefined();
    expect(diffProps.at(-1)!.newFile.lang).toBeUndefined();
  });
});

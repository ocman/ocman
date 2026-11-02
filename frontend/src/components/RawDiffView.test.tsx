// @vitest-environment jsdom
import { render } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { RawDiffView } from './RawDiffView';

const patchDiff = vi.hoisted(() => vi.fn((props: { patch: string }) => <pre>{props.patch}</pre>));
vi.mock('@pierre/diffs/react', () => ({ PatchDiff: patchDiff }));

const patch = (path: string) => `diff --git a/${path} b/${path}\n--- a/${path}\n+++ b/${path}\n@@ -1,2 +1,2 @@\n-a\n+b\n `;

describe('RawDiffView', () => {
  it('keeps a trailing blank context line of a single-file patch', () => {
    render(<RawDiffView diff={patch('x')} />);
    expect(patchDiff).toHaveBeenCalledTimes(1);
    expect(patchDiff.mock.calls[0][0].patch).toBe(patch('x'));
  });

  it('renders each file section of a concatenated patch separately, keeping its context lines', () => {
    patchDiff.mockClear();
    render(<RawDiffView diff={`${patch('x')}\n${patch('y')}`} />);
    expect(patchDiff.mock.calls.map(([props]) => props.patch)).toEqual([patch('x'), patch('y')]);
  });
});

// @vitest-environment jsdom
import { renderHook } from '@testing-library/react';
import { expect, it } from 'vitest';
import { useProjectTarget } from './useProjectTarget';

it('retains project identity while a sibling session is unresolved', () => {
  const { result, rerender } = renderHook(({ directory, session }) => useProjectTarget(directory, session), {
    initialProps: { directory: '/first' as string | undefined, session: { projectId: 'p', remoteId: 'box' } as { projectId: string; remoteId: string } | undefined },
  });
  rerender({ directory: undefined, session: undefined });
  expect(result.current).toMatchObject({ directory: '/first', remoteId: 'box', projectId: 'p' });
  rerender({ directory: '/second', session: { projectId: 'p', remoteId: 'box' } });
  expect(result.current).toMatchObject({ directory: '/first', projectId: 'p' });
  rerender({ directory: '/other', session: { projectId: 'other', remoteId: 'box' } });
  expect(result.current).toMatchObject({ directory: '/other', projectId: 'other' });
});

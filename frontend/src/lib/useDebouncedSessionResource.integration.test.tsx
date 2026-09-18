// @vitest-environment jsdom

import { act, renderHook, waitFor } from '@testing-library/react';
import { expect, it, vi } from 'vitest';
import { useDebouncedSessionResource } from './useDebouncedSessionResource';

it('keeps the previous result when a refresh fails', async () => {
  const fetch = vi.fn()
    .mockResolvedValueOnce('previous result')
    .mockRejectedValueOnce(new Error('network failure'));
  const { result } = renderHook(() =>
    useDebouncedSessionResource('session-1', fetch, '', 'failed'),
  );

  await waitFor(() => expect(result.current.data).toBe('previous result'));
  act(() => result.current.refresh());
  await waitFor(() => expect(result.current.error).toBe('network failure'));

  expect(result.current.data).toBe('previous result');
});

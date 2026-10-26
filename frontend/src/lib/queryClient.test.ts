import { describe, expect, it } from 'vitest';
import { APIError } from './api.requests';
import { queryClient } from './queryClient';

const shouldRetry = (failureCount: number, error: unknown) => {
  const retry = queryClient.getDefaultOptions().queries?.retry;
  if (typeof retry !== 'function') throw new Error('expected a retry predicate');
  return retry(failureCount, error as Error);
};

describe('queryClient retry', () => {
  it.each([400, 404, 409, 422])('does not retry an API %i whose message is the response body', (status) => {
    expect(shouldRetry(0, new APIError('Could not load projects.', status))).toBe(false);
  });

  it('does not retry a bare HTTP 4xx message', () => {
    expect(shouldRetry(0, new Error('HTTP 404'))).toBe(false);
  });

  it('does not retry an abort', () => {
    expect(shouldRetry(0, new DOMException('aborted', 'AbortError'))).toBe(false);
  });

  it('retries a server or network failure once', () => {
    const errors = [new APIError('upstream exploded', 500), new Error('HTTP 503'), new TypeError('Failed to fetch')];
    for (const error of errors) {
      expect(shouldRetry(0, error)).toBe(true);
      expect(shouldRetry(1, error)).toBe(false);
    }
  });
});

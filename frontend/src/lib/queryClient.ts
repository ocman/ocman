import { QueryClient } from '@tanstack/react-query';

// The app's one QueryClient. Sensible defaults:
// - staleTime: 10s — data is considered fresh for 10s after fetch,
//   so rapid navigation doesn't re-fetch immediately.
// - No auto-retry on 4xx (client errors are not transient).
// - Retry once on 5xx / network errors.
// It is a module singleton so code outside React (store actions, slash
// commands, SSE handlers) can patch the same session caches React reads.
export const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      staleTime: 10_000,
      retry: (failureCount, error) => {
        // Don't retry client errors (4xx) or aborts.
        if (error instanceof DOMException && error.name === 'AbortError') return false;
        // APIError messages carry the response body, so check its status.
        // Not imported: the test setup loads this module before specs mock its graph.
        const apiStatus = (error as { status?: unknown } | null)?.status;
        if (typeof apiStatus === 'number' && apiStatus >= 400 && apiStatus < 500) return false;
        if (error instanceof Error && error.message.match(/^HTTP [45]\d\d/)) {
          const status = parseInt(error.message.slice(5), 10);
          if (status >= 400 && status < 500) return false;
        }
        return failureCount < 1;
      },
    },
  },
});

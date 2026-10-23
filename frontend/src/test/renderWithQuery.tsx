import type { ReactElement, ReactNode } from 'react';
import { render, renderHook, type RenderHookOptions, type RenderOptions } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';

function queryWrapper() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const wrapper = ({ children }: { children: ReactNode }) => <QueryClientProvider client={client}>{children}</QueryClientProvider>;
  return { client, wrapper };
}

/** Render under a fresh QueryClient (no retries), kept across rerenders. */
export function renderWithQuery(ui: ReactElement, options?: Omit<RenderOptions, 'wrapper'>) {
  const { client, wrapper } = queryWrapper();
  return { client, ...render(ui, { ...options, wrapper }) };
}

/** renderHook under a fresh QueryClient (no retries), kept across rerenders. */
export function renderHookWithQuery<Result, Props>(
  hook: (props: Props) => Result,
  options?: Omit<RenderHookOptions<Props>, 'wrapper'>,
) {
  const { client, wrapper } = queryWrapper();
  return { client, ...renderHook(hook, { ...options, wrapper }) };
}

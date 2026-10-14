import type { ReactElement, ReactNode } from 'react';
import { render, type RenderOptions } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';

/** Render under a fresh QueryClient (no retries), kept across rerenders. */
export function renderWithQuery(ui: ReactElement, options?: Omit<RenderOptions, 'wrapper'>) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const wrapper = ({ children }: { children: ReactNode }) => <QueryClientProvider client={client}>{children}</QueryClientProvider>;
  return { client, ...render(ui, { ...options, wrapper }) };
}

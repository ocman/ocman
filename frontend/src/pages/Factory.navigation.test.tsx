// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { render, screen } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { api } from '../lib/api';
import { FactoryEpics } from './Factory';

vi.mock('../lib/api', () => ({ api: {
  factoryEpics: vi.fn(),
  factoryFormulas: vi.fn(),
  factoryQueue: vi.fn(),
  sessions: vi.fn(),
  createFactoryEpic: vi.fn(),
} }));

describe('Factory navigation', () => {
  beforeEach(() => {
    vi.mocked(api.factoryEpics).mockResolvedValue([]);
    vi.mocked(api.factoryFormulas).mockResolvedValue([]);
    vi.mocked(api.factoryQueue).mockResolvedValue([]);
    vi.mocked(api.sessions).mockResolvedValue([]);
  });

  it('identifies the current local route', async () => {
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    render(<QueryClientProvider client={client}><MemoryRouter initialEntries={['/factory/epics']}><FactoryEpics /></MemoryRouter></QueryClientProvider>);

    expect(await screen.findByRole('link', { name: 'Epics', current: 'page' })).toHaveAttribute('href', '/factory/epics');
    expect(screen.getByRole('navigation', { name: 'Factory' })).toBeInTheDocument();
  });
});

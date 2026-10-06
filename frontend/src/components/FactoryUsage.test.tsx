// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, screen, within } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { afterEach, expect, it, vi } from 'vitest';
import { fetchJSON } from '../lib/api';
import { FactoryUsage, type EpicUsage, type Usage } from './FactoryUsage';

vi.mock('../lib/api', () => ({ fetchJSON: vi.fn() }));
afterEach(() => vi.clearAllMocks());

const usage: Usage = { tokens: { cacheWrite: 40, cacheRead: 30, output: 20, input: 10 }, cost: 0, estCost: 1.5 };
const data: EpicUsage = { total: usage, phases: { plan: usage, implement: usage, verify: usage, deliver: usage }, attempts: [{ attemptId: 'a', workId: 'e.1', stage: 'verify', session: { id: 's/1', platform: 'p' }, usage }, { attemptId: 'b', workId: 'e.1', stage: 'plan', session: { id: '', platform: '' }, usage: null }], incomplete: true };

function show(props: { epicID: string; attemptID?: string; compact?: boolean }) {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	return render(<QueryClientProvider client={client}><MemoryRouter><FactoryUsage {...props} /></MemoryRouter></QueryClientProvider>);
}

it('shows phase totals and every attempt, keeping billing separate from estimates', async () => {
	vi.mocked(fetchJSON).mockResolvedValue(data);
	show({ epicID: 'e/1' });
	const table = await screen.findByRole('table', { name: 'Phase usage for e/1' });
	expect(fetchJSON).toHaveBeenCalledWith('/api/factory/epics/e%2F1/usage', expect.any(AbortSignal));
	expect(screen.getByText(/Incomplete totals/)).toBeInTheDocument();
	expect(within(table).getAllByRole('row')).toHaveLength(6);
	const row = within(table).getAllByRole('row')[1];
	expect(within(row).getAllByRole('cell').map((cell) => cell.textContent)).toEqual(['10', '20', '30', '40', '$0.0000', '$1.5000']);
	fireEvent.click(screen.getByText('Attempts'));
	expect(screen.getByRole('rowheader', { name: /Verify · e.1 · a/ })).toBeInTheDocument();
	expect(screen.getByRole('link', { name: 'Open session' })).toHaveAttribute('href', '/session/s%2F1?platform=p');
	expect(screen.getByText('Usage unavailable')).toBeInTheDocument();
});

it('shows an attempt token total including both cache buckets', async () => {
	vi.mocked(fetchJSON).mockResolvedValue(data);
	show({ epicID: 'e', attemptID: 'a' });
	expect(await screen.findByText('Attempt: 100 tokens · Billed $0.0000 · Estimated $1.5000')).toBeInTheDocument();
	expect(screen.queryByRole('table')).not.toBeInTheDocument();
});

it.each(['b', 'missing'])('shows unavailable attempt %s without pretending it cost zero', async (attemptID) => {
	vi.mocked(fetchJSON).mockResolvedValue(data);
	show({ epicID: 'e', attemptID });
	expect(await screen.findByText('Attempt usage unavailable')).toBeInTheDocument();
});

it('shows queue phase totals without attempt history', async () => {
	vi.mocked(fetchJSON).mockResolvedValue({ ...data, incomplete: false });
	show({ epicID: 'e', compact: true });
	await screen.findByRole('table');
	expect(screen.queryByText('Attempts')).not.toBeInTheDocument();
	expect(screen.queryByText(/Incomplete totals/)).not.toBeInTheDocument();
});

it('shows loading and failed reads', async () => {
	vi.mocked(fetchJSON).mockRejectedValue(new Error('offline'));
	show({ epicID: 'e' });
	expect(screen.getByRole('status')).toHaveTextContent('Loading usage');
	expect(await screen.findByRole('alert')).toHaveTextContent('Could not load usage.');
});

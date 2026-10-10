// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { api } from '../lib/api';
import { factoryCardFromToolResult } from './factoryCards';
import { FactoryMarkerCard } from './FactoryMarkerCard';

vi.mock('../lib/api', () => ({ api: { factoryControlAction: vi.fn(), decideFactoryControlAction: vi.fn() } }));

const pending = { id: 'fca_1', epicId: 'epic-1', action: 'reopen', summary: 'Run the tests again', status: 'pending' as const };

function renderCard() {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	return render(<QueryClientProvider client={client}><MemoryRouter><FactoryMarkerCard epicID="epic-1" issueID="fca_1" action="control" /></MemoryRouter></QueryClientProvider>);
}

describe('FactoryControlCard', () => {
	beforeEach(() => { vi.mocked(api.factoryControlAction).mockReset(); vi.mocked(api.decideFactoryControlAction).mockReset(); });

	it('parses control proposals from the factory_control tool', () => {
		expect(factoryCardFromToolResult('ocman_factory_control', '[[ocman:card type=factory-issue epic=epic-1 issue=fca_1 action=control]]')).toEqual({ type: 'factory-issue', epicID: 'epic-1', issueID: 'fca_1', action: 'control' });
		expect(factoryCardFromToolResult('ocman_factory_control', '[[ocman:card type=factory-epic epic=epic-1 action=control]]')).toBeNull();
		expect(factoryCardFromToolResult('ocman_sessions', '[[ocman:card type=factory-issue epic=epic-1 issue=fca_1 action=control]]')).toBeNull();
	});

	it('runs the change only when the user confirms', async () => {
		const user = userEvent.setup();
		vi.mocked(api.factoryControlAction).mockResolvedValue(pending);
		vi.mocked(api.decideFactoryControlAction).mockResolvedValue({ ...pending, status: 'done' });
		renderCard();
		expect(await screen.findByText('Run the tests again')).toBeInTheDocument();
		await user.click(screen.getByRole('button', { name: 'Confirm' }));
		expect(api.decideFactoryControlAction).toHaveBeenCalledWith('epic-1', 'fca_1', 'confirm');
		expect(await screen.findByRole('status')).toHaveTextContent('Done');
		expect(screen.queryByRole('button', { name: 'Confirm' })).not.toBeInTheDocument();
	});

	it('dismisses, and shows decided proposals and errors', async () => {
		const user = userEvent.setup();
		vi.mocked(api.factoryControlAction).mockResolvedValue(pending);
		vi.mocked(api.decideFactoryControlAction).mockRejectedValue(new Error('already dismissed'));
		const { unmount } = renderCard();
		await user.click(await screen.findByRole('button', { name: 'Dismiss' }));
		expect(api.decideFactoryControlAction).toHaveBeenCalledWith('epic-1', 'fca_1', 'dismiss');
		expect(await screen.findByRole('alert')).toHaveTextContent('already dismissed');
		unmount();
		vi.mocked(api.factoryControlAction).mockResolvedValue({ ...pending, status: 'failed', result: 'gate is not part of the Epic' });
		renderCard();
		expect(await screen.findByRole('status')).toHaveTextContent('Failed: gate is not part of the Epic');
	});

	it('reports a proposal it cannot load', async () => {
		vi.mocked(api.factoryControlAction).mockRejectedValue(new Error('gone'));
		renderCard();
		expect(await screen.findByRole('alert')).toHaveTextContent('Could not load the proposed change');
	});
});

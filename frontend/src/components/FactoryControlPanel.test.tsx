// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter, Route, Routes, useLocation } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { api } from '../lib/api';
import { FactoryControlPanel } from './FactoryControlPanel';

vi.mock('../lib/api', () => ({ api: { factoryControl: vi.fn(), openFactoryControl: vi.fn() } }));

function Location() {
	return <p>{useLocation().pathname + useLocation().search}</p>;
}

function renderPanel(closed = false) {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	return render(<QueryClientProvider client={client}><MemoryRouter initialEntries={['/factory/epics/epic-1']}><Routes><Route path="/factory/epics/:id" element={<FactoryControlPanel epicID="epic-1" closed={closed} />} /><Route path="/session/:id" element={<Location />} /></Routes></MemoryRouter></QueryClientProvider>);
}

describe('FactoryControlPanel', () => {
	beforeEach(() => { vi.mocked(api.factoryControl).mockReset(); vi.mocked(api.openFactoryControl).mockReset(); });

	it('starts the control session and opens it', async () => {
		const user = userEvent.setup();
		vi.mocked(api.factoryControl).mockResolvedValue(null);
		vi.mocked(api.openFactoryControl).mockResolvedValue({ epicId: 'epic-1', platform: 'opencode', sessionId: 'ses-1' });
		renderPanel();
		await user.click(await screen.findByRole('button', { name: 'Start control session' }));
		expect(api.openFactoryControl).toHaveBeenCalledWith('epic-1');
		expect(await screen.findByText('/session/ses-1?factoryEpic=epic-1')).toBeInTheDocument();
	});

	it('opens an existing control session through the server, which may replace it', async () => {
		const user = userEvent.setup();
		vi.mocked(api.factoryControl).mockResolvedValue({ epicId: 'epic-1', platform: 'opencode', sessionId: 'ses-2' });
		vi.mocked(api.openFactoryControl).mockResolvedValue({ epicId: 'epic-1', platform: 'opencode', sessionId: 'ses-3' });
		renderPanel();
		await user.click(await screen.findByRole('button', { name: 'Open control session' }));
		expect(api.openFactoryControl).toHaveBeenCalledWith('epic-1');
		expect(await screen.findByText('/session/ses-3?factoryEpic=epic-1')).toBeInTheDocument();
	});

	it('opens a closed epic\'s archived session without starting a new one', async () => {
		const user = userEvent.setup();
		vi.mocked(api.factoryControl).mockResolvedValue({ epicId: 'epic-1', platform: 'opencode', sessionId: 'ses-2' });
		renderPanel(true);
		await user.click(await screen.findByRole('button', { name: 'Open control session' }));
		expect(api.openFactoryControl).not.toHaveBeenCalled();
		expect(await screen.findByText('/session/ses-2?factoryEpic=epic-1')).toBeInTheDocument();
	});

	it('reports a failed start and hides for a closed epic without one', async () => {
		const user = userEvent.setup();
		vi.mocked(api.factoryControl).mockResolvedValue(null);
		vi.mocked(api.openFactoryControl).mockRejectedValue(new Error('No local OpenCode'));
		const { unmount } = renderPanel();
		await user.click(await screen.findByRole('button', { name: 'Start control session' }));
		expect(await screen.findByRole('alert')).toHaveTextContent('No local OpenCode');
		unmount();
		renderPanel(true);
		await vi.waitFor(() => expect(api.factoryControl).toHaveBeenCalledTimes(2));
		expect(screen.queryByRole('region', { name: 'Control session' })).not.toBeInTheDocument();
	});
});

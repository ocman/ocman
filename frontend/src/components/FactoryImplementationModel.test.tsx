// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi } from 'vitest';
import { api, type FactoryEpic } from '../lib/api';
import { FactoryImplementationModel } from './FactoryImplementationModel';
import { implementationModelTier, useFactoryImplementationModel } from './useFactoryImplementationModel';

vi.mock('../lib/api', () => ({ api: { sessionModels: vi.fn() } }));

const epic = { id: 'epic', attempts: [{ session: { id: 'plan', platform: 'agent' } }], planGate: { proposalHash: 'hash', resolution: 'open' } } as FactoryEpic;
function Picker({ value = epic }: { value?: FactoryEpic }) {
	return <FactoryImplementationModel {...useFactoryImplementationModel(value)} />;
}
const picker = () => screen.getByRole('combobox', { name: 'Implementation model' });
function mount(value = epic) {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	return render(<QueryClientProvider client={client}><Picker value={value} /></QueryClientProvider>);
}

describe('implementation model choice', () => {
	it.each([
		['anthropic/fable', 'Strong'], ['openai/gpt-6-astra', 'Strong'],
		['anthropic/claude-opus-4', 'Balanced'], ['openai/gpt-5.6-sol', 'Balanced'],
		['anthropic/claude-sonnet-4', 'Fast'], ['openai/terra', 'Fast'], ['provider/other', 'Other'],
	])('labels %s as %s', (model, tier) => expect(implementationModelTier(model)).toBe(tier));

	it('suggests available balanced models and allows the runtime default', async () => {
		vi.mocked(api.sessionModels).mockResolvedValue({ models: [
			{ provider: 'p', model: 'opus', isAvailable: false }, { provider: 'p', model: 'terra' }, { provider: 'p', model: 'sol' },
		], hasProviders: true });
		mount();
		await waitFor(() => expect(picker()).toHaveTextContent('p/sol'));
		const user = userEvent.setup();
		await user.click(picker());
		expect(screen.getByRole('option', { name: /terra/ })).toBeInTheDocument();
		expect(screen.queryByRole('option', { name: /opus/ })).not.toBeInTheDocument();
		await user.click(screen.getByRole('option', { name: 'Runtime default' }));
		expect(picker()).toHaveTextContent('Runtime default');
	});

	it('suggests a fast model when no balanced model is available', async () => {
		vi.mocked(api.sessionModels).mockResolvedValue({ models: [{ provider: 'p', model: 'terra' }], hasProviders: true });
		mount();
		await waitFor(() => expect(picker()).toHaveTextContent('p/terra'));
	});

	it('keeps historical models when provider availability is unknown', async () => {
		vi.mocked(api.sessionModels).mockResolvedValue({ models: [{ provider: 'p', model: 'sol', isAvailable: false }], hasProviders: false });
		mount();
		await waitFor(() => expect(picker()).toHaveTextContent('p/sol'));
		await userEvent.setup().click(picker());
		expect(screen.getByRole('option', { name: /sol/ })).toBeInTheDocument();
	});

	it('keeps approval usable when the catalog fails', async () => {
		vi.mocked(api.sessionModels).mockRejectedValue(new Error('offline'));
		mount();
		expect(await screen.findByText('Could not load models. Runtime default is available.')).toBeInTheDocument();
		expect(picker()).toBeEnabled();
	});

	it('locks the saved model when retrying an approved plan', () => {
		mount({ ...epic, planGate: { ...epic.planGate!, resolution: 'approved', implementationModel: 'p/sol' } });
		expect(picker()).toHaveTextContent('p/sol');
		expect(picker()).toBeDisabled();
	});

	it('retains an approved runtime default as the initial amendment choice', async () => {
		vi.mocked(api.sessionModels).mockResolvedValue({ models: [{ provider: 'p', model: 'sol' }], hasProviders: true });
		mount({ ...epic, proposal: { revision: 2, contentHash: 'new', manifest: { epicId: 'epic', molId: 'mol', project: '/repo', nodes: [], baseRevision: 1 } } });
		await waitFor(() => expect(picker()).toBeEnabled());
		expect(picker()).toHaveTextContent('Runtime default');
	});
});

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
		await waitFor(() => expect(screen.getByLabelText('Implementation model')).toHaveValue('p/sol'));
		expect(screen.queryByRole('option', { name: /p\/opus/ })).not.toBeInTheDocument();
		await userEvent.setup().selectOptions(screen.getByLabelText('Implementation model'), '');
		expect(screen.getByLabelText('Implementation model')).toHaveValue('');
	});

	it('suggests a fast model when no balanced model is available', async () => {
		vi.mocked(api.sessionModels).mockResolvedValue({ models: [{ provider: 'p', model: 'terra' }], hasProviders: true });
		mount();
		await waitFor(() => expect(screen.getByLabelText('Implementation model')).toHaveValue('p/terra'));
	});

	it('keeps approval usable when the catalog fails', async () => {
		vi.mocked(api.sessionModels).mockRejectedValue(new Error('offline'));
		mount();
		expect(await screen.findByText('Could not load models. Runtime default is available.')).toBeInTheDocument();
		expect(screen.getByLabelText('Implementation model')).toBeEnabled();
	});

	it('locks the saved model when retrying an approved plan', () => {
		mount({ ...epic, planGate: { ...epic.planGate!, resolution: 'approved', implementationModel: 'p/sol' } });
		expect(screen.getByLabelText('Implementation model')).toHaveValue('p/sol');
		expect(screen.getByLabelText('Implementation model')).toBeDisabled();
	});
});

// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi } from 'vitest';
import { api, postJSON } from '../lib/api';
import { FactoryEpicModels } from './FactoryEpicModels';
import { useFactoryImplementationModel } from './useFactoryImplementationModel';
import type { FactoryEpicWithModels } from './useFactoryEpicModels';

vi.mock('../lib/api', () => ({ api: { getJudgeModelOptions: vi.fn(), sessionModels: vi.fn() }, postJSON: vi.fn() }));

const epic = { id: 'epic/1', status: 'open', models: { plan: 'p/fable' } } as FactoryEpicWithModels;
function mount(ui: React.ReactNode) {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	return render(<QueryClientProvider client={client}>{ui}</QueryClientProvider>);
}

describe('epic models', () => {
	it('saves a per-phase model and keeps the others', async () => {
		vi.mocked(api.getJudgeModelOptions).mockResolvedValue({ models: ['p/fable', 'p/sol', 'p/terra'], default: '' });
		vi.mocked(postJSON).mockResolvedValue({});
		mount(<FactoryEpicModels epic={epic} />);
		expect(screen.getByRole('combobox', { name: 'Planning model' })).toHaveTextContent('p/fable');
		await waitFor(() => expect(api.getJudgeModelOptions).toHaveBeenCalled());
		const user = userEvent.setup();
		await user.click(screen.getByRole('combobox', { name: 'Verification model' }));
		await user.keyboard('terra');
		await user.click(await screen.findByRole('option', { name: /terra/ }));
		expect(postJSON).toHaveBeenCalledWith('/api/factory/epics/epic%2F1/models', { plan: 'p/fable', verification: 'p/terra' });
		expect(screen.getByText('Changes apply only to work that has not started yet.')).toBeInTheDocument();
	});

	it('shows a saved model even when the catalog is unavailable', async () => {
		vi.mocked(api.getJudgeModelOptions).mockRejectedValue(new Error('offline'));
		mount(<FactoryEpicModels epic={{ ...epic, models: { implementation: 'x/custom' } }} />);
		await waitFor(() => expect(screen.getByRole('combobox', { name: 'Implementation model' })).toHaveTextContent('x/custom'));
	});

	it('locks the approval picker to the epic implementation model', () => {
		function Probe() {
			const { model, locked } = useFactoryImplementationModel({ ...epic, planGate: { issueId: 'g', proposalRevision: 1, proposalHash: 'h', resolution: 'open' }, models: { implementation: 'p/sol' } } as FactoryEpicWithModels);
			return <span>{model}:{String(locked)}</span>;
		}
		mount(<Probe />);
		expect(screen.getByText('p/sol:true')).toBeInTheDocument();
	});
});

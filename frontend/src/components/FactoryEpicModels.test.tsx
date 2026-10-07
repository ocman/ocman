// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { api, postJSON } from '../lib/api';
import { FactoryEpicModels } from './FactoryEpicModels';
import { useFactoryImplementationModel } from './useFactoryImplementationModel';
import type { FactoryEpicWithModels } from './useFactoryEpicModels';

vi.mock('../lib/api', () => ({ api: { prepareSession: vi.fn(), sessionModels: vi.fn() }, postJSON: vi.fn() }));

const epic = { id: 'epic/1', initialProject: '/repo', status: 'open', models: { plan: 'p/fable' } } as FactoryEpicWithModels;
const catalog = { platform: 'opencode', agents: [], commands: [], liveConnection: true, models: { hasProviders: true, models: ['fable', 'sol', 'terra'].map((model) => ({ provider: 'p', model, isAvailable: true })) } };
function mount(ui: React.ReactNode) {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	return render(<QueryClientProvider client={client}>{ui}</QueryClientProvider>);
}

describe('epic models', () => {
	beforeEach(() => { vi.clearAllMocks(); vi.mocked(api.prepareSession).mockResolvedValue(catalog); });

	it('saves a per-phase model and keeps the others', async () => {
		vi.mocked(postJSON).mockResolvedValue({});
		mount(<FactoryEpicModels epic={epic} />);
		expect(screen.getByRole('combobox', { name: 'Planning model' })).toHaveTextContent('p/fable');
		await waitFor(() => expect(api.prepareSession).toHaveBeenCalled());
		const user = userEvent.setup();
		await user.click(screen.getByRole('combobox', { name: 'Verification model' }));
		await user.keyboard('terra');
		await user.click(await screen.findByRole('option', { name: /terra/ }));
		expect(postJSON).toHaveBeenCalledWith('/api/factory/epics/epic%2F1/models', { plan: 'p/fable', verification: 'p/terra' });
		expect(screen.getByText('Changes apply only to work that has not started yet.')).toBeInTheDocument();
	});

	it('shows a saved model even when the catalog is unavailable', async () => {
		vi.mocked(api.prepareSession).mockRejectedValue(new Error('offline'));
		mount(<FactoryEpicModels epic={{ ...epic, models: { implementation: 'x/custom' } }} />);
		await waitFor(() => expect(screen.getByRole('combobox', { name: 'Implementation model' })).toHaveTextContent('x/custom'));
	});

	it('finds project-specific models by display name across all epic projects', async () => {
		vi.mocked(api.prepareSession).mockImplementation(async ({ directory }) => directory === '/spark' ? { ...catalog, models: { hasProviders: true, models: [{ provider: 'spark', model: 'qwen-flash', modelName: 'Qwen3.8 Flash Next NVFP4 256K (Spark)' }] } } : catalog);
		mount(<FactoryEpicModels epic={{ ...epic, projects: [{ path: '/repo', removable: false }, { path: '/spark', removable: true }] }} />);
		const user = userEvent.setup();
		await user.click(screen.getByRole('combobox', { name: 'Planning model' }));
		await user.keyboard('Qwen3.8 Flash Next NVFP4 256K (Spark)');
		await user.click(await screen.findByRole('option', { name: /spark\/qwen-flash/ }));
		expect(postJSON).toHaveBeenCalledWith('/api/factory/epics/epic%2F1/models', { plan: 'spark/qwen-flash' });
		expect(api.prepareSession).toHaveBeenCalledWith({ directory: '/spark', remoteId: 'local' }, expect.any(AbortSignal));
		expect(api.prepareSession).toHaveBeenCalledTimes(2);
	});

	it('allows an unlisted model ID when discovery fails and offers restart guidance', async () => {
		vi.mocked(api.prepareSession).mockRejectedValue(new Error('offline'));
		mount(<FactoryEpicModels epic={epic} />);
		await screen.findByText(/Could not load models/);
		const user = userEvent.setup();
		await user.click(screen.getByRole('combobox', { name: 'Verification model' }));
		await user.keyboard('spark/qwen-flash');
		await user.click(await screen.findByRole('option', { name: 'Use spark/qwen-flash' }));
		expect(postJSON).toHaveBeenCalledWith('/api/factory/epics/epic%2F1/models', { plan: 'p/fable', verification: 'spark/qwen-flash' });
		expect(screen.getByText(/Restart the project.*OpenCode instance/)).toBeInTheDocument();
		vi.mocked(api.prepareSession).mockResolvedValue(catalog);
		await user.click(screen.getByRole('button', { name: 'Refresh models' }));
		await waitFor(() => expect(screen.queryByText(/Could not load models/)).not.toBeInTheDocument());
	});

	it('keeps a successful project catalog selectable when another project fails', async () => {
		vi.mocked(api.prepareSession).mockImplementation(async ({ directory }) => {
			if (directory === '/offline') throw new Error('offline');
			return { ...catalog, liveConnection: false, models: { hasProviders: false, models: [{ provider: 'spark', model: 'qwen-flash', isAvailable: false }] } };
		});
		mount(<FactoryEpicModels epic={{ ...epic, projects: [{ path: '/offline', removable: true }, { path: '/duplicate', removable: true }] }} />);
		await screen.findByText(/Could not load models/);
		const user = userEvent.setup();
		await user.click(screen.getByRole('combobox', { name: 'Implementation model' }));
		expect(screen.getAllByRole('option', { name: 'spark/qwen-flash' })).toHaveLength(1);
		expect(screen.queryByText('provider disconnected')).not.toBeInTheDocument();
		await user.click(screen.getByRole('option', { name: 'spark/qwen-flash' }));
		expect(postJSON).toHaveBeenCalledWith('/api/factory/epics/epic%2F1/models', { plan: 'p/fable', implementation: 'spark/qwen-flash' });
	});

	it.each(['/repo', '/history'])('preserves live names when %s is the first project', async (initialProject) => {
		const model = { provider: 'spark', model: 'qwen-flash' };
		vi.mocked(api.prepareSession).mockImplementation(async ({ directory }) => ({ ...catalog, models: {
			hasProviders: directory === '/repo',
			models: [directory === '/repo' ? { ...model, providerName: 'NVIDIA Spark', modelName: 'Qwen3.8 Flash Next NVFP4 256K (Spark)' } : model],
		} }));
		mount(<FactoryEpicModels epic={{ ...epic, initialProject, projects: [{ path: '/repo', removable: false }, { path: '/history', removable: true }] }} />);
		const user = userEvent.setup();
		await user.click(screen.getByRole('combobox', { name: 'Planning model' }));
		await user.keyboard('Qwen3.8 Flash Next NVFP4 256K (Spark)');
		expect(await screen.findByRole('option', { name: 'spark/qwen-flash' })).toBeInTheDocument();
		await user.clear(screen.getByRole('textbox', { name: 'Search models' }));
		await user.keyboard('NVIDIA Spark');
		await user.click(screen.getByRole('option', { name: 'spark/qwen-flash' }));
		expect(postJSON).toHaveBeenCalledWith('/api/factory/epics/epic%2F1/models', { plan: 'spark/qwen-flash' });
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

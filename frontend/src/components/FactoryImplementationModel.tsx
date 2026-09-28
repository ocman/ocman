import type { useFactoryImplementationModel } from './useFactoryImplementationModel';
import { ModelSelect } from './ModelSelect';

export function FactoryImplementationModel({ model, models, entries, setModel, loading, error, locked }: ReturnType<typeof useFactoryImplementationModel>) {
	return <span>
		<label>Implementation model<ModelSelect value={model} models={models} modelEntries={entries} onChange={setModel} ariaLabel="Implementation model" defaultLabel="Runtime default" disabled={loading || locked} /></label>
		<span>Planning: Fable / Astra. Implementation: Opus / Sol recommended; Sonnet / Terra for speed.</span>
		{loading && <span role="status">Loading available models…</span>}
		{error && <span role="status">Could not load models. Runtime default is available.</span>}
	</span>;
}
